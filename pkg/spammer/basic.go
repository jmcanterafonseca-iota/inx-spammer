package spammer

import (
	"context"
	"fmt"

	"github.com/iotaledger/inx-spammer/pkg/common"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/nodeclient"
)

// collects Basic outputs from a given address.
func collectBasicOutputsQuery(addressBech32 string, allowNativeTokens bool) nodeclient.IndexerQuery {
	falseCondition := false

	basicOutputsQuery := &nodeclient.BasicOutputsQuery{
		AddressBech32: addressBech32,
		IndexerExpirationParas: nodeclient.IndexerExpirationParas{
			HasExpiration: &falseCondition,
		},
		IndexerTimelockParas: nodeclient.IndexerTimelockParas{
			HasTimelock: &falseCondition,
		},
		IndexerStorageDepositParas: nodeclient.IndexerStorageDepositParas{
			HasStorageDepositReturn: &falseCondition,
		},
	}

	if !allowNativeTokens {
		basicOutputsQuery.IndexerNativeTokenParas = nodeclient.IndexerNativeTokenParas{
			HasNativeTokens: &falseCondition,
		}
	}

	return basicOutputsQuery
}

func (s *Spammer) basicOutputSend(ctx context.Context, accountSender *LedgerAccount, accountReceiver *LedgerAccount, additionalTag ...string) error {

	if len(accountSender.BasicOutputs()) < 1 {
		return fmt.Errorf("%w: basic outputs", common.ErrNoUTXOAvailable)
	}

	spamBuilder := NewSpamBuilder(accountSender, additionalTag...)

	_, remainingBasicInputs := consumeInputs(accountSender.BasicOutputs(), func(basicInput *UTXO) (consume bool, abort bool) {
		basicOutput, ok := basicInput.Output().(*iotago.BasicOutput)
		if !ok {
			panic(fmt.Sprintf("invalid type: expected *iotago.BasicOutput, got %T", basicInput.Output()))
		}

		nativeTokens := basicOutput.NativeTokenList().MustSet()
		if len(nativeTokens) != 0 {
			// output contains native tokens, do not consume the basic output
			return false, false
		}

		if !spamBuilder.AddInput(basicInput) {
			return false, true
		}

		return true, false
	})

	if spamBuilder.ConsumedInputsEmpty() {
		return fmt.Errorf("%w: filtered basic outputs", common.ErrNoUTXOAvailable)
	}

	// create the new basic output
	targetBasicOuput := &iotago.BasicOutput{
		Conditions: iotago.UnlockConditions{
			&iotago.AddressUnlockCondition{Address: accountReceiver.Address()},
		},
	}
	if !spamBuilder.AddOutput(targetBasicOuput) {
		return fmt.Errorf("%w: basic output", common.ErrMaxOutputsCountExceeded)
	}

	createdOutputs, utxoRemainder, err := s.BuildTransactionPayloadBlockAndSend(
		ctx,
		spamBuilder,
	)
	if err != nil {
		return err
	}

	if utxoRemainder != nil {
		// add the newly created basic output for the remainder to the remaining basic outputs list
		remainingBasicInputs = append(remainingBasicInputs, utxoRemainder)
	}

	accountSender.SetBasicOutputs(remainingBasicInputs)
	if err := s.bookCreatedOutputs(createdOutputs, accountReceiver, nil, nil); err != nil {
		panic(err)
	}

	return nil
}

func (s *Spammer) basicOutputSendNativeTokens(ctx context.Context, accountSender *LedgerAccount, accountReceiver *LedgerAccount, additionalTag ...string) error {

	if len(accountSender.BasicOutputs()) < 1 {
		return fmt.Errorf("%w: basic outputs", common.ErrNoUTXOAvailable)
	}

	spamBuilder := NewSpamBuilder(accountSender, additionalTag...)

	_, remainingBasicInputs := consumeInputs(accountSender.BasicOutputs(), func(basicInput *UTXO) (consume bool, abort bool) {
		basicOutput, ok := basicInput.Output().(*iotago.BasicOutput)
		if !ok {
			panic(fmt.Sprintf("invalid type: expected *iotago.BasicOutput, got %T", basicInput.Output()))
		}

		nativeTokens := basicOutput.NativeTokenList().MustSet()
		if len(nativeTokens) == 0 {
			// output doesn't contain any native tokens, do not consume the basic output
			return false, false
		}

		// send the native tokens to a new basic output
		//nolint:forcetypeassert // we already checked the type
		createdBasicOutput := basicOutput.Clone().(*iotago.BasicOutput)
		createdBasicOutput.UnlockConditionSet().Address().Address = accountReceiver.Address()

		tmpSpamBuilder := spamBuilder.Clone()

		if !tmpSpamBuilder.AddInput(basicInput) {
			return false, true
		}

		if !tmpSpamBuilder.AddOutput(createdBasicOutput) {
			return false, true
		}

		spamBuilder = tmpSpamBuilder

		return true, false
	})

	if spamBuilder.ConsumedInputsEmpty() {
		return fmt.Errorf("%w: filtered basic outputs", common.ErrNoUTXOAvailable)
	}

	createdOutputs, utxoRemainder, err := s.BuildTransactionPayloadBlockAndSend(
		ctx,
		spamBuilder,
	)
	if err != nil {
		return err
	}

	if utxoRemainder != nil {
		// add the newly created basic output for the remainder to the remaining basic outputs list
		remainingBasicInputs = append(remainingBasicInputs, utxoRemainder)
	}

	accountSender.SetBasicOutputs(remainingBasicInputs)
	if err := s.bookCreatedOutputs(createdOutputs, accountReceiver, nil, nil); err != nil {
		panic(err)
	}

	return nil
}
