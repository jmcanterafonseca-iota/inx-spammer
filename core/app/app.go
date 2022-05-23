package app

import (
	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/app/core/shutdown"
	"github.com/iotaledger/hive.go/app/plugins/profiling"

	"github.com/gohornet/inx-app/inx"
	"github.com/gohornet/inx-spammer/core/spammer"
	"github.com/gohornet/inx-spammer/plugins/prometheus"
)

var (
	// Name of the app.
	Name = "inx-spammer"

	// Version of the app.
	Version = "0.1.0"
)

func App() *app.App {
	return app.New(Name, Version,
		app.WithInitComponent(InitComponent),
		app.WithCoreComponents([]*app.CoreComponent{
			inx.CoreComponent,
			spammer.CoreComponent,
			shutdown.CoreComponent,
		}...),
		app.WithPlugins([]*app.Plugin{
			profiling.Plugin,
			prometheus.Plugin,
		}...),
	)
}

var (
	InitComponent *app.InitComponent
)

func init() {
	InitComponent = &app.InitComponent{
		Component: &app.Component{
			Name: "App",
		},
		NonHiddenFlags: []string{
			"config",
			"help",
			"version",
		},
	}
}