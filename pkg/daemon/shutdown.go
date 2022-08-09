package daemon

const (
	PriorityDisconnectINX = iota // no dependencies
	PriorityStopSpammerLedgerUpdates
	PriorityStopSpammer
	PriorityStopTipsMetrics
	PriorityStopCPUUsageUpdater
	PriorityStopSpammerAPI
	PriorityStopPrometheus
)
