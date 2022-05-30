package spammer

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/atomic"

	hivedaemon "github.com/iotaledger/hive.go/daemon"
	"github.com/iotaledger/hive.go/datastructure/timeheap"
	"github.com/iotaledger/hive.go/events"
	"github.com/iotaledger/hive.go/logger"
	"github.com/iotaledger/hive.go/math"
	"github.com/iotaledger/hive.go/syncutils"
	iotago "github.com/iotaledger/iota.go/v3"
	builder "github.com/iotaledger/iota.go/v3/builder"

	"github.com/iotaledger/inx-spammer/pkg/common"
	"github.com/iotaledger/inx-spammer/pkg/daemon"
	"github.com/iotaledger/inx-spammer/pkg/pow"
)

type (
	// IsNodeHealthyFunc returns whether the node is synced, has active peers and its latest milestone is not too old.
	IsNodeHealthyFunc = func() (bool, error)

	// GetTipsPoolSizesFunc returns the current tip pool sizes of the node.
	GetTipsPoolSizesFunc = func() (uint32, uint32)

	// RequestTipsFunc returns tips choosen by the node.
	RequestTipsFunc = func(ctx context.Context, count uint32, allowSemiLazy bool) (iotago.BlockIDs, error)

	// SendBlockFunc is a function which sends a block to the network.
	SendBlockFunc = func(ctx context.Context, block *iotago.Block) (iotago.BlockID, error)
)

// Spammer is used to issue blocks to the IOTA network to create load on the tangle.
type Spammer struct {
	// the logger used to log events.
	*logger.WrappedLogger
	syncutils.RWMutex

	protoParas            *iotago.ProtocolParameters
	bpsRateLimit          float64
	cpuMaxUsage           float64
	workersCount          int
	message               string
	tag                   string
	tagSemiLazy           string
	nonLazyTipsThreshold  uint32
	semiLazyTipsThreshold uint32
	getTipsPoolSizesFunc  GetTipsPoolSizesFunc
	requestTipsFunc       RequestTipsFunc
	isNodeHealthyFunc     IsNodeHealthyFunc
	sendBlockFunc         SendBlockFunc
	spammerMetrics        *SpammerMetrics
	cpuUsageUpdater       *CPUUsageUpdater
	daemon                hivedaemon.Daemon

	spammerStartTime   time.Time
	spammerAvgHeap     *timeheap.TimeHeap
	lastSentSpamBlocks uint32

	isRunning           bool
	bpsRateLimitRunning float64
	cpuMaxUsageRunning  float64
	workersCountRunning int

	processID        atomic.Uint32
	spammerWaitGroup sync.WaitGroup

	Events *SpammerEvents
}

// New creates a new spammer instance.
func New(
	protoParas *iotago.ProtocolParameters,
	bpsRateLimit float64,
	cpuMaxUsage float64,
	workersCount int,
	message string,
	tag string,
	tagSemiLazy string,
	nonLazyTipsThreshold uint32,
	semiLazyTipsThreshold uint32,
	getTipsPoolSizesFunc GetTipsPoolSizesFunc,
	requestTipsFunc RequestTipsFunc,
	isNodeHealthyFunc IsNodeHealthyFunc,
	sendBlockFunc SendBlockFunc,
	spammerMetrics *SpammerMetrics,
	cpuUsageUpdater *CPUUsageUpdater,
	daemon hivedaemon.Daemon,
	log *logger.Logger) *Spammer {

	if workersCount == 0 {
		workersCount = runtime.NumCPU() - 1
	}

	return &Spammer{
		WrappedLogger:         logger.NewWrappedLogger(log),
		protoParas:            protoParas,
		bpsRateLimit:          bpsRateLimit,
		cpuMaxUsage:           cpuMaxUsage,
		workersCount:          workersCount,
		message:               message,
		tag:                   tag,
		tagSemiLazy:           tagSemiLazy,
		nonLazyTipsThreshold:  nonLazyTipsThreshold,
		semiLazyTipsThreshold: semiLazyTipsThreshold,
		getTipsPoolSizesFunc:  getTipsPoolSizesFunc,
		requestTipsFunc:       requestTipsFunc,
		isNodeHealthyFunc:     isNodeHealthyFunc,
		sendBlockFunc:         sendBlockFunc,
		spammerMetrics:        spammerMetrics,
		cpuUsageUpdater:       cpuUsageUpdater,
		daemon:                daemon,
		// Events are the events of the spammer
		Events: &SpammerEvents{
			SpamPerformed:         events.NewEvent(SpamStatsCaller),
			AvgSpamMetricsUpdated: events.NewEvent(AvgSpamMetricsCaller),
		},
		spammerAvgHeap: timeheap.NewTimeHeap(),
	}
}

func (s *Spammer) selectSpammerTips(ctx context.Context) (isSemiLazy bool, tips iotago.BlockIDs, err error) {
	nonLazyPoolSize, semiLazyPoolSize := s.getTipsPoolSizesFunc()

	if s.semiLazyTipsThreshold != 0 && semiLazyPoolSize > s.semiLazyTipsThreshold {
		// threshold was defined and reached, return semi-lazy tips for the spammer
		tips, err = s.requestTipsFunc(ctx, 8, true)
		if err != nil {
			return false, nil, fmt.Errorf("couldn't select semi-lazy tips: %w", err)
		}

		if len(tips) < 2 {
			// do not spam if the amount of tips are less than 2 since that would not reduce the semi lazy count
			return false, nil, fmt.Errorf("%w: semi lazy tips are equal", common.ErrNoTipsAvailable)
		}

		return true, tips, nil
	}

	if s.nonLazyTipsThreshold != 0 && nonLazyPoolSize < s.nonLazyTipsThreshold {
		// if a threshold was defined and not reached, do not return tips for the spammer
		return false, nil, fmt.Errorf("%w: non-lazy threshold not reached", common.ErrNoTipsAvailable)
	}

	tips, err = s.requestTipsFunc(ctx, 8, false)
	if err != nil {
		return false, tips, fmt.Errorf("couldn't select non-lazy tips: %w", err)
	}
	return false, tips, nil
}

func (s *Spammer) doSpam(ctx context.Context) (time.Duration, time.Duration, error) {

	timeStart := time.Now()
	isSemiLazy, tips, err := s.selectSpammerTips(ctx)
	if err != nil {
		return time.Duration(0), time.Duration(0), err
	}
	durationGTTA := time.Since(timeStart)

	tag := s.tag
	if isSemiLazy {
		tag = s.tagSemiLazy
	}

	tagBytes := []byte(tag)
	if len(tagBytes) > iotago.MaxTagLength {
		tagBytes = tagBytes[:iotago.MaxTagLength]
	}

	txCount := int(s.spammerMetrics.SentSpamBlocks.Load()) + 1

	now := time.Now()
	messageString := s.message
	messageString += fmt.Sprintf("\nCount: %06d", txCount)
	messageString += fmt.Sprintf("\nTimestamp: %s", now.Format(time.RFC3339))
	messageString += fmt.Sprintf("\nTipselection: %v", durationGTTA.Truncate(time.Microsecond))

	iotaBlock, err := builder.
		NewBlockBuilder(s.protoParas.Version).
		ParentsBlockIDs(tips).
		Payload(&iotago.TaggedData{Tag: tagBytes, Data: []byte(messageString)}).
		Build()
	if err != nil {
		return time.Duration(0), time.Duration(0), err
	}

	timeStart = time.Now()
	if _, err := pow.DoPoW(ctx, iotaBlock, float64(s.protoParas.MinPoWScore), 1, 5*time.Second, func() (tips iotago.BlockIDs, err error) {
		// refresh tips of the spammer if PoW takes longer than a configured duration.
		_, refreshedTips, err := s.selectSpammerTips(ctx)
		return refreshedTips, err
	}); err != nil {
		return time.Duration(0), time.Duration(0), err
	}
	durationPOW := time.Since(timeStart)

	if _, err := s.sendBlockFunc(ctx, iotaBlock); err != nil {
		return time.Duration(0), time.Duration(0), err
	}
	s.spammerMetrics.SentSpamBlocks.Inc()

	return durationGTTA, durationPOW, nil
}

func (s *Spammer) startSpammerWorkers(bpsRateLimit float64, cpuMaxUsage float64, spammerWorkerCount int) {
	s.bpsRateLimitRunning = bpsRateLimit
	s.cpuMaxUsageRunning = cpuMaxUsage
	s.workersCountRunning = spammerWorkerCount
	s.isRunning = true

	var rateLimitChannel chan struct{} = nil
	var rateLimitAbortSignal chan struct{} = nil

	if bpsRateLimit != 0.0 {
		rateLimitChannel = make(chan struct{}, spammerWorkerCount*2)
		rateLimitAbortSignal = make(chan struct{})

		// create a background worker that fills rateLimitChannel every second
		if err := s.daemon.BackgroundWorker("Spammer rate limit channel", func(ctx context.Context) {
			s.spammerWaitGroup.Add(1)
			defer s.spammerWaitGroup.Done()

			currentProcessID := s.processID.Load()
			interval := time.Duration(int64(float64(time.Second) / bpsRateLimit))
			timeout := interval * 2
			if timeout < time.Second {
				timeout = time.Second
			}

			var lastDuration time.Duration
		rateLimitLoop:
			for {
				timeStart := time.Now()

				rateLimitCtx, rateLimitCtxCancel := context.WithTimeout(context.Background(), timeout)

				if currentProcessID != s.processID.Load() {
					close(rateLimitAbortSignal)
					rateLimitCtxCancel()
					break rateLimitLoop
				}

				// measure the last interval error and multiply by two to compensate (to reach target BPS)
				lastIntervalError := (lastDuration - interval) * 2.0
				if lastIntervalError < 0 {
					lastIntervalError = 0
				}
				time.Sleep(interval - lastIntervalError)

				select {
				case <-ctx.Done():
					// received shutdown signal
					close(rateLimitAbortSignal)
					rateLimitCtxCancel()
					break rateLimitLoop

				case rateLimitChannel <- struct{}{}:
					// wait until a worker is free

				case <-rateLimitCtx.Done():
					// timeout if the channel is not free in time
					// maybe the consumer was shut down
				}

				rateLimitCtxCancel()
				lastDuration = time.Since(timeStart)
			}

		}, daemon.PriorityStopSpammer); err != nil {
			s.LogPanicf("failed to start worker: %s", err)
		}
	}

	spammerCnt := atomic.NewInt32(0)
	for i := 0; i < spammerWorkerCount; i++ {
		if err := s.daemon.BackgroundWorker(fmt.Sprintf("Spammer_%d", i), func(ctx context.Context) {
			s.spammerWaitGroup.Add(1)
			defer s.spammerWaitGroup.Done()

			spammerIndex := spammerCnt.Inc()
			currentProcessID := s.processID.Load()

			s.LogInfof("Starting Spammer %d... done", spammerIndex)

		spammerLoop:
			for {
				select {
				case <-ctx.Done():
					break spammerLoop

				default:
					if currentProcessID != s.processID.Load() {
						break spammerLoop
					}

					if bpsRateLimit != 0 {
						// if rateLimit is activated, wait until this spammer thread gets a signal
						select {
						case <-rateLimitAbortSignal:
							break spammerLoop
						case <-ctx.Done():
							break spammerLoop
						case <-rateLimitChannel:
						}
					}

					isHealthy, err := s.isNodeHealthyFunc()
					if err != nil {
						s.LogWarn(err)
						continue
					}

					if !isHealthy {
						time.Sleep(time.Second)
						continue
					}

					if err := s.cpuUsageUpdater.WaitForLowerCPUUsage(ctx, cpuMaxUsage); err != nil {
						if !errors.Is(err, common.ErrOperationAborted) {
							s.LogWarn(err)
						}
						continue
					}

					if s.spammerStartTime.IsZero() {
						// set the start time for the metrics
						s.spammerStartTime = time.Now()
					}

					durationGTTA, durationPOW, err := s.doSpam(ctx)
					if err != nil {
						continue
					}
					s.Events.SpamPerformed.Trigger(&SpamStats{Tipselection: float32(durationGTTA.Seconds()), ProofOfWork: float32(durationPOW.Seconds())})
				}
			}

			s.LogInfof("Stopping Spammer %d...", spammerIndex)
			s.LogInfof("Stopping Spammer %d... done", spammerIndex)
		}, daemon.PriorityStopSpammer); err != nil {
			s.LogWarnf("failed to start worker: %s", err)
		}
	}
}

func (s *Spammer) stopWithoutLocking() {
	// increase the process ID to stop all running workers
	s.processID.Inc()

	// wait until all spammers are stopped
	s.spammerWaitGroup.Wait()

	// reset the start time to stop the metrics
	s.spammerStartTime = time.Time{}

	// clear the metrics heap
	for s.spammerAvgHeap.Len() > 0 {
		s.spammerAvgHeap.Pop()
	}
}

func (s *Spammer) IsRunning() bool {
	return s.isRunning
}

func (s *Spammer) BPSRateLimitRunning() float64 {
	return s.bpsRateLimitRunning
}

func (s *Spammer) CPUMaxUsageRunning() float64 {
	return s.cpuMaxUsageRunning
}

func (s *Spammer) SpammerWorkersRunning() int {
	return s.workersCountRunning
}

// Start starts the spammer to spam with the given settings, otherwise it uses the settings from the config.
func (s *Spammer) Start(bpsRateLimit *float64, cpuMaxUsage *float64, workersCount *int) error {
	s.Lock()
	defer s.Unlock()

	s.stopWithoutLocking()

	bpsRateLimitCfg := s.bpsRateLimit
	cpuMaxUsageCfg := s.cpuMaxUsage
	workersCountCfg := s.workersCount

	if bpsRateLimit != nil {
		bpsRateLimitCfg = *bpsRateLimit
	}

	if cpuMaxUsage != nil {
		cpuMaxUsageCfg = *cpuMaxUsage
	}

	if workersCount != nil {
		workersCountCfg = *workersCount
	}

	if cpuMaxUsageCfg > 0.0 && runtime.GOOS == "windows" {
		s.LogWarn("spammer.cpuMaxUsage not supported on Windows. will be deactivated")
		cpuMaxUsageCfg = 0.0
	}

	if cpuMaxUsageCfg > 0.0 && runtime.NumCPU() == 1 {
		s.LogWarn("spammer.cpuMaxUsage not supported on single core machines. will be deactivated")
		cpuMaxUsageCfg = 0.0
	}

	if workersCountCfg >= runtime.NumCPU() || workersCountCfg == 0 {
		workersCountCfg = runtime.NumCPU() - 1
	}
	if workersCountCfg < 1 {
		workersCountCfg = 1
	}

	s.startSpammerWorkers(bpsRateLimitCfg, cpuMaxUsageCfg, workersCountCfg)

	return nil
}

// Stop stops the spammer.
func (s *Spammer) Stop() error {
	s.Lock()
	defer s.Unlock()

	s.stopWithoutLocking()

	s.isRunning = false
	s.bpsRateLimitRunning = 0.0
	s.cpuMaxUsageRunning = 0.0
	s.workersCountRunning = 0

	return nil
}

// measureSpammerMetrics measures the spammer metrics.
func (s *Spammer) MeasureSpammerMetrics() {
	if s.spammerStartTime.IsZero() {
		// Spammer not started yet
		return
	}

	sentSpamBlocks := s.spammerMetrics.SentSpamBlocks.Load()
	newBlocks := math.Uint32Diff(sentSpamBlocks, s.lastSentSpamBlocks)
	s.lastSentSpamBlocks = sentSpamBlocks

	s.spammerAvgHeap.Add(uint64(newBlocks))

	timeDiff := time.Since(s.spammerStartTime)
	if timeDiff > 60*time.Second {
		// Only filter over one minute maximum
		timeDiff = 60 * time.Second
	}

	// trigger events for outside listeners
	s.Events.AvgSpamMetricsUpdated.Trigger(&AvgSpamMetrics{
		NewBlocks:              newBlocks,
		AverageBlocksPerSecond: s.spammerAvgHeap.AveragePerSecond(timeDiff),
	})
}
