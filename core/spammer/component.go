package spammer

import (
	"context"
	"net/http"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/dig"

	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/timeutil"
	"github.com/iotaledger/inx-app/httpserver"
	"github.com/iotaledger/inx-app/nodebridge"
	"github.com/iotaledger/inx-spammer/pkg/daemon"
	"github.com/iotaledger/inx-spammer/pkg/spammer"
)

const (
	APIRoute = "spammer/v1"

	cpuUsageSampleTime = 200 * time.Millisecond
	cpuUsageSleepTime  = 200 * time.Millisecond
)

func init() {
	CoreComponent = &app.CoreComponent{
		Component: &app.Component{
			Name:      "Spammer",
			DepsFunc:  func(cDeps dependencies) { deps = cDeps },
			Params:    params,
			Provide:   provide,
			Configure: configure,
			Run:       run,
		},
	}
}

var (
	CoreComponent *app.CoreComponent

	deps dependencies
)

type dependencies struct {
	dig.In
	Spammer         *spammer.Spammer
	NodeBridge      *nodebridge.NodeBridge
	CPUUsageUpdater *spammer.CPUUsageUpdater
	TipPoolListener *nodebridge.TipPoolListener
}

func provide(c *dig.Container) error {

	if err := c.Provide(func() *spammer.CPUUsageUpdater {
		return spammer.NewCPUUsageUpdater(cpuUsageSampleTime, cpuUsageSleepTime)
	}); err != nil {
		return err
	}

	if err := c.Provide(func() *spammer.SpammerMetrics {
		return &spammer.SpammerMetrics{}
	}); err != nil {
		return err
	}

	if err := c.Provide(func(nodeBridge *nodebridge.NodeBridge) *nodebridge.TipPoolListener {
		return nodebridge.NewTipPoolListener(nodeBridge, 200*time.Millisecond)
	}); err != nil {
		return err
	}

	type spammerDeps struct {
		dig.In
		SpammerMetrics  *spammer.SpammerMetrics
		NodeBridge      *nodebridge.NodeBridge
		CPUUsageUpdater *spammer.CPUUsageUpdater
		TipPoolListener *nodebridge.TipPoolListener
	}

	if err := c.Provide(func(deps spammerDeps) *spammer.Spammer {
		CoreComponent.LogInfo("Setting up spammer...")
		return spammer.New(
			deps.NodeBridge.ProtocolParameters,
			ParamsSpammer.BPSRateLimit,
			ParamsSpammer.CPUMaxUsage,
			ParamsSpammer.Workers,
			ParamsSpammer.Message,
			ParamsSpammer.Tag,
			ParamsSpammer.TagSemiLazy,
			ParamsSpammer.NonLazyTipsThreshold,
			ParamsSpammer.SemiLazyTipsThreshold,
			deps.TipPoolListener.GetTipsPoolSizes,
			deps.NodeBridge.RequestTips,
			func() (bool, error) {
				status, err := deps.NodeBridge.NodeStatus()
				if err != nil {
					return false, err
				}
				return status.IsHealthy, nil
			},
			deps.NodeBridge.SubmitBlock,
			deps.SpammerMetrics,
			deps.CPUUsageUpdater,
			CoreComponent.Daemon(),
			CoreComponent.Logger(),
		)
	}); err != nil {
		return err
	}

	return nil
}

func configure() error {
	return nil
}

func run() error {

	// create a background worker that measures current CPU usage
	if err := CoreComponent.Daemon().BackgroundWorker("CPU Usage Updater", func(ctx context.Context) {
		deps.CPUUsageUpdater.Run(ctx)
	}, daemon.PriorityStopCPUUsageUpdater); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

	// create a background worker that listens to tips metrics
	if err := CoreComponent.Daemon().BackgroundWorker("Tips Metrics Updater", func(ctx context.Context) {
		deps.TipPoolListener.Run(ctx)
	}, daemon.PriorityStopTipsMetrics); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

	// create a background worker that "measures" the spammer averages values every second
	if err := CoreComponent.Daemon().BackgroundWorker("Spammer Metrics Updater", func(ctx context.Context) {
		timeutil.NewTicker(deps.Spammer.MeasureSpammerMetrics, 1*time.Second, ctx).WaitForGracefulShutdown()
	}, daemon.PriorityStopSpammer); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

	// automatically start the spammer on startup if the flag is set
	if ParamsSpammer.Autostart {
		_ = deps.Spammer.Start(nil, nil, nil)
	}

	// create a background worker that handles the API
	if err := CoreComponent.Daemon().BackgroundWorker("API", func(ctx context.Context) {
		CoreComponent.LogInfo("Starting API ... done")

		e := httpserver.NewEcho(CoreComponent.Logger(), nil, ParamsSpammer.DebugRequestLoggerEnabled)

		CoreComponent.LogInfo("Starting API server...")

		_ = spammer.NewSpammerServer(deps.Spammer, e.Group(""))

		go func() {
			CoreComponent.LogInfof("You can now access the API using: http://%s", ParamsSpammer.BindAddress)
			if err := e.Start(ParamsSpammer.BindAddress); err != nil && !errors.Is(err, http.ErrServerClosed) {
				CoreComponent.LogPanicf("Stopped REST-API server due to an error (%s)", err)
			}
		}()

		if err := deps.NodeBridge.RegisterAPIRoute(APIRoute, ParamsSpammer.BindAddress); err != nil {
			CoreComponent.LogPanicf("Registering INX api route failed, error: %s", err)
		}

		<-ctx.Done()
		CoreComponent.LogInfo("Stopping API ...")

		if err := deps.NodeBridge.UnregisterAPIRoute(APIRoute); err != nil {
			CoreComponent.LogWarnf("Unregistering INX api route failed, error: %s", err)
		}

		shutdownCtx, shutdownCtxCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := e.Shutdown(shutdownCtx); err != nil {
			CoreComponent.LogWarn(err)
		}
		shutdownCtxCancel()
		CoreComponent.LogInfo("Stopping API ... done")
	}, daemon.PriorityStopSpammerAPI); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

	return nil
}
