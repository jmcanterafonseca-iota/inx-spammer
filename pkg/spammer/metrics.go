package spammer

import (
	"go.uber.org/atomic"
)

// SpammerMetrics defines metrics for the spammer.
type SpammerMetrics struct {
	// The number of sent spam blocks.
	SentSpamBlocks atomic.Uint32
}
