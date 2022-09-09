package spammer

import (
	"time"

	"github.com/iotaledger/hive.go/core/app"
)

// ParametersSpammer contains the definition of the parameters used by the Spammer.
type ParametersSpammer struct {
	// whether to automatically start the spammer on startup
	Autostart bool `default:"false" usage:"automatically start the spammer on startup"`

	// the blocks per second rate limit for the spammer (0 = no limit)
	BPSRateLimit float64 `name:"bpsRateLimit" default:"0.0" usage:"the blocks per second rate limit for the spammer (0 = no limit)"`
	// workers remains idle for a while when cpu usage gets over this limit (0 = disable)
	CPUMaxUsage float64 `name:"cpuMaxUsage" default:"0.80" usage:"workers remains idle for a while when cpu usage gets over this limit (0 = disable)"`
	// the amount of parallel running spammers
	Workers int `default:"0" usage:"the amount of parallel running spammers"`

	// the message to embed within the spam blocks
	Message string `default:"We are all made of stardust." usage:"the message to embed within the spam blocks"`
	// the tag of the block
	Tag string `default:"HORNET Spammer" usage:"the tag of the block"`
	// the tag of the block if the semi-lazy pool is used (uses "tag" if empty)
	TagSemiLazy string `default:"HORNET Spammer Semi-Lazy" usage:"the tag of the block if the semi-lazy pool is used (uses \"tag\" if empty)"`

	ValueSpam struct {
		// whether to spam with transaction payloads instead of data payloads
		Enabled            bool `default:"false" usage:"whether to spam with transaction payloads instead of data payloads"`
		SendBasicOutput    bool `default:"true" usage:"whether to send basic outputs"`
		CollectBasicOutput bool `default:"true" usage:"whether to collect basic outputs"`
		CreateAlias        bool `default:"true" usage:"whether to create aliases"`
		DestroyAlias       bool `default:"true" usage:"whether to destroy aliases"`
		CreateFoundry      bool `default:"true" usage:"whether to create foundries"`
		DestroyFoundry     bool `default:"true" usage:"whether to destroy foundries"`
		MintNativeToken    bool `default:"true" usage:"whether to mint native tokens"`
		MeltNativeToken    bool `default:"true" usage:"whether to melt native tokens"`
		CreateNFT          bool `default:"true" usage:"whether to create NFTs"`
		DestroyNFT         bool `default:"true" usage:"whether to destroy NFTs"`
	}

	Tipselection struct {
		// NonLazyTipsThreshold is the maximum amount of tips in the non-lazy tip-pool before the spammer tries to reduce these (0 = always).
		// This is used to support the network if someone attacks the tangle by spamming a lot of tips.
		NonLazyTipsThreshold uint32 `default:"0" usage:"the maximum amount of tips in the non-lazy tip-pool before the spammer tries to reduce these (0 = always)"`
		// SemiLazyTipsThreshold is the maximum amount of tips in the semi-lazy tip-pool before the spammer tries to reduce these (0 = disable).
		// This is used to support the network if someone attacks the tangle by spamming a lot of tips.
		SemiLazyTipsThreshold uint32 `default:"30" usage:"the maximum amount of tips in the semi-lazy tip-pool before the spammer tries to reduce these (0 = disable)"`
	}
}

// ParametersRestAPI contains the definition of the parameters used by the Spammer HTTP server.
type ParametersRestAPI struct {
	// BindAddress defines the bind address on which the Spammer HTTP server listens.
	BindAddress string `default:"localhost:9092" usage:"the bind address on which the Spammer HTTP server listens"`
	// AdvertiseAddress defines the address of the Spammer HTTP server which is advertised to the INX Server (optional).
	AdvertiseAddress string `default:"" usage:"the address of the Spammer HTTP server which is advertised to the INX Server (optional)"`
	// DebugRequestLoggerEnabled defines whether the debug logging for requests should be enabled
	DebugRequestLoggerEnabled bool `default:"false" usage:"whether the debug logging for requests should be enabled"`
}

// ParametersPoW contains the definition of the parameters used by PoW.
type ParametersPoW struct {
	// Defines the interval for refreshing tips during PoW.
	RefreshTipsInterval time.Duration `default:"5s" usage:"interval for refreshing tips during PoW"`
}

var ParamsSpammer = &ParametersSpammer{}
var ParamsRestAPI = &ParametersRestAPI{}
var ParamsPoW = &ParametersPoW{}

var params = &app.ComponentParams{
	Params: map[string]any{
		"spammer": ParamsSpammer,
		"restAPI": ParamsRestAPI,
		"pow":     ParamsPoW,
	},
	Masked: nil,
}
