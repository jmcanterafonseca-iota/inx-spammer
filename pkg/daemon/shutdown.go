package daemon

const (
	PriorityDisconnectINX = iota // no dependencies
	PriorityStopSpammer
	PriorityStopTipsMetrics
	PriorityStopCPUUsageUpdater
	PriorityStopSpammerAPI
	PriorityStopPrometheus
)
