package spammer

import (
	"context"
	"fmt"

	"github.com/iotaledger/inx-spammer/pkg/common"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/nodeclient"
)

// collects Alias outputs from a given address.
func collectAliasOutputsQuery(addressBech32 string) nodeclient.IndexerQuery {
	return &nodeclient.AliasesQuery{
		StateControllerBech32: addressBech32,
	}
}

func (s *Spammer) aliasOutputCreate(ctx context.Context, accountSender *LedgerAccount, additionalTag ...string) error {

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

	// create the new alias output
	targetAliasOuput := &iotago.AliasOutput{
		AliasID:    iotago.AliasID{},
		StateIndex: 0,
		Conditions: iotago.UnlockConditions{
			&iotago.StateControllerAddressUnlockCondition{Address: accountSender.Address()},
			&iotago.GovernorAddressUnlockCondition{Address: accountSender.Address()},
		},
		ImmutableFeatures: iotago.Features{
			&iotago.IssuerFeature{Address: accountSender.Address()},
		},
	}
	if !spamBuilder.AddOutput(targetAliasOuput) {
		return fmt.Errorf("%w: alias outputs", common.ErrMaxOutputsCountExceeded)
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
	if err := s.bookCreatedOutputs(createdOutputs, nil, accountSender, nil); err != nil {
		panic(err)
	}

	return nil
}

func (s *Spammer) aliasOutputStateTransition(ctx context.Context, accountSender *LedgerAccount, additionalTag ...string) error {

	if len(accountSender.AliasOutputs()) < 1 {
		return fmt.Errorf("%w: alias outputs", common.ErrNoUTXOAvailable)
	}

	spamBuilder := NewSpamBuilder(accountSender, additionalTag...)

	_, remainingAliasInputs := consumeInputs(accountSender.AliasOutputs(), func(aliasInput *AliasUTXO) (consume bool, abort bool) {
		aliasOutput, ok := aliasInput.Output().(*iotago.AliasOutput)
		if !ok {
			panic(fmt.Sprintf("invalid type: expected *iotago.AliasOutput, got %T", aliasInput.Output()))
		}

		// create the new alias output
		//nolint:forcetypeassert // we already checked the type
		transitionedAliasOutput := aliasOutput.Clone().(*iotago.AliasOutput)
		transitionedAliasOutput.StateIndex++
		if transitionedAliasOutput.AliasID.Empty() {
			transitionedAliasOutput.AliasID = iotago.AliasIDFromOutputID(aliasInput.OutputID())
		}

		tmpSpamBuilder := spamBuilder.Clone()

		if !tmpSpamBuilder.AddInput(aliasInput) {
			return false, true
		}

		if !tmpSpamBuilder.AddOutputWithOwnership(transitionedAliasOutput, aliasInput.FoundryOutputs()) {
			return false, true
		}

		spamBuilder = tmpSpamBuilder

		return true, false
	})

	createdOutputs, utxoRemainder, err := s.BuildTransactionPayloadBlockAndSend(
		ctx,
		spamBuilder,
	)
	if err != nil {
		return err
	}

	if utxoRemainder != nil {
		// add the newly created basic output for the remainder to the remaining basic outputs list
		accountSender.AppendBasicOutput(utxoRemainder)
	}

	accountSender.SetAliasOutputs(remainingAliasInputs)
	if err := s.bookCreatedOutputs(createdOutputs, nil, accountSender, nil); err != nil {
		panic(err)
	}

	return nil
}

func (s *Spammer) aliasOutputGovernanceTransition(ctx context.Context, accountSender *LedgerAccount, accountReceiver *LedgerAccount, additionalTag ...string) error {

	if len(accountSender.AliasOutputs()) < 1 {
		return fmt.Errorf("%w: alias outputs", common.ErrNoUTXOAvailable)
	}

	spamBuilder := NewSpamBuilder(accountSender, additionalTag...)

	_, remainingAliasInputs := consumeInputs(accountSender.AliasOutputs(), func(aliasInput *AliasUTXO) (consume bool, abort bool) {
		aliasOutput, ok := aliasInput.Output().(*iotago.AliasOutput)
		if !ok {
			panic(fmt.Sprintf("invalid type: expected *iotago.AliasOutput, got %T", aliasInput.Output()))
		}

		// create the new alias output
		//nolint:forcetypeassert // we already checked the type
		transitionedAliasOutput := aliasOutput.Clone().(*iotago.AliasOutput)
		transitionedAliasOutput.UnlockConditionSet().GovernorAddress().Address = accountReceiver.Address()
		transitionedAliasOutput.UnlockConditionSet().StateControllerAddress().Address = accountReceiver.Address()
		if transitionedAliasOutput.AliasID.Empty() {
			transitionedAliasOutput.AliasID = iotago.AliasIDFromOutputID(aliasInput.OutputID())
		}

		tmpSpamBuilder := spamBuilder.Clone()

		if !tmpSpamBuilder.AddInput(aliasInput) {
			return false, true
		}

		if !tmpSpamBuilder.AddOutputWithOwnership(transitionedAliasOutput, aliasInput.FoundryOutputs()) {
			return false, true
		}

		spamBuilder = tmpSpamBuilder

		return true, false
	})

	createdOutputs, utxoRemainder, err := s.BuildTransactionPayloadBlockAndSend(
		ctx,
		spamBuilder,
	)
	if err != nil {
		return err
	}

	if utxoRemainder != nil {
		// add the newly created basic output for the remainder to the remaining basic outputs list
		accountSender.AppendBasicOutput(utxoRemainder)
	}

	accountSender.SetAliasOutputs(remainingAliasInputs)
	if err := s.bookCreatedOutputs(createdOutputs, nil, accountReceiver, nil); err != nil {
		panic(err)
	}

	return nil
}

func (s *Spammer) aliasOutputDestroy(ctx context.Context, accountSender *LedgerAccount, additionalTag ...string) error {

	if len(accountSender.AliasOutputs()) < 1 {
		return fmt.Errorf("%w: alias outputs", common.ErrNoUTXOAvailable)
	}

	spamBuilder := NewSpamBuilder(accountSender, additionalTag...)

	_, remainingAliasInputs := consumeInputs(accountSender.AliasOutputs(), func(aliasInput *AliasUTXO) (consume bool, abort bool) {

		if aliasInput.FoundryOutputs() != nil && len(aliasInput.FoundryOutputs()) > 0 {
			// there exists a foundry output, so we can not destroy the alias output
			return false, false
		}

		if !spamBuilder.AddInput(aliasInput) {
			return false, true
		}

		return true, false
	})

	if spamBuilder.ConsumedInputsEmpty() {
		return fmt.Errorf("%w: filtered alias outputs", common.ErrNoUTXOAvailable)
	}

	_, utxoRemainder, err := s.BuildTransactionPayloadBlockAndSend(
		ctx,
		spamBuilder,
	)
	if err != nil {
		return err
	}

	if utxoRemainder != nil {
		// add the newly created basic output for the remainder to the remaining basic outputs list
		accountSender.AppendBasicOutput(utxoRemainder)
	}

	accountSender.SetAliasOutputs(remainingAliasInputs)

	return nil
}
