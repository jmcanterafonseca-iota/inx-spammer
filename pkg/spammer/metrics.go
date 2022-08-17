package spammer

import (
	"go.uber.org/atomic"
)

// Metrics defines metrics for the spammer.
type Metrics struct {
	// The number of sent spam blocks.
	SentSpamBlocks atomic.Uint32
}
