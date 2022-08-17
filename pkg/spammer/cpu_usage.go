package spammer

import (
	"context"
	"math/rand"
	"runtime"
	"time"

	"github.com/pkg/errors"
	"github.com/shirou/gopsutil/cpu"

	"github.com/iotaledger/hive.go/core/syncutils"
	"github.com/iotaledger/inx-spammer/pkg/common"
)

var (
	// ErrCPUPercentageUnknown is returned if the CPU usage couldn't be determined.
	ErrCPUPercentageUnknown = errors.New("CPU percentage unknown")
)

// CPUUsageUpdater measures the CPU usage.
type CPUUsageUpdater struct {
	syncutils.RWMutex

	sampleTime time.Duration
	sleepTime  time.Duration

	usage float64
	err   error
}

// NewCPUUsageUpdater creates a new CPUUsageUpdater to measure the CPU usage.
func NewCPUUsageUpdater(sampleTime time.Duration, sleepTime time.Duration) *CPUUsageUpdater {
	return &CPUUsageUpdater{
		sampleTime: sampleTime,
		sleepTime:  sleepTime,
		usage:      0.0,
		err:        nil,
	}
}

// Run measures cpu usage until the context is canceled.
func (c *CPUUsageUpdater) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			// context was stopped
			return
		}

		cpuUsagePSutil, err := cpu.Percent(c.sampleTime, false)
		c.Lock()
		if err != nil {
			c.err = ErrCPUPercentageUnknown
			c.Unlock()

			return
		}
		c.err = nil
		c.usage = cpuUsagePSutil[0] / 100.0
		c.Unlock()
	}
}

// CPUUsage returns latest cpu usage.
func (c *CPUUsageUpdater) CPUUsage() (float64, error) {
	c.RLock()
	defer c.RUnlock()

	return c.usage, c.err
}

// CPUUsageGuessWithAdditionalWorker returns guessed cpu usage with another core running at 100% load.
func (c *CPUUsageUpdater) CPUUsageGuessWithAdditionalWorker() (float64, error) {
	cpuUsage, err := c.CPUUsage()
	if err != nil {
		return 0.0, err
	}

	return cpuUsage + (1.0 / float64(runtime.NumCPU())), nil
}

// WaitForLowerCPUUsage waits until the cpu usage drops below cpuMaxUsage.
func (c *CPUUsageUpdater) WaitForLowerCPUUsage(ctx context.Context, cpuMaxUsage float64) error {
	if cpuMaxUsage == 0.0 {
		return nil
	}

	for {
		cpuUsage, err := c.CPUUsageGuessWithAdditionalWorker()
		if err != nil {
			return err
		}

		if cpuUsage < cpuMaxUsage {
			break
		}

		select {
		case <-ctx.Done():
			return common.ErrOperationAborted

		//nolint:gosec // we do not care about weak random numbers here
		case <-time.After(time.Duration(int(c.sleepTime) + rand.Intn(int(c.sleepTime)))):
			// sleep a random time between sleepTime and 2*sleepTime
		}
	}

	return nil
}
