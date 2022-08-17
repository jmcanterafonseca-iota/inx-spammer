package spammer

import (
	iotago "github.com/iotaledger/iota.go/v3"
)

type UTXOInterface interface {
	OutputID() iotago.OutputID
	Output() iotago.Output
	PendingBlockID() iotago.BlockID
}

// unspent outputs.
type UTXO struct {
	outputID iotago.OutputID
	output   iotago.Output
	// if the UTXO is not confirmed yet, we need to reference the block that contains it
	// if we want to reuse it
	pendingBlockID iotago.BlockID
}

func NewUTXO(outputID iotago.OutputID, output iotago.Output, pendingBlockID iotago.BlockID) *UTXO {
	return &UTXO{
		outputID:       outputID,
		output:         output,
		pendingBlockID: pendingBlockID,
	}
}

func (u *UTXO) OutputID() iotago.OutputID {
	return u.outputID
}

func (u *UTXO) Output() iotago.Output {
	return u.output
}

func (u *UTXO) PendingBlockID() iotago.BlockID {
	return u.pendingBlockID
}

type AliasUTXO struct {
	outputID iotago.OutputID
	output   iotago.Output
	// if the UTXO is not confirmed yet, we need to reference the block that contains it
	// if we want to reuse it
	pendingBlockID iotago.BlockID
	foundryOutputs []*UTXO
}

func NewAliasUTXO(outputID iotago.OutputID, output iotago.Output, pendingBlockID iotago.BlockID, foundryOutputs []*UTXO) *AliasUTXO {
	return &AliasUTXO{
		outputID:       outputID,
		output:         output,
		pendingBlockID: pendingBlockID,
		foundryOutputs: foundryOutputs,
	}
}

func (u *AliasUTXO) OutputID() iotago.OutputID {
	return u.outputID
}

func (u *AliasUTXO) Output() iotago.Output {
	return u.output
}

func (u *AliasUTXO) PendingBlockID() iotago.BlockID {
	return u.pendingBlockID
}

func (u *AliasUTXO) FoundryOutputs() []*UTXO {
	return u.foundryOutputs
}

func (u *AliasUTXO) SetFoundryOutputs(foundryOutputs []*UTXO) {
	u.foundryOutputs = foundryOutputs
}

func (u *AliasUTXO) AppendFoundryOutput(foundryOutput *UTXO) {
	u.foundryOutputs = append(u.foundryOutputs, foundryOutput)
}

func consumeInputs[T UTXOInterface](inputs []T, onConsumeInput func(input T) (consume bool, abort bool)) ([]T, []T) {
	consumedInputs := []T{}
	remainingInputs := []T{}

	aborted := false
	for _, utxo := range inputs {
		if aborted {
			remainingInputs = append(remainingInputs, utxo)

			continue
		}

		consume, abort := onConsumeInput(utxo)
		if abort {
			aborted = true
			remainingInputs = append(remainingInputs, utxo)

			continue
		}

		if consume {
			consumedInputs = append(consumedInputs, utxo)
		} else {
			remainingInputs = append(remainingInputs, utxo)
		}
	}

	return consumedInputs, remainingInputs
}

// type guards.
var _ UTXOInterface = &UTXO{}
var _ UTXOInterface = &AliasUTXO{}
