package spammer

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/atomic"

	"github.com/iotaledger/hive.go/core/contextutils"
	hivedaemon "github.com/iotaledger/hive.go/core/daemon"
	"github.com/iotaledger/hive.go/core/datastructure/timeheap"
	"github.com/iotaledger/hive.go/core/events"
	"github.com/iotaledger/hive.go/core/logger"
	"github.com/iotaledger/hive.go/core/math"
	"github.com/iotaledger/hive.go/core/syncutils"
	"github.com/iotaledger/inx-app/pow"
	"github.com/iotaledger/inx-spammer/pkg/common"
	"github.com/iotaledger/inx-spammer/pkg/daemon"
	"github.com/iotaledger/inx-spammer/pkg/hdwallet"
	iotago "github.com/iotaledger/iota.go/v3"
	builder "github.com/iotaledger/iota.go/v3/builder"
	"github.com/iotaledger/iota.go/v3/nodeclient"
)

const (
	AddressIndexSender   = 0
	AddressIndexReceiver = 1
)

const (
	IndexerQueryMaxResults = 1000
	IndexerQueryTimeout    = 15 * time.Second
)

type outputState byte

const (
	// create basic output on sender address.
	stateBasicOutputCreate outputState = iota
	// send basic output from sender address to receiver address.
	stateBasicOutputSend
	// create alias output on sender address.
	stateAliasOutputCreate
	// do an alias output state transition on sender address.
	stateAliasOutputStateTransition
	// create a token foundry output on sender address.
	stateFoundryOutputCreate
	// mint native tokens on sender address.
	stateFoundryOutputMintNativeTokens
	// sent native tokens from sender address to receiver address.
	stateBasicOutputSendNativeTokens
	// transition ownership of alias output from sender to receiver.
	stateAliasOutputGovernanceTransition
	// melt native tokens on receiver address.
	stateFoundryOutputMeltNativeTokens
	// destroy foundry output on receiver address.
	stateFoundryOutputDestroy
	// destroy alias output on receiver address.
	stateAliasOutputDestroy
	// create NFT output on sender address.
	stateNFTOutputCreate
	// send NFT output from sender address to receiver address.
	stateNFTOutputSend
	// destroy NFT output on receiver address.
	stateNFTOutputDestroy
	// send basic output from receiver address to sender address.
	stateBasicOutputCollect
)

var (
	outputStateNamesMap = map[outputState]string{
		stateBasicOutputCreate:               "create basic output on sender address",
		stateBasicOutputSend:                 "send basic output from sender address to receiver address",
		stateAliasOutputCreate:               "create alias output on sender address",
		stateAliasOutputStateTransition:      "do an alias output state transition on sender address",
		stateFoundryOutputCreate:             "create a token foundry output on sender address",
		stateFoundryOutputMintNativeTokens:   "mint native tokens on sender address",
		stateBasicOutputSendNativeTokens:     "sent native tokens from sender address to receiver address",
		stateAliasOutputGovernanceTransition: "transition ownership of alias output from sender to receiver",
		stateFoundryOutputMeltNativeTokens:   "melt native tokens on receiver address",
		stateFoundryOutputDestroy:            "destroy foundry output on receiver address",
		stateAliasOutputDestroy:              "destroy alias output on receiver address",
		stateNFTOutputCreate:                 "create NFT output on sender address",
		stateNFTOutputSend:                   "send NFT output from sender address to receiver address",
		stateNFTOutputDestroy:                "destroy NFT output on receiver address",
		stateBasicOutputCollect:              "send basic output from receiver address to sender address",
	}
)

type (
	// IsNodeHealthyFunc returns whether the node is synced, has active peers and its latest milestone is not too old.
	IsNodeHealthyFunc = func() (bool, error)

	// ProtocolParametersFunc returns the latest protocol parameters from the node.
	ProtocolParametersFunc = func() *iotago.ProtocolParameters

	// GetTipsPoolSizesFunc returns the current tip pool sizes of the node.
	GetTipsPoolSizesFunc = func() (uint32, uint32)

	// RequestTipsFunc returns tips chosen by the node.
	RequestTipsFunc = func(ctx context.Context, count uint32, allowSemiLazy bool) (iotago.BlockIDs, error)

	// SendBlockFunc is a function which sends a block to the network.
	SendBlockFunc = func(ctx context.Context, block *iotago.Block) (iotago.BlockID, error)

	// BlockMetadataFunc is a function to fetch the required metadata for a given block ID.
	// This should return nil if the block is not found.
	BlockMetadataFunc = func(blockID iotago.BlockID) (*Metadata, error)

	// pendingTransaction holds info about a sent transaction that is pending.
	pendingTransaction struct {
		BlockID        iotago.BlockID
		TransactionID  iotago.TransactionID
		ConsumedInputs iotago.OutputIDs
	}

	// Metadata contains the basic block metadata required by the spammer.
	Metadata struct {
		IsReferenced   bool
		IsConflicting  bool
		ShouldReattach bool
	}
)

// Spammer is used to issue blocks to the IOTA network to create load on the tangle.
type Spammer struct {
	// the logger used to log events.
	*logger.WrappedLogger
	syncutils.RWMutex

	protocolParametersFunc ProtocolParametersFunc
	nodeClient             *nodeclient.Client

	bpsRateLimit                float64
	cpuMaxUsage                 float64
	workersCount                int
	message                     string
	tag                         string
	tagSemiLazy                 string
	valueSpamEnabled            bool
	valueSpamSendBasicOutput    bool
	valueSpamCollectBasicOutput bool
	valueSpamCreateAlias        bool
	valueSpamDestroyAlias       bool
	valueSpamCreateFoundry      bool
	valueSpamDestroyFoundry     bool
	valueSpamMintNativeToken    bool
	valueSpamMeltNativeToken    bool
	valueSpamCreateNFT          bool
	valueSpamDestroyNFT         bool
	nonLazyTipsThreshold        uint32
	semiLazyTipsThreshold       uint32
	refreshTipsInterval         time.Duration
	getTipsPoolSizesFunc        GetTipsPoolSizesFunc
	requestTipsFunc             RequestTipsFunc
	isNodeHealthyFunc           IsNodeHealthyFunc
	sendBlockFunc               SendBlockFunc
	blockMetadataFunc           BlockMetadataFunc
	spammerMetrics              *Metrics
	cpuUsageUpdater             *CPUUsageUpdater
	daemon                      hivedaemon.Daemon

	indexer nodeclient.IndexerClient

	spammerStartTime   time.Time
	spammerAvgHeap     *timeheap.TimeHeap
	lastSentSpamBlocks uint32

	accountSender   *LedgerAccount
	accountReceiver *LedgerAccount

	outputState outputState

	isRunning           bool
	isValueSpamEnabled  bool
	bpsRateLimitRunning float64
	cpuMaxUsageRunning  float64
	workersCountRunning int

	processID        atomic.Uint32
	spammerWaitGroup sync.WaitGroup

	ledgerMilestoneIndex        atomic.Uint32
	currentLedgerMilestoneIndex uint32

	Events *Events

	// pendingTransactionsMap is a map of sent transactions that are pending.
	pendingTransactionsMap map[iotago.BlockID]*pendingTransaction
}

// New creates a new spammer instance.
func New(
	protocolParametersFunc ProtocolParametersFunc,
	nodeClient *nodeclient.Client,
	wallet *hdwallet.HDWallet,
	bpsRateLimit float64,
	cpuMaxUsage float64,
	workersCount int,
	message string,
	tag string,
	tagSemiLazy string,
	valueSpamEnabled bool,
	valueSpamSendBasicOutput bool,
	valueSpamCollectBasicOutput bool,
	valueSpamCreateAlias bool,
	valueSpamDestroyAlias bool,
	valueSpamCreateFoundry bool,
	valueSpamDestroyFoundry bool,
	valueSpamMintNativeToken bool,
	valueSpamMeltNativeToken bool,
	valueSpamCreateNFT bool,
	valueSpamDestroyNFT bool,
	nonLazyTipsThreshold uint32,
	semiLazyTipsThreshold uint32,
	refreshTipsInterval time.Duration,
	getTipsPoolSizesFunc GetTipsPoolSizesFunc,
	requestTipsFunc RequestTipsFunc,
	isNodeHealthyFunc IsNodeHealthyFunc,
	sendBlockFunc SendBlockFunc,
	blockMetadataFunc BlockMetadataFunc,
	spammerMetrics *Metrics,
	cpuUsageUpdater *CPUUsageUpdater,
	daemon hivedaemon.Daemon,
	log *logger.Logger) (*Spammer, error) {

	if workersCount == 0 {
		workersCount = runtime.NumCPU() - 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), IndexerQueryTimeout)
	defer cancel()

	var err error
	var accountSender, accountReceiver *LedgerAccount
	var indexer nodeclient.IndexerClient
	if wallet != nil {
		// only create accounts if a wallet was provided
		accountSender, err = NewLedgerAccount(wallet, AddressIndexSender, protocolParametersFunc)
		if err != nil {
			return nil, err
		}

		accountReceiver, err = NewLedgerAccount(wallet, AddressIndexReceiver, protocolParametersFunc)
		if err != nil {
			return nil, err
		}

		log.Infof("Address for Sender: %s", accountSender.AddressBech32())
		log.Infof("Address for Receiver: %s", accountReceiver.AddressBech32())

		for {
			indexer, err = nodeClient.Indexer(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil, err
				}
				time.Sleep(time.Second)

				continue
			}

			break
		}
	}

	return &Spammer{
		WrappedLogger:               logger.NewWrappedLogger(log),
		protocolParametersFunc:      protocolParametersFunc,
		nodeClient:                  nodeClient,
		bpsRateLimit:                bpsRateLimit,
		cpuMaxUsage:                 cpuMaxUsage,
		workersCount:                workersCount,
		message:                     message,
		tag:                         tag,
		tagSemiLazy:                 tagSemiLazy,
		valueSpamEnabled:            valueSpamEnabled,
		valueSpamSendBasicOutput:    valueSpamSendBasicOutput,
		valueSpamCollectBasicOutput: valueSpamCollectBasicOutput,
		valueSpamCreateAlias:        valueSpamCreateAlias,
		valueSpamDestroyAlias:       valueSpamDestroyAlias,
		valueSpamCreateFoundry:      valueSpamCreateFoundry,
		valueSpamDestroyFoundry:     valueSpamDestroyFoundry,
		valueSpamMintNativeToken:    valueSpamMintNativeToken,
		valueSpamMeltNativeToken:    valueSpamMeltNativeToken,
		valueSpamCreateNFT:          valueSpamCreateNFT,
		valueSpamDestroyNFT:         valueSpamDestroyNFT,
		nonLazyTipsThreshold:        nonLazyTipsThreshold,
		semiLazyTipsThreshold:       semiLazyTipsThreshold,
		refreshTipsInterval:         refreshTipsInterval,
		getTipsPoolSizesFunc:        getTipsPoolSizesFunc,
		requestTipsFunc:             requestTipsFunc,
		isNodeHealthyFunc:           isNodeHealthyFunc,
		sendBlockFunc:               sendBlockFunc,
		blockMetadataFunc:           blockMetadataFunc,
		spammerMetrics:              spammerMetrics,
		cpuUsageUpdater:             cpuUsageUpdater,
		daemon:                      daemon,
		indexer:                     indexer,
		// Events are the events of the spammer
		Events: &Events{
			SpamPerformed:         events.NewEvent(SpamStatsCaller),
			AvgSpamMetricsUpdated: events.NewEvent(AvgSpamMetricsCaller),
		},
		spammerAvgHeap:         timeheap.NewTimeHeap(),
		accountSender:          accountSender,
		accountReceiver:        accountReceiver,
		pendingTransactionsMap: make(map[iotago.BlockID]*pendingTransaction),
		outputState:            stateBasicOutputCreate,
	}, nil
}

func (s *Spammer) selectSpammerTips(ctx context.Context, requiredTips iotago.BlockIDs) (isSemiLazy bool, tips iotago.BlockIDs, duration time.Duration, err error) {
	ts := time.Now()

	tips = make(iotago.BlockIDs, 0)

	// we only request as much tips as needed
	tipCount := iotago.BlockMaxParents - uint32(len(requiredTips))

	if tipCount > 0 {
		nonLazyPoolSize, semiLazyPoolSize := s.getTipsPoolSizesFunc()

		if s.semiLazyTipsThreshold != 0 && semiLazyPoolSize > s.semiLazyTipsThreshold {
			// threshold was defined and reached, return semi-lazy tips for the spammer
			tips, err = s.requestTipsFunc(ctx, tipCount, true)
			if err != nil {
				return false, nil, time.Duration(0), fmt.Errorf("couldn't select semi-lazy tips: %w", err)
			}
			tips = append(tips, requiredTips...)
			tips = tips.RemoveDupsAndSort()

			if len(tips) < 2 {
				// do not spam if the amount of tips are less than 2 since that would not reduce the semi lazy count
				return false, nil, time.Duration(0), fmt.Errorf("%w: semi lazy tips are equal", common.ErrNoTipsAvailable)
			}

			return true, tips, time.Since(ts), nil
		}

		if s.nonLazyTipsThreshold != 0 && nonLazyPoolSize < s.nonLazyTipsThreshold {
			// if a threshold was defined and not reached, do not return tips for the spammer
			return false, nil, time.Duration(0), fmt.Errorf("%w: non-lazy threshold not reached", common.ErrNoTipsAvailable)
		}

		tips, err = s.requestTipsFunc(ctx, tipCount, false)
		if err != nil {
			return false, tips, time.Duration(0), fmt.Errorf("couldn't select non-lazy tips: %w", err)
		}
	}

	tips = append(tips, requiredTips...)
	tips = tips.RemoveDupsAndSort()

	return false, tips, time.Since(ts), nil
}

func (s *Spammer) doSpam(ctx context.Context, currentProcessID uint32) error {

	if currentProcessID != s.processID.Load() {
		return nil
	}

	if !s.isValueSpamEnabled {
		return s.BuildTaggedDataBlockAndSend(ctx)
	}

	s.Lock()
	defer s.Unlock()

	if s.currentLedgerMilestoneIndex != s.ledgerMilestoneIndex.Load() {
		// stop spamming if the ledger milestone has changed
		return nil
	}

	logDebugStateErrorFunc := func(state outputState, err error) {
		s.LogDebugf("state: %d, %s failed: %s", state, outputStateNamesMap[s.outputState], err)
	}

	if s.accountSender == nil || s.accountReceiver == nil {
		logDebugStateErrorFunc(s.outputState, common.ErrMnemonicNotProvided)

		return common.ErrMnemonicNotProvided
	}

	executed := false
	for !executed {
		switch s.outputState {
		case stateBasicOutputCreate:
			if s.valueSpamSendBasicOutput {
				if err := s.basicOutputSend(ctx, s.accountSender, s.accountSender, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateBasicOutputSend

		case stateBasicOutputSend:
			if s.valueSpamSendBasicOutput {
				if err := s.basicOutputSend(ctx, s.accountSender, s.accountReceiver, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateAliasOutputCreate

		case stateAliasOutputCreate:
			if s.valueSpamCreateAlias {
				if err := s.aliasOutputCreate(ctx, s.accountSender, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateAliasOutputStateTransition

		case stateAliasOutputStateTransition:
			if s.valueSpamCreateAlias {
				if err := s.aliasOutputStateTransition(ctx, s.accountSender, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateFoundryOutputCreate

		case stateFoundryOutputCreate:
			if s.valueSpamCreateAlias && s.valueSpamCreateFoundry {
				if err := s.foundryOutputCreate(ctx, s.accountSender, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateFoundryOutputMintNativeTokens

		case stateFoundryOutputMintNativeTokens:
			if s.valueSpamCreateAlias && s.valueSpamCreateFoundry && s.valueSpamMintNativeToken {
				if err := s.foundryOutputMintNativeTokens(ctx, s.accountSender, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateBasicOutputSendNativeTokens

		case stateBasicOutputSendNativeTokens:
			if s.valueSpamCreateAlias && s.valueSpamCreateFoundry && s.valueSpamMintNativeToken {
				if err := s.basicOutputSendNativeTokens(ctx, s.accountSender, s.accountReceiver, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateAliasOutputGovernanceTransition

		case stateAliasOutputGovernanceTransition:
			if s.valueSpamCreateAlias {
				if err := s.aliasOutputGovernanceTransition(ctx, s.accountSender, s.accountReceiver, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateFoundryOutputMeltNativeTokens

		case stateFoundryOutputMeltNativeTokens:
			if s.valueSpamCreateAlias && s.valueSpamCreateFoundry && s.valueSpamMintNativeToken && s.valueSpamMeltNativeToken {
				if err := s.foundryOutputMeltNativeTokens(ctx, s.accountReceiver, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateFoundryOutputDestroy

		case stateFoundryOutputDestroy:
			if s.valueSpamCreateAlias && s.valueSpamCreateFoundry && s.valueSpamDestroyFoundry && (!s.valueSpamMintNativeToken || s.valueSpamMeltNativeToken) {
				if err := s.foundryOutputDestroy(ctx, s.accountReceiver, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateAliasOutputDestroy

		case stateAliasOutputDestroy:
			if s.valueSpamCreateAlias && s.valueSpamDestroyAlias && ((!s.valueSpamCreateFoundry || s.valueSpamDestroyFoundry) && (!s.valueSpamMintNativeToken || s.valueSpamMeltNativeToken)) {
				if err := s.aliasOutputDestroy(ctx, s.accountReceiver, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateNFTOutputCreate

		case stateNFTOutputCreate:
			if s.valueSpamCreateNFT {
				if err := s.nftOutputCreate(ctx, s.accountSender, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateNFTOutputSend

		case stateNFTOutputSend:
			if s.valueSpamCreateNFT {
				if err := s.nftOutputSend(ctx, s.accountSender, s.accountReceiver, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateNFTOutputDestroy

		case stateNFTOutputDestroy:
			if s.valueSpamCreateNFT && s.valueSpamDestroyNFT {
				if err := s.nftOutputDestroy(ctx, s.accountReceiver, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.outputState = stateBasicOutputCollect

		case stateBasicOutputCollect:
			if s.valueSpamCollectBasicOutput {
				if err := s.basicOutputSend(ctx, s.accountReceiver, s.accountSender, outputStateNamesMap[s.outputState]); err != nil {
					logDebugStateErrorFunc(s.outputState, err)
				}
				executed = true
			}
			s.cleanupOwnership()
			s.outputState = stateBasicOutputCreate
			s.LogDebug("spam cycle complete")
		}
	}

	return nil
}

// addPendingTransactionWithoutLocking tracks a pending transaction.
// write lock must be acquired outside.
func (s *Spammer) addPendingTransactionWithoutLocking(pending *pendingTransaction) {
	s.pendingTransactionsMap[pending.BlockID] = pending
}

// clearPendingTransactionWithoutLocking removes tracking of a pending transaction.
// write lock must be acquired outside.
func (s *Spammer) clearPendingTransactionWithoutLocking(blockID iotago.BlockID) {
	delete(s.pendingTransactionsMap, blockID)
}

func (s *Spammer) startSpammerWorkers(valueSpamEnabled bool, bpsRateLimit float64, cpuMaxUsage float64, spammerWorkerCount int) error {
	s.isValueSpamEnabled = valueSpamEnabled
	s.bpsRateLimitRunning = bpsRateLimit
	s.cpuMaxUsageRunning = cpuMaxUsage
	s.workersCountRunning = spammerWorkerCount
	s.isRunning = true

	if s.isValueSpamEnabled {
		if s.accountSender == nil || s.accountReceiver == nil {
			return common.ErrMnemonicNotProvided
		}

		// we only run one spammer worker for value spam in parallel,
		// but we use "workersCountRunning" threads in parallel to do the PoW.
		spammerWorkerCount = 1
	}

	var rateLimitChannel chan struct{}
	var rateLimitAbortSignal chan struct{}
	currentProcessID := s.processID.Load()

	if bpsRateLimit != 0.0 {
		rateLimitChannel = make(chan struct{}, spammerWorkerCount*2)
		rateLimitAbortSignal = make(chan struct{})

		rateLimitWorkerFunc := func(ctx context.Context) {
			s.spammerWaitGroup.Add(1)
			defer s.spammerWaitGroup.Done()

			interval := time.Duration(int64(float64(time.Second) / bpsRateLimit))
			timeout := interval * 2
			if timeout < time.Second {
				timeout = time.Second
			}

			var lastDuration time.Duration

			for {
				timeStart := time.Now()

				rateLimitCtx, rateLimitCtxCancel := context.WithTimeout(context.Background(), timeout)

				if currentProcessID != s.processID.Load() {
					close(rateLimitAbortSignal)
					rateLimitCtxCancel()

					return
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

					return

				case rateLimitChannel <- struct{}{}:
					// wait until a worker is free

				case <-rateLimitCtx.Done():
					// timeout if the channel is not free in time
					// maybe the consumer was shut down
				}

				rateLimitCtxCancel()
				lastDuration = time.Since(timeStart)
			}
		}

		// create a background worker that fills rateLimitChannel every second
		if err := s.daemon.BackgroundWorker("Spammer rate limit channel", func(ctx context.Context) {
			rateLimitWorkerFunc(ctx)
		}, daemon.PriorityStopSpammer); err != nil {
			s.LogPanicf("failed to start worker: %s", err)
		}
	}

	spammerWorkerFunc := func(ctx context.Context, spammerCnt *atomic.Int32) {
		s.spammerWaitGroup.Add(1)
		defer s.spammerWaitGroup.Done()

		spammerIndex := spammerCnt.Inc()

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

				// do spam
				if err = s.doSpam(ctx, currentProcessID); err != nil {
					continue
				}
			}
		}

		s.LogInfof("Stopping Spammer %d...", spammerIndex)
		s.LogInfof("Stopping Spammer %d... done", spammerIndex)
	}

	spammerCnt := atomic.NewInt32(0)
	for i := 0; i < spammerWorkerCount; i++ {
		if err := s.daemon.BackgroundWorker(fmt.Sprintf("Spammer_%d", i), func(ctx context.Context) {
			spammerWorkerFunc(ctx, spammerCnt)
		}, daemon.PriorityStopSpammer); err != nil {
			s.LogWarnf("failed to start worker: %s", err)
		}
	}

	return nil
}

// ATTENTION: spammer lock should not be acquired when
// calling this function because it might deadlock.
func (s *Spammer) triggerStopSignalAndWait() {
	// increase the process ID to stop all running workers
	s.processID.Inc()
	// wait until all spammers are stopped
	s.spammerWaitGroup.Wait()
}

func (s *Spammer) resetSpammerStats() {
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

func (s *Spammer) IsValueSpamEnabled() bool {
	return s.isValueSpamEnabled
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
func (s *Spammer) Start(valueSpamEnabled *bool, bpsRateLimit *float64, cpuMaxUsage *float64, workersCount *int) error {

	s.triggerStopSignalAndWait()

	s.Lock()
	defer s.Unlock()

	s.resetSpammerStats()

	valueSpamEnabledCfg := s.valueSpamEnabled
	bpsRateLimitCfg := s.bpsRateLimit
	cpuMaxUsageCfg := s.cpuMaxUsage
	workersCountCfg := s.workersCount

	if valueSpamEnabled != nil {
		valueSpamEnabledCfg = *valueSpamEnabled
	}

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

	if err := s.getCurrentSpammerLedgerState(context.Background()); err != nil {
		return err
	}

	if err := s.startSpammerWorkers(valueSpamEnabledCfg, bpsRateLimitCfg, cpuMaxUsageCfg, workersCountCfg); err != nil {
		return err
	}

	return nil
}

// Stop stops the spammer.
func (s *Spammer) Stop() error {

	s.triggerStopSignalAndWait()

	s.Lock()
	defer s.Unlock()

	s.resetSpammerStats()

	s.isRunning = false

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

// ApplyNewLedgerUpdate applies a new ledger update to the spammer.
// If a conflict is found, the spammer ledger state is rebuild by querying the indexer.
func (s *Spammer) ApplyNewLedgerUpdate(ctx context.Context, msIndex iotago.MilestoneIndex, createdOutputs iotago.OutputIDs, consumedOutputs iotago.OutputIDs) error {

	// set the atomic ledger milestone index to stop all ongoing spammers with the old ledger state
	s.ledgerMilestoneIndex.Store(msIndex)

	s.Lock()
	defer s.Unlock()

	if !s.isRunning {
		// spammer is not running
		return nil
	}

	// if we didn't provide accounts, we don't have to check for conflicts.
	if s.accountSender == nil || s.accountReceiver == nil {
		return nil
	}

	// create maps for faster lookup.
	// outputs that are created and consumed in the same milestone exist in both maps.
	newSpentsMap := make(map[iotago.OutputID]struct{})
	for _, spent := range consumedOutputs {
		newSpentsMap[spent] = struct{}{}
	}

	newOutputsMap := make(map[iotago.OutputID]struct{})
	for _, output := range createdOutputs {
		newOutputsMap[output] = struct{}{}
	}

	// check if pending transactions were affected by the ledger update.
	for _, pendingTx := range s.pendingTransactionsMap {
		// we never reuse an output, so we don't have to check for conflicts.
		outputID := iotago.OutputIDFromTransactionIDAndIndex(pendingTx.TransactionID, 0)

		if _, ok := newSpentsMap[outputID]; ok {
			// the pending transaction was affected by the ledger update.
			// remove the pending transaction from the pending transactions map.
			s.clearPendingTransactionWithoutLocking(pendingTx.BlockID)

			continue
		}

		if _, ok := newOutputsMap[outputID]; ok {
			// the pending transaction was affected by the ledger update.
			// remove the pending transaction from the pending transactions map.
			s.clearPendingTransactionWithoutLocking(pendingTx.BlockID)

			continue
		}
	}

	s.accountSender.ClearSpentOutputs(newSpentsMap)
	s.accountReceiver.ClearSpentOutputs(newSpentsMap)

	conflicting := false
	// it may happen that transactions that were created by the spammer are conflicting (e.g. block below max depth).
	// if that happens, we need to recreate our local ledger state by querying the indexer, because
	// we reuse the created outputs for further transactions, and all following transactions are conflicting as well.
	checkPendingBlockMetadata := func(pendingTx *pendingTransaction) {
		blockID := pendingTx.BlockID

		metadata, err := s.blockMetadataFunc(blockID)
		if err != nil {
			// an error occurred
			conflicting = true

			return
		}

		if metadata == nil {
			// block unknown
			conflicting = true

			return
		}

		if metadata.IsReferenced {
			if metadata.IsConflicting {
				// transaction was conflicting
				conflicting = true

				return
			}

			return
		}

		if metadata.ShouldReattach {
			// below max depth
			conflicting = true
		}
	}

	// check all remaining pending transactions
	for _, pendingTx := range s.pendingTransactionsMap {
		if conflicting {
			break
		}
		checkPendingBlockMetadata(pendingTx)
	}

	if conflicting {
		// there was a conflict in the chain
		s.resetSpammerState()

		// wait until the indexer got updated
		if err := s.waitForIndexerUpdate(ctx, msIndex); err != nil {
			return err
		}
	} else {
		// we only allow the spammer to create new transactions if there was no conflict in the last milestone.
		s.currentLedgerMilestoneIndex = s.ledgerMilestoneIndex.Load()
	}

	// it may happen that after applying all ledger changes, there are no known outputs left.
	// this mostly happens after a conflict, because updating the local state after a conflict
	// may return outputs that are confirmed in the next milestone,
	// but there are no new outputs created by the spammer.
	// in this case we query the indexer again to get the latest state.
	if s.accountSender.Empty() && s.accountReceiver.Empty() && s.isValueSpamEnabled {
		if err := s.getCurrentSpammerLedgerState(ctx); err != nil {
			// there was an error getting the current ledger state
			s.resetSpammerState()

			s.LogWarnf("failed to get current spammer ledger state: %s", err.Error())
		}
	}

	return nil
}

// resetSpammerState resets the spammer state in case of a conflict.
func (s *Spammer) resetSpammerState() {
	// forget all known outputs
	s.accountSender.ResetOutputs()
	s.accountReceiver.ResetOutputs()

	// recreate the pending transactions map
	s.pendingTransactionsMap = make(map[iotago.BlockID]*pendingTransaction)

	// reset the state if there was a conflict
	s.outputState = stateBasicOutputCreate
}

// waitForIndexerUpdate waits until the indexer got updated to the expected milestone index.
func (s *Spammer) waitForIndexerUpdate(ctx context.Context, msIndex iotago.MilestoneIndex) error {

	if s.indexer == nil {
		return nodeclient.ErrIndexerPluginNotAvailable
	}

	ctxWaitForUpdate, cancelWaitForUpdate := context.WithTimeout(ctx, IndexerQueryTimeout)
	defer cancelWaitForUpdate()

	for ctxWaitForUpdate.Err() == nil {

		// we create a dummy call to check for the ledger index in the result
		result, err := s.indexer.Outputs(ctx, &nodeclient.BasicOutputsQuery{
			IndexerCursorParas: nodeclient.IndexerCursorParas{
				PageSize: 1,
			},
		})
		if err != nil {
			return err
		}

		for result.Next() {
			if result.Response.LedgerIndex >= msIndex {
				return nil
			}
		}

		// short sleep time to reduce load on the indexer
		time.Sleep(20 * time.Millisecond)
	}

	return ctxWaitForUpdate.Err()
}

func (s *Spammer) getCurrentSpammerLedgerState(ctx context.Context) error {

	if s.accountSender == nil || s.accountReceiver == nil {
		return nil
	}

	if s.indexer == nil {
		return nodeclient.ErrIndexerPluginNotAvailable
	}

	ts := time.Now()

	ctxQuery, cancelQuery := context.WithTimeout(ctx, IndexerQueryTimeout)
	defer cancelQuery()

	// only query basic outputs with native tokens if we want to melt them
	allowNativeTokens := s.valueSpamCreateAlias && s.valueSpamCreateFoundry && s.valueSpamMintNativeToken && s.valueSpamMeltNativeToken

	// get all known outputs from the indexer (sender)
	if err := s.accountSender.QueryOutputsFromIndexer(ctxQuery, s.indexer, allowNativeTokens, true, s.valueSpamCreateAlias, s.valueSpamCreateAlias, s.valueSpamCreateNFT, IndexerQueryMaxResults); err != nil {
		return err
	}

	receiversAliasOutputsUsed := s.valueSpamCreateAlias && s.valueSpamDestroyAlias && ((!s.valueSpamCreateFoundry || s.valueSpamDestroyFoundry) && (!s.valueSpamMintNativeToken || s.valueSpamMeltNativeToken))

	// get all known outputs from the indexer (receiver)
	if err := s.accountReceiver.QueryOutputsFromIndexer(ctxQuery, s.indexer, allowNativeTokens, s.valueSpamCollectBasicOutput, receiversAliasOutputsUsed, receiversAliasOutputsUsed, s.valueSpamDestroyNFT, IndexerQueryMaxResults); err != nil {
		return err
	}

	s.LogDebugf(`getCurrentSpammerLedgerState finished, took: %v
	outputs sender:   basic: %d, alias: %d, foundry: %d, nft: %d
	outputs receiver: basic: %d, alias: %d, foundry: %d, nft: %d`, time.Since(ts).Truncate(time.Millisecond),
		s.accountSender.BasicOutputsCount(), s.accountSender.AliasOutputsCount(), s.accountSender.FoundryOutputsCount(), s.accountSender.NFTOutputsCount(),
		s.accountReceiver.BasicOutputsCount(), s.accountReceiver.AliasOutputsCount(), s.accountReceiver.FoundryOutputsCount(), s.accountReceiver.NFTOutputsCount(),
	)

	return nil
}

func (s *Spammer) composeTaggedData(isSemiLazy bool, timeGTTA time.Duration, additionalTag ...string) *iotago.TaggedData {
	// prepare tagged data
	tag := s.tag
	if isSemiLazy {
		tag = s.tagSemiLazy
	}

	tagBytes := []byte(tag)
	if len(tagBytes) > iotago.MaxTagLength {
		tagBytes = tagBytes[:iotago.MaxTagLength]
	}

	txCount := int(s.spammerMetrics.SentSpamBlocks.Load()) + 1
	messageString := s.message
	messageString += fmt.Sprintf("\nCount: %06d", txCount)
	messageString += fmt.Sprintf("\nTimestamp: %s", time.Now().Format(time.RFC3339))
	messageString += fmt.Sprintf("\nTipselection: %v", timeGTTA.Truncate(time.Microsecond))
	if len(additionalTag) > 0 {
		messageString += fmt.Sprintf("\n%s", strings.Join(additionalTag, "\n"))
	}

	return &iotago.TaggedData{Tag: tagBytes, Data: []byte(messageString)}
}

func addBalanceToOutput(output iotago.Output, balance uint64) error {

	//nolint:exhaustive // we do not need to check all output types
	switch output.Type() {
	case iotago.OutputBasic:
		//nolint:forcetypeassert // we already checked the type
		o := output.(*iotago.BasicOutput)
		o.Amount += balance
	case iotago.OutputAlias:
		//nolint:forcetypeassert // we already checked the type
		o := output.(*iotago.AliasOutput)
		o.Amount += balance
	case iotago.OutputFoundry:
		//nolint:forcetypeassert // we already checked the type
		o := output.(*iotago.FoundryOutput)
		o.Amount += balance
	case iotago.OutputNFT:
		//nolint:forcetypeassert // we already checked the type
		o := output.(*iotago.NFTOutput)
		o.Amount += balance
	default:
		return fmt.Errorf("%w: type %d", iotago.ErrUnknownOutputType, output.Type())
	}

	return nil
}

func setMinimumBalanceOfOutput(protocolParams *iotago.ProtocolParameters, output iotago.Output) error {

	minAmount := protocolParams.RentStructure.MinRent(output)

	//nolint:exhaustive // we do not need to check all output types
	switch output.Type() {
	case iotago.OutputBasic:
		//nolint:forcetypeassert // we already checked the type
		o := output.(*iotago.BasicOutput)
		if o.Amount < minAmount {
			o.Amount = minAmount
		}
	case iotago.OutputAlias:
		//nolint:forcetypeassert // we already checked the type
		o := output.(*iotago.AliasOutput)
		if o.Amount < minAmount {
			o.Amount = minAmount
		}
	case iotago.OutputFoundry:
		//nolint:forcetypeassert // we already checked the type
		o := output.(*iotago.FoundryOutput)
		if o.Amount < minAmount {
			o.Amount = minAmount
		}
	case iotago.OutputNFT:
		//nolint:forcetypeassert // we already checked the type
		o := output.(*iotago.NFTOutput)
		if o.Amount < minAmount {
			o.Amount = minAmount
		}
	default:
		return fmt.Errorf("%w: type %d", iotago.ErrUnknownOutputType, output.Type())
	}

	return nil
}

func (s *Spammer) BuildTaggedDataBlockAndSend(ctx context.Context) error {

	protocolParams := s.protocolParametersFunc()

	// select tips
	isSemiLazy, tips, durationGTTA, err := s.selectSpammerTips(ctx, iotago.BlockIDs{})
	if err != nil {
		return err
	}

	// build a block with tagged data payload
	block, err := builder.
		NewBlockBuilder().
		ProtocolVersion(protocolParams.Version).
		Parents(tips).
		Payload(s.composeTaggedData(isSemiLazy, durationGTTA)).
		Build()
	if err != nil {
		return fmt.Errorf("build block failed, error: %w", err)
	}

	timeStart := time.Now()
	// we only use 1 thread to do the PoW for tagged data spam blocks, because several threads run in parallel
	if _, err := pow.DoPoW(ctx, block, float64(protocolParams.MinPoWScore), 1, s.refreshTipsInterval, func() (tips iotago.BlockIDs, err error) {
		selectTipsCtx, selectTipsCancel := context.WithTimeout(ctx, s.refreshTipsInterval)
		defer selectTipsCancel()

		mergedCtx, mergedCancel := contextutils.MergeContexts(ctx, selectTipsCtx)
		defer mergedCancel()

		// refresh tips of the spammer if PoW takes longer than a configured duration.
		_, refreshedTips, _, err := s.selectSpammerTips(mergedCtx, iotago.BlockIDs{})

		return refreshedTips, err
	}); err != nil {
		return err
	}
	durationPoW := time.Since(timeStart)

	if _, err := s.sendBlockFunc(ctx, block); err != nil {
		return fmt.Errorf("send data block failed, error: %w", err)
	}

	s.spammerMetrics.SentSpamBlocks.Inc()
	s.Events.SpamPerformed.Trigger(&SpamStats{Tipselection: float32(durationGTTA.Seconds()), ProofOfWork: float32(durationPoW.Seconds())})

	return nil
}

func (s *Spammer) BuildTransactionPayloadBlockAndSend(ctx context.Context, spamBuilder *SpamBuilder) ([]UTXOInterface, *UTXO, error) {

	if len(spamBuilder.consumedInputs) < 1 {
		return nil, nil, common.ErrNoUTXOAvailable
	}

	protocolParams := s.protocolParametersFunc()

	// select tips
	isSemiLazy, tips, durationGTTA, err := s.selectSpammerTips(ctx, spamBuilder.requiredTips)
	if err != nil {
		return nil, nil, err
	}

	// create a new transaction builder for the correct network
	txBuilder := builder.NewTransactionBuilder(protocolParams.NetworkID())

	// add tagged data payload
	txBuilder.AddTaggedDataPayload(s.composeTaggedData(isSemiLazy, durationGTTA, spamBuilder.additionalTag...))

	senderAddress := spamBuilder.accountSender.Address()

	// add all inputs
	var remainder int64
	consumedInputIDs := iotago.OutputIDs{}
	for _, input := range spamBuilder.consumedInputs {
		remainder += int64(input.Output().Deposit())

		var unlockAddress iotago.Address

		//nolint:exhaustive // we do not need to check all output types
		switch input.Output().Type() {
		case iotago.OutputBasic:
			//nolint:forcetypeassert // we already checked the type
			o := input.Output().(*iotago.BasicOutput)

			addrUnlockCondition := o.UnlockConditionSet().Address()
			if addrUnlockCondition == nil {
				return nil, nil, fmt.Errorf("unlock condition is not an address")
			}

			if !addrUnlockCondition.Address.Equal(senderAddress) {
				return nil, nil, fmt.Errorf("unlock address of input does not match sender address: %s != %s", addrUnlockCondition.Address.Bech32(protocolParams.Bech32HRP), senderAddress.Bech32(protocolParams.Bech32HRP))
			}

			unlockAddress = addrUnlockCondition.Address

		case iotago.OutputAlias:
			//nolint:forcetypeassert // we already checked the type
			o := input.Output().(*iotago.AliasOutput)

			addrUnlockCondition := o.UnlockConditionSet().StateControllerAddress()
			if addrUnlockCondition == nil {
				return nil, nil, fmt.Errorf("unlock condition is not a state controller address")
			}

			if !addrUnlockCondition.Address.Equal(senderAddress) {
				return nil, nil, fmt.Errorf("unlock state controller address of input does not match sender address: %s != %s", addrUnlockCondition.Address.Bech32(protocolParams.Bech32HRP), senderAddress.Bech32(protocolParams.Bech32HRP))
			}

			unlockAddress = addrUnlockCondition.Address

		case iotago.OutputFoundry:
			//nolint:forcetypeassert // we already checked the type
			o := input.Output().(*iotago.FoundryOutput)

			addrUnlockCondition := o.UnlockConditionSet().ImmutableAlias()
			if addrUnlockCondition == nil {
				return nil, nil, fmt.Errorf("unlock condition is not an immutable alias address")
			}

			unlockAddress = addrUnlockCondition.Address

		case iotago.OutputNFT:
			//nolint:forcetypeassert // we already checked the type
			o := input.Output().(*iotago.NFTOutput)

			addrUnlockCondition := o.UnlockConditionSet().Address()
			if addrUnlockCondition == nil {
				return nil, nil, fmt.Errorf("unlock condition is not an address")
			}

			if !addrUnlockCondition.Address.Equal(senderAddress) {
				return nil, nil, fmt.Errorf("unlock address of input does not match sender address: %s != %s", addrUnlockCondition.Address.Bech32(protocolParams.Bech32HRP), senderAddress.Bech32(protocolParams.Bech32HRP))
			}

			unlockAddress = addrUnlockCondition.Address

		default:
			return nil, nil, fmt.Errorf("%w: type %d", iotago.ErrUnknownOutputType, input.Output().Type())
		}

		txBuilder.AddInput(&builder.TxInput{
			UnlockTarget: unlockAddress,
			InputID:      input.OutputID(),
			Input:        input.Output(),
		})
		consumedInputIDs = append(consumedInputIDs, input.OutputID())
	}

	// create a remainder output
	// if the balance for the remainder output is not sufficient, the remainder output is not used.
	basicOuputRemainder := &iotago.BasicOutput{
		Conditions: iotago.UnlockConditions{
			&iotago.AddressUnlockCondition{Address: senderAddress},
		},
	}

	if err := setMinimumBalanceOfOutput(protocolParams, basicOuputRemainder); err != nil {
		return nil, nil, err
	}

	// add all outputs and calculate the remainder
	var remainderOutputIndex uint16
	for i, outputWithOwnership := range spamBuilder.createdOutputs {
		output := outputWithOwnership.Output

		if err := setMinimumBalanceOfOutput(protocolParams, output); err != nil {
			return nil, nil, err
		}

		remainder -= int64(output.Deposit())

		if i == len(spamBuilder.createdOutputs)-1 {
			// last output
			// we need to check if there is a remainder and if the remainder has sufficient balance for the storage deposit
			// otherwise we add the remainder balance to the last output

			if remainder == 0 {
				// if there is no remainder, we do not add a remainder output
				basicOuputRemainder = nil
			}

			if remainder > 0 {
				if uint64(remainder) >= basicOuputRemainder.Amount {
					// remainder balance is sufficient to fund the storage deposit for the remainder output
					basicOuputRemainder.Amount = uint64(remainder)
				} else {
					// remainder balance is not sufficient to fund the storage deposit for the remainder output
					// add the remainder balance to the current output
					basicOuputRemainder = nil
					if err := addBalanceToOutput(output, uint64(remainder)); err != nil {
						return nil, nil, err
					}
				}
				remainder = 0
			}
		}

		txBuilder.AddOutput(output)
		remainderOutputIndex++
	}

	if remainder < 0 {
		return nil, nil, fmt.Errorf("%w: %d", common.ErrNotEnoughBalanceAvailable, remainder)
	}

	// in case no output was given, we need to set the remainder
	if remainder > 0 {
		basicOuputRemainder.Amount = uint64(remainder)
	}

	// add the remainder output if it is needed
	if basicOuputRemainder != nil {
		txBuilder.AddOutput(basicOuputRemainder)
	}

	// build the transaction payload
	txPayload, err := txBuilder.Build(protocolParams, spamBuilder.accountSender.Signer())
	if err != nil {
		return nil, nil, fmt.Errorf("build tx payload failed, error: %w", err)
	}

	// build a block with transaction payload
	block, err := builder.
		NewBlockBuilder().
		ProtocolVersion(protocolParams.Version).
		Parents(tips).
		Payload(txPayload).
		Build()
	if err != nil {
		return nil, nil, fmt.Errorf("build block failed, error: %w", err)
	}

	timeStart := time.Now()
	// we only use "workersCount" threads in parallel to do the PoW for transaction spam blocks, because only one spammer thread runs in parallel.
	if _, err := pow.DoPoW(ctx, block, float64(protocolParams.MinPoWScore), s.workersCountRunning, s.refreshTipsInterval, func() (tips iotago.BlockIDs, err error) {
		selectTipsCtx, selectTipsCancel := context.WithTimeout(ctx, s.refreshTipsInterval)
		defer selectTipsCancel()

		mergedCtx, mergedCancel := contextutils.MergeContexts(ctx, selectTipsCtx)
		defer mergedCancel()

		// refresh tips of the spammer if PoW takes longer than a configured duration.
		_, refreshedTips, _, err := s.selectSpammerTips(mergedCtx, spamBuilder.requiredTips)

		return refreshedTips, err
	}); err != nil {
		return nil, nil, err
	}
	durationPoW := time.Since(timeStart)

	blockID, err := s.sendBlockFunc(ctx, block)
	if err != nil {
		// there was an error during sending a transaction
		// it is high likely that something is non-solid or below max depth
		s.resetSpammerState()

		return nil, nil, fmt.Errorf("send transaction block failed, error: %w", err)
	}

	s.spammerMetrics.SentSpamBlocks.Inc()
	s.Events.SpamPerformed.Trigger(&SpamStats{Tipselection: float32(durationGTTA.Seconds()), ProofOfWork: float32(durationPoW.Seconds())})

	transactionID, err := txPayload.ID()
	if err != nil {
		return nil, nil, fmt.Errorf("computing transactionID failed, error: %w", err)
	}

	s.addPendingTransactionWithoutLocking(&pendingTransaction{
		BlockID:        blockID,
		TransactionID:  transactionID,
		ConsumedInputs: consumedInputIDs,
	})

	createdOutputs := make([]UTXOInterface, 0)

	var outputIndex uint16
	for _, outputWithOwnership := range spamBuilder.createdOutputs {
		output := outputWithOwnership.Output
		if output.Type() == iotago.OutputAlias {
			createdOutputs = append(createdOutputs, NewAliasUTXO(
				iotago.OutputIDFromTransactionIDAndIndex(transactionID, outputIndex),
				output,
				blockID,
				outputWithOwnership.OwnedOutputs,
			))
		} else {
			createdOutputs = append(createdOutputs, NewUTXO(
				iotago.OutputIDFromTransactionIDAndIndex(transactionID, outputIndex),
				output,
				blockID,
			))
		}

		outputIndex++
	}

	if basicOuputRemainder == nil {
		return createdOutputs, nil, nil
	}

	return createdOutputs, NewUTXO(
		iotago.OutputIDFromTransactionIDAndIndex(transactionID, remainderOutputIndex),
		basicOuputRemainder,
		blockID,
	), nil
}

func (s *Spammer) bookCreatedOutputs(createdOutputs []UTXOInterface, basicOutputsAccount *LedgerAccount, aliasOutputsAccount *LedgerAccount, nftOutputsAccount *LedgerAccount) error {

	sort.Slice(createdOutputs, func(i, j int) bool {
		return createdOutputs[i].Output().Type() < createdOutputs[j].Output().Type()
	})

	for _, output := range createdOutputs {

		//nolint:exhaustive // we do not need to check all output types
		switch output.Output().Type() {
		case iotago.OutputBasic:
			if basicOutputsAccount == nil {
				return fmt.Errorf("basic output account is nil")
			}

			utxo, ok := output.(*UTXO)
			if !ok {
				panic(fmt.Sprintf("invalid type: expected *UTXO, got %T", output))
			}

			basicOutputsAccount.AppendBasicOutput(utxo)

		case iotago.OutputAlias:
			if aliasOutputsAccount == nil {
				return fmt.Errorf("alias output account is nil")
			}

			aliasUTXO, ok := output.(*AliasUTXO)
			if !ok {
				panic(fmt.Sprintf("invalid type: expected *AliasUTXO, got %T", output))
			}

			aliasOutputsAccount.AppendAliasOutput(aliasUTXO)

		case iotago.OutputFoundry:
			if aliasOutputsAccount == nil {
				return fmt.Errorf("alias output account is nil")
			}

			utxo, ok := output.(*UTXO)
			if !ok {
				panic(fmt.Sprintf("invalid type: expected *UTXO, got %T", output))
			}

			if err := aliasOutputsAccount.AppendFoundryOutput(utxo); err != nil {
				return err
			}

		case iotago.OutputNFT:
			if nftOutputsAccount == nil {
				return fmt.Errorf("nft output account is nil")
			}

			utxo, ok := output.(*UTXO)
			if !ok {
				panic(fmt.Sprintf("invalid type: expected *UTXO, got %T", output))
			}

			nftOutputsAccount.AppendNFTOutput(utxo)

		default:
			return fmt.Errorf("%w: type %d", iotago.ErrUnknownOutputType, output.Output().Type())
		}
	}

	return nil
}

// cleanupOwnership removes tracking of outputs if we don't destroy them to keep the memory usage of the spammer bounded.
func (s *Spammer) cleanupOwnership() {

	if !(s.valueSpamCreateAlias && s.valueSpamCreateFoundry && s.valueSpamMintNativeToken) {
		// sender's basic outputs with native tokens are never used => cleanup
		s.accountSender.CleanupOwnershipBasicOutputs()
	}

	if !s.valueSpamCollectBasicOutput {
		// receiver's basic outputs are never used => cleanup
		s.accountReceiver.ResetBasicOutputs()
	}

	if !s.valueSpamCreateAlias {
		// sender's alias outputs are never used => cleanup
		s.accountSender.ResetAliasOutputs()
	} else {
		// there should be no foundry outputs in the sender's alias outputs anyway => cleanup
		s.accountSender.ResetFoundryOutputs()
	}

	if !(s.valueSpamCreateAlias && s.valueSpamDestroyAlias && ((!s.valueSpamCreateFoundry || s.valueSpamDestroyFoundry) && (!s.valueSpamMintNativeToken || s.valueSpamMeltNativeToken))) {
		// receiver's alias outputs are never used => cleanup
		s.accountReceiver.ResetAliasOutputs()
	}

	if !s.valueSpamCreateNFT {
		// sender's NFT outputs are never used => cleanup
		s.accountSender.ResetNFTOutputs()
	}

	if !s.valueSpamDestroyNFT {
		// receiver's NFT outputs are never used => cleanup
		s.accountReceiver.ResetNFTOutputs()
	}
}
