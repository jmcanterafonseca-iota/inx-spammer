package spammer

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/dig"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/iotaledger/hive.go/core/app"
	"github.com/iotaledger/hive.go/core/app/pkg/shutdown"
	"github.com/iotaledger/hive.go/core/timeutil"
	"github.com/iotaledger/inx-app/pkg/httpserver"
	"github.com/iotaledger/inx-app/pkg/nodebridge"
	"github.com/iotaledger/inx-spammer/pkg/daemon"
	"github.com/iotaledger/inx-spammer/pkg/hdwallet"
	"github.com/iotaledger/inx-spammer/pkg/spammer"
	inx "github.com/iotaledger/inx/go"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/nodeclient"
)

const (
	APIRoute = "spammer/v1"

	cpuUsageSampleTime            = 200 * time.Millisecond
	cpuUsageSleepTime             = 200 * time.Millisecond
	indexerPluginAvailableTimeout = 30 * time.Second
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
	ShutdownHandler *shutdown.ShutdownHandler
}

func provide(c *dig.Container) error {

	if err := c.Provide(func() *spammer.CPUUsageUpdater {
		return spammer.NewCPUUsageUpdater(cpuUsageSampleTime, cpuUsageSleepTime)
	}); err != nil {
		return err
	}

	if err := c.Provide(func() *spammer.Metrics {
		return &spammer.Metrics{}
	}); err != nil {
		return err
	}

	if err := c.Provide(func(nodeBridge *nodebridge.NodeBridge) *nodebridge.TipPoolListener {
		return nodebridge.NewTipPoolListener(nodeBridge, 200*time.Millisecond)
	}); err != nil {
		return err
	}

	fetchMetadata := func(blockID iotago.BlockID) (*spammer.Metadata, error) {
		ctx, cancel := context.WithTimeout(CoreComponent.Daemon().ContextStopped(), 5*time.Second)
		defer cancel()

		metadata, err := deps.NodeBridge.BlockMetadata(ctx, blockID)
		if err != nil {
			st, ok := status.FromError(err)
			if ok && st.Code() == codes.NotFound {
				//nolint:nilnil // nil, nil is ok in this context, even if it is not go idiomatic
				return nil, nil
			}

			return nil, err
		}

		return &spammer.Metadata{
			IsReferenced: metadata.GetReferencedByMilestoneIndex() != 0,
			//nolint:nosnakecase // grpc uses underscores
			IsConflicting:  metadata.GetConflictReason() != inx.BlockMetadata_CONFLICT_REASON_NONE,
			ShouldReattach: metadata.GetShouldReattach(),
		}, nil
	}

	type spammerDeps struct {
		dig.In
		SpammerMetrics  *spammer.Metrics
		NodeBridge      *nodebridge.NodeBridge
		CPUUsageUpdater *spammer.CPUUsageUpdater
		TipPoolListener *nodebridge.TipPoolListener
	}

	if err := c.Provide(func(deps spammerDeps) (*spammer.Spammer, error) {
		CoreComponent.LogInfo("Setting up spammer ...")

		mnemonic, err := loadMnemonicFromEnvironment("SPAMMER_MNEMONIC")
		if err != nil {
			if ParamsSpammer.ValueSpam.Enabled {
				CoreComponent.LogErrorfAndExit("value spam enabled but loading mnemonic seed failed, err: %s", err)
			}
		}

		var wallet *hdwallet.HDWallet
		var indexer nodeclient.IndexerClient
		if len(mnemonic) > 0 {
			// new HDWallet instance for address derivation
			wallet, err = hdwallet.NewHDWallet(deps.NodeBridge.ProtocolParameters(), mnemonic, "", 0, false)
			if err != nil {
				return nil, err
			}

			ctxIndexer, cancelIndexer := context.WithTimeout(CoreComponent.Daemon().ContextStopped(), indexerPluginAvailableTimeout)
			defer cancelIndexer()

			indexer, err = deps.NodeBridge.Indexer(ctxIndexer)
			if err != nil {
				return nil, err
			}
		}

		return spammer.New(
			deps.NodeBridge.ProtocolParameters,
			indexer,
			wallet,
			ParamsSpammer.BPSRateLimit,
			ParamsSpammer.CPUMaxUsage,
			ParamsSpammer.Workers,
			ParamsSpammer.Message,
			ParamsSpammer.Tag,
			ParamsSpammer.TagSemiLazy,
			ParamsSpammer.ValueSpam.Enabled,
			ParamsSpammer.ValueSpam.SendBasicOutput,
			ParamsSpammer.ValueSpam.CollectBasicOutput,
			ParamsSpammer.ValueSpam.CreateAlias,
			ParamsSpammer.ValueSpam.DestroyAlias,
			ParamsSpammer.ValueSpam.CreateFoundry,
			ParamsSpammer.ValueSpam.DestroyFoundry,
			ParamsSpammer.ValueSpam.MintNativeToken,
			ParamsSpammer.ValueSpam.MeltNativeToken,
			ParamsSpammer.ValueSpam.CreateNFT,
			ParamsSpammer.ValueSpam.DestroyNFT,
			ParamsSpammer.Tipselection.NonLazyTipsThreshold,
			ParamsSpammer.Tipselection.SemiLazyTipsThreshold,
			ParamsPoW.RefreshTipsInterval,
			deps.TipPoolListener.GetTipsPoolSizes,
			deps.NodeBridge.RequestTips,
			deps.NodeBridge.IsNodeHealthy,
			deps.NodeBridge.SubmitBlock,
			fetchMetadata,
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

	// create a background worker that handles the ledger updates
	if err := CoreComponent.Daemon().BackgroundWorker("Spammer[LedgerUpdates]", func(ctx context.Context) {
		if err := deps.NodeBridge.ListenToLedgerUpdates(ctx, 0, 0, func(update *nodebridge.LedgerUpdate) error {
			createdOutputs := iotago.OutputIDs{}
			for _, output := range update.Created {
				createdOutputs = append(createdOutputs, output.GetOutputId().Unwrap())
			}
			consumedOutputs := iotago.OutputIDs{}
			for _, spent := range update.Consumed {
				consumedOutputs = append(consumedOutputs, spent.GetOutput().GetOutputId().Unwrap())
			}

			err := deps.Spammer.ApplyNewLedgerUpdate(ctx, update.MilestoneIndex, createdOutputs, consumedOutputs)
			if err != nil {
				deps.ShutdownHandler.SelfShutdown(fmt.Sprintf("Spammer plugin hit a critical error while applying new ledger update: %s", err.Error()), true)
			}

			return err
		}); err != nil {
			deps.ShutdownHandler.SelfShutdown(fmt.Sprintf("Listening to LedgerUpdates failed, error: %s", err), true)
		}
	}, daemon.PriorityStopSpammerLedgerUpdates); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

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
		if err := deps.Spammer.Start(nil, nil, nil, nil); err != nil {
			CoreComponent.LogPanicf("failed to autostart spammer: %s", err)
		}
	}

	// create a background worker that handles the API
	if err := CoreComponent.Daemon().BackgroundWorker("API", func(ctx context.Context) {
		CoreComponent.LogInfo("Starting API ... done")

		e := httpserver.NewEcho(CoreComponent.Logger(), nil, ParamsRestAPI.DebugRequestLoggerEnabled)

		CoreComponent.LogInfo("Starting API server ...")

		//nolint:contextcheck // false positive
		_ = spammer.NewServer(deps.Spammer, e.Group(""))

		go func() {
			CoreComponent.LogInfof("You can now access the API using: http://%s", ParamsRestAPI.BindAddress)
			if err := e.Start(ParamsRestAPI.BindAddress); err != nil && !errors.Is(err, http.ErrServerClosed) {
				CoreComponent.LogErrorfAndExit("Stopped REST-API server due to an error (%s)", err)
			}
		}()

		ctxRegister, cancelRegister := context.WithTimeout(ctx, 5*time.Second)

		advertisedAddress := ParamsRestAPI.BindAddress
		if ParamsRestAPI.AdvertiseAddress != "" {
			advertisedAddress = ParamsRestAPI.AdvertiseAddress
		}

		if err := deps.NodeBridge.RegisterAPIRoute(ctxRegister, APIRoute, advertisedAddress); err != nil {
			CoreComponent.LogErrorfAndExit("Registering INX api route failed: %s", err)
		}
		cancelRegister()

		CoreComponent.LogInfo("Starting API server ... done")
		<-ctx.Done()
		CoreComponent.LogInfo("Stopping API ...")

		ctxUnregister, cancelUnregister := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelUnregister()

		//nolint:contextcheck // false positive
		if err := deps.NodeBridge.UnregisterAPIRoute(ctxUnregister, APIRoute); err != nil {
			CoreComponent.LogWarnf("Unregistering INX api route failed: %s", err)
		}

		shutdownCtx, shutdownCtxCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCtxCancel()

		//nolint:contextcheck // false positive
		if err := e.Shutdown(shutdownCtx); err != nil {
			CoreComponent.LogWarn(err)
		}

		CoreComponent.LogInfo("Stopping API ... done")
	}, daemon.PriorityStopSpammerAPI); err != nil {
		CoreComponent.LogPanicf("failed to start worker: %s", err)
	}

	return nil
}

// loads Mnemonic phrases from the given environment variable.
func loadMnemonicFromEnvironment(name string) ([]string, error) {
	keys, exists := os.LookupEnv(name)
	if !exists {
		return nil, fmt.Errorf("environment variable '%s' not set", name)
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("environment variable '%s' not set", name)
	}

	phrases := strings.Split(keys, " ")

	return phrases, nil
}
