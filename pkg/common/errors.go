package common

import (
	"github.com/pkg/errors"
)

var (
	// ErrOperationAborted is returned when the operation was aborted e.g. by a shutdown signal.
	ErrOperationAborted = errors.New("operation was aborted")
	// ErrNoTipsAvailable is returned when no tips are available in the node.
	ErrNoTipsAvailable = errors.New("no tips available")
	// ErrNoUTXOAvailable is returned when no UTXO are available on the address.
	ErrNoUTXOAvailable = errors.New("no UTXO available")
	// ErrMaxOutputsCountExceeded is returned when the maximum count of outputs in a transaction is exceeded.
	ErrMaxOutputsCountExceeded = errors.New("max outputs count exceeded")
	// ErrNotEnoughBalanceAvailable is returned when not enough balance is on the address.
	ErrNotEnoughBalanceAvailable = errors.New("not enough balance available")
	// ErrMnemonicNotProvided is returned when no mnemonic was provided via environment variables.
	ErrMnemonicNotProvided = errors.New("value spam disabled because \"SPAMMER_MNEMONIC\" was not provided via environment variables")
)
