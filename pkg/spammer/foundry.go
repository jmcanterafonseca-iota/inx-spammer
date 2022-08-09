package spammer

import (
	"context"
	"fmt"
	"math/big"

	"github.com/iotaledger/hive.go/serializer/v2"
	"github.com/iotaledger/inx-spammer/pkg/common"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/nodeclient"
)

// collects Foundry outputs from a given alias
func collectFoundryOutputsQuery(addressBech32 string) nodeclient.IndexerQuery {
	return &nodeclient.FoundriesQuery{
		AliasAddressBech32: addressBech32,
	}
}

func (s *Spammer) foundryOutputCreate(ctx context.Context, accountSender *LedgerAccount, additionalTag ...string) error {

	if len(accountSender.BasicOutputs()) < 1 {
		return fmt.Errorf("%w: basic outputs", common.ErrNoUTXOAvailable)
	}

	if len(accountSender.AliasOutputs()) < 1 {
		return fmt.Errorf("%w: alias outputs", common.ErrNoUTXOAvailable)
	}

	spamBuilder := NewSpamBuilder(accountSender, additionalTag...)

	_, remainingBasicInputs := consumeInputs(accountSender.BasicOutputs(), func(basicInput *UTXO) (consume bool, abort bool) {
		basicOutput := basicInput.Output().(*iotago.BasicOutput)

		nativeTokens := basicOutput.NativeTokenList().MustSet()
		if len(nativeTokens) != 0 {
			// output contains native tokens, do not consume the basic output
			return false, false
		}

		if !spamBuilder.AddInput(
			basicInput,
			WithInputMaxTipsCount(iotago.BlockMaxParents/2),
			WithInputMaxInputsCount(iotago.MaxInputsCount/2),
		) {
			return false, true
		}

		return true, false
	})

	if spamBuilder.ConsumedInputsEmpty() {
		return fmt.Errorf("%w: filtered basic outputs", common.ErrNoUTXOAvailable)
	}

	_, remainingAliasInputs := consumeInputs(accountSender.AliasOutputs(), func(aliasInput *AliasUTXO) (consume bool, abort bool) {
		aliasOutput := aliasInput.Output().(*iotago.AliasOutput)

		// create the new alias output
		transitionedAliasOutput := aliasOutput.Clone().(*iotago.AliasOutput)
		transitionedAliasOutput.StateIndex++
		transitionedAliasOutput.FoundryCounter++
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

		// create the new foundry output
		newFoundryOutput := &iotago.FoundryOutput{
			Amount:       0,
			NativeTokens: nil,
			SerialNumber: transitionedAliasOutput.FoundryCounter,
			TokenScheme: &iotago.SimpleTokenScheme{
				MintedTokens:  big.NewInt(0),
				MeltedTokens:  big.NewInt(0),
				MaximumSupply: big.NewInt(1000000000),
			},
			Conditions: iotago.UnlockConditions{
				&iotago.ImmutableAliasUnlockCondition{Address: transitionedAliasOutput.AliasID.ToAddress().(*iotago.AliasAddress)},
			},
			Features: nil,
			ImmutableFeatures: iotago.Features{
				&iotago.MetadataFeature{
					Data: []byte("TestCoin"),
				},
			},
		}

		if !tmpSpamBuilder.AddOutput(newFoundryOutput) {
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
		remainingBasicInputs = append(remainingBasicInputs, utxoRemainder)
	}

	accountSender.SetBasicOutputs(remainingBasicInputs)
	accountSender.SetAliasOutputs(remainingAliasInputs)
	if err := s.bookCreatedOutputs(createdOutputs, nil, accountSender, nil); err != nil {
		panic(err)
	}

	return nil
}

func (s *Spammer) foundryOutputMintNativeTokens(ctx context.Context, accountSender *LedgerAccount, additionalTag ...string) error {

	if len(accountSender.BasicOutputs()) < 1 {
		return fmt.Errorf("%w: basic outputs", common.ErrNoUTXOAvailable)
	}

	if len(accountSender.AliasOutputs()) < 1 {
		return fmt.Errorf("%w: alias outputs", common.ErrNoUTXOAvailable)
	}

	spamBuilder := NewSpamBuilder(accountSender, additionalTag...)

	_, remainingBasicInputs := consumeInputs(accountSender.BasicOutputs(), func(basicInput *UTXO) (consume bool, abort bool) {
		basicOutput := basicInput.Output().(*iotago.BasicOutput)

		nativeTokens := basicOutput.NativeTokenList().MustSet()
		if len(nativeTokens) != 0 {
			// output contains native tokens, do not consume the basic output
			return false, false
		}

		// we limit the number of basic outputs to leave space for alias outputs and foundry outputs
		if !spamBuilder.AddInput(
			basicInput,
			WithInputMaxTipsCount(iotago.BlockMaxParents/4),
			WithInputMaxInputsCount(iotago.MaxInputsCount/2),
		) {
			return false, true
		}

		return true, false
	})

	if spamBuilder.ConsumedInputsEmpty() {
		return fmt.Errorf("%w: filtered basic outputs", common.ErrNoUTXOAvailable)
	}

	aliasConsumed := false
	_, remainingAliasInputs := consumeInputs(accountSender.AliasOutputs(), func(aliasInput *AliasUTXO) (consume bool, abort bool) {
		aliasOutput := aliasInput.Output().(*iotago.AliasOutput)

		// create the new alias output
		transitionedAliasOutput := aliasOutput.Clone().(*iotago.AliasOutput)
		if transitionedAliasOutput.AliasID.Empty() {
			transitionedAliasOutput.AliasID = iotago.AliasIDFromOutputID(aliasInput.OutputID())
		}

		spamBuilderTmp := spamBuilder.Clone()

		if !spamBuilderTmp.AddInput(aliasInput) {
			return false, true
		}

		foundryConsumed := false
		_, remainingFoundryInputs := consumeInputs(aliasInput.FoundryOutputs(), func(foundryInput *UTXO) (consume bool, abort bool) {
			foundryOutput := foundryInput.Output().(*iotago.FoundryOutput)

			if foundryOutput.TokenScheme.(*iotago.SimpleTokenScheme).MintedTokens.Sign() > 0 {
				// token already minted, do not consume the foundry output
				return false, false
			}

			if !spamBuilderTmp.AddInput(foundryInput) {
				return false, true
			}

			// create the new foundry output
			transitionedFoundryOutput := foundryOutput.Clone().(*iotago.FoundryOutput)

			// mint tokens in foundry
			transitionedFoundryOutput.TokenScheme.(*iotago.SimpleTokenScheme).MintedTokens = big.NewInt(100000000)

			if !spamBuilderTmp.AddOutput(transitionedFoundryOutput) {
				return false, true
			}

			// send the minted tokens to a new basic output
			createdBasicOutput := &iotago.BasicOutput{
				Amount: 0,
				NativeTokens: iotago.NativeTokens{
					&iotago.NativeToken{
						ID:     transitionedFoundryOutput.MustNativeTokenID(),
						Amount: big.NewInt(100000000),
					},
				},
				Conditions: iotago.UnlockConditions{
					&iotago.AddressUnlockCondition{Address: accountSender.Address()},
				},
				Features: nil,
			}

			if !spamBuilderTmp.AddOutput(createdBasicOutput) {
				return false, true
			}

			foundryConsumed = true

			return true, false
		})

		if !foundryConsumed {
			// no foundry output consumed, do not consume the alias output
			return false, false
		}

		if !spamBuilderTmp.AddOutputWithOwnership(transitionedAliasOutput, remainingFoundryInputs) {
			return false, true
		}

		spamBuilder = spamBuilderTmp

		aliasConsumed = true

		return true, false
	})

	if !aliasConsumed {
		return fmt.Errorf("%w: filtered alias outputs", common.ErrNoUTXOAvailable)
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
	accountSender.SetAliasOutputs(remainingAliasInputs)
	if err := s.bookCreatedOutputs(createdOutputs, accountSender, accountSender, nil); err != nil {
		panic(err)
	}

	return nil
}

func (s *Spammer) foundryOutputMeltNativeTokens(ctx context.Context, accountSender *LedgerAccount, additionalTag ...string) error {

	if len(accountSender.BasicOutputs()) < 1 {
		return fmt.Errorf("%w: basic outputs", common.ErrNoUTXOAvailable)
	}

	if len(accountSender.AliasOutputs()) < 1 {
		return fmt.Errorf("%w: alias outputs", common.ErrNoUTXOAvailable)
	}

	spamBuilder := NewSpamBuilder(accountSender, additionalTag...)

	basicOutputsAliasIDsMap := make(map[iotago.AliasID]struct{})
	basicOutputsPerNativeTokensMap := make(map[iotago.NativeTokenID][]*UTXO)

	_, _ = consumeInputs(accountSender.BasicOutputs(), func(basicInput *UTXO) (consume bool, abort bool) {
		basicOutput := basicInput.Output().(*iotago.BasicOutput)

		nativeTokens := basicOutput.NativeTokenList().MustSet()
		if len(nativeTokens) == 0 {
			// output doesn't contain any native tokens, do not consume the basic output
			return false, false
		}

		// loop over every native token in the output to collect the foundry IDs and alias IDs
		for _, nativeToken := range nativeTokens {
			foundryID := nativeToken.ID

			aliasID, err := AliasIDFromFoundryID(foundryID)
			if err != nil {
				return false, false
			}
			basicOutputsAliasIDsMap[aliasID] = struct{}{}

			// collect the basic inputs mapped by their native token ID (for faster lookup)
			if _, exists := basicOutputsPerNativeTokensMap[nativeToken.ID]; !exists {
				basicOutputsPerNativeTokensMap[nativeToken.ID] = make([]*UTXO, 0)
			}
			basicOutputsPerNativeTokensMap[nativeToken.ID] = append(basicOutputsPerNativeTokensMap[nativeToken.ID], basicInput)
		}

		return true, false
	})

	_, remainingAliasInputs := consumeInputs(accountSender.AliasOutputs(), func(aliasInput *AliasUTXO) (consume bool, abort bool) {
		aliasOutput := aliasInput.Output().(*iotago.AliasOutput)

		if _, exists := basicOutputsAliasIDsMap[aliasOutput.AliasID]; !exists {
			// there is no native token that belongs to this alias, do not consume the alias output
			return false, false
		}

		spamBuilderTmpAlias := spamBuilder.Clone()

		// create the new alias output
		transitionedAliasOutput := aliasOutput.Clone().(*iotago.AliasOutput)
		if transitionedAliasOutput.AliasID.Empty() {
			transitionedAliasOutput.AliasID = iotago.AliasIDFromOutputID(aliasInput.OutputID())
		}

		if !spamBuilderTmpAlias.AddInput(aliasInput) {
			return false, true
		}

		foundryConsumed := false
		_, remainingFoundryInputs := consumeInputs(aliasInput.FoundryOutputs(), func(foundryInput *UTXO) (consume bool, abort bool) {
			foundryOutput := foundryInput.Output().(*iotago.FoundryOutput)

			foundryID := foundryOutput.MustID()

			// get all basic outputs that contain native tokens with that foundry ID
			basicOutputsWithNativeToken, basicOutputFound := basicOutputsPerNativeTokensMap[foundryID]
			if !basicOutputFound {
				// there is no basic output available that belongs to this foundry, do not consume the foundry output
				return false, false
			}

			if foundryOutput.TokenScheme.(*iotago.SimpleTokenScheme).MintedTokens.Sign() < 1 {
				// no token available in the foundry, do not consume the foundry output
				return false, false
			}

			spamBuilderTmpFoundry := spamBuilderTmpAlias.Clone()

			if !spamBuilderTmpFoundry.AddInput(foundryInput) {
				return false, true
			}

			tokensToMelt := big.NewInt(0)
			basicOutputFound = false
			_, _ = consumeInputs(basicOutputsWithNativeToken, func(basicInput *UTXO) (consume bool, abort bool) {
				basicOutput := basicInput.Output().(*iotago.BasicOutput)

				spamBuilderTmpBasic := spamBuilderTmpFoundry.Clone()

				nativeTokenFound := false
				otherNativeTokenFound := false
				tokensToMeltTmp := big.NewInt(0)
				for _, nativeToken := range basicOutput.NativeTokenList().MustSet() {
					if !foundryID.Matches(nativeToken.ID) {
						// this native token does not belong to this foundry, do not consume the basic output
						otherNativeTokenFound = true
					}
					nativeTokenFound = true
					tokensToMeltTmp.Add(tokensToMeltTmp, nativeToken.Amount)
				}

				if !nativeTokenFound {
					// there is no native token that belongs to this foundry, do not consume the basic output
					return false, false
				}

				if otherNativeTokenFound {
					// there is at least one native token that does not belong to this foundry, do not consume the basic output
					// TODO: we can create a new basic output with the remaining native tokens
					return false, false
				}

				if !spamBuilderTmpBasic.AddInput(basicInput) {
					return false, true
				}

				tokensToMelt.Add(tokensToMelt, tokensToMeltTmp)
				spamBuilderTmpFoundry = spamBuilderTmpBasic

				basicOutputFound = true

				return true, false
			})

			if !basicOutputFound {
				return false, false
			}

			// create the new foundry output
			transitionedFoundryOutput := foundryOutput.Clone().(*iotago.FoundryOutput)

			// melt tokens in foundry
			newMeltedBalance := big.NewInt(0).Add(transitionedFoundryOutput.TokenScheme.(*iotago.SimpleTokenScheme).MeltedTokens, tokensToMelt)
			transitionedFoundryOutput.TokenScheme.(*iotago.SimpleTokenScheme).MeltedTokens = newMeltedBalance

			// we leave some space for the alias output and the remainder output
			if !spamBuilderTmpFoundry.AddOutput(transitionedFoundryOutput, WithOutputMaxOutputsCount(iotago.MaxOutputsCount-2)) {
				return false, true
			}

			foundryConsumed = true
			spamBuilderTmpAlias = spamBuilderTmpFoundry

			return true, false
		})

		if !foundryConsumed {
			return false, false
		}

		if !spamBuilderTmpAlias.AddOutputWithOwnership(transitionedAliasOutput, remainingFoundryInputs) {
			return false, true
		}

		spamBuilder = spamBuilderTmpAlias

		return true, false
	})

	if spamBuilder.ConsumedInputsEmpty() {
		return fmt.Errorf("%w: filtered alias outputs", common.ErrNoUTXOAvailable)
	}

	createdOutputs, utxoRemainder, err := s.BuildTransactionPayloadBlockAndSend(
		ctx,
		spamBuilder,
	)
	if err != nil {
		return err
	}

	// filter the remaining basic inputs
	_, remainingBasicInputs := consumeInputs(accountSender.BasicOutputs(), func(basicInput *UTXO) (consume bool, abort bool) {
		if spamBuilder.InputConsumed(basicInput.OutputID()) {
			return true, false
		}
		return false, false
	})

	if utxoRemainder != nil {
		// add the newly created basic output for the remainder to the remaining basic outputs list
		remainingBasicInputs = append(remainingBasicInputs, utxoRemainder)
	}

	accountSender.SetBasicOutputs(remainingBasicInputs)
	accountSender.SetAliasOutputs(remainingAliasInputs)

	if err := s.bookCreatedOutputs(createdOutputs, accountSender, accountSender, nil); err != nil {
		panic(err)
	}

	return nil
}

func (s *Spammer) foundryOutputDestroy(ctx context.Context, accountSender *LedgerAccount, additionalTag ...string) error {

	if len(accountSender.AliasOutputs()) < 1 {
		return fmt.Errorf("%w: alias outputs", common.ErrNoUTXOAvailable)
	}

	spamBuilder := NewSpamBuilder(accountSender, additionalTag...)

	_, remainingAliasInputs := consumeInputs(accountSender.AliasOutputs(), func(aliasInput *AliasUTXO) (consume bool, abort bool) {
		aliasOutput := aliasInput.Output().(*iotago.AliasOutput)

		// create the new alias output
		transitionedAliasOutput := aliasOutput.Clone().(*iotago.AliasOutput)
		if transitionedAliasOutput.AliasID.Empty() {
			transitionedAliasOutput.AliasID = iotago.AliasIDFromOutputID(aliasInput.OutputID())
		}

		spamBuilderTmp := spamBuilder.Clone()

		if !spamBuilderTmp.AddInput(aliasInput) {
			return false, true
		}

		foundryConsumed := false
		_, remainingFoundryInputs := consumeInputs(aliasInput.FoundryOutputs(), func(foundryInput *UTXO) (consume bool, abort bool) {
			foundryOutput := foundryInput.Output().(*iotago.FoundryOutput)

			if foundryOutput.TokenScheme.(*iotago.SimpleTokenScheme).MintedTokens.Cmp(foundryOutput.TokenScheme.(*iotago.SimpleTokenScheme).MeltedTokens) > 0 {
				// not all tokens melted, do not consume the foundry output
				return false, false
			}

			if !spamBuilderTmp.AddInput(foundryInput) {
				return false, true
			}

			foundryConsumed = true

			return true, false
		})

		if !foundryConsumed {
			return false, false
		}

		if !spamBuilderTmp.AddOutputWithOwnership(transitionedAliasOutput, remainingFoundryInputs) {
			return false, true
		}

		spamBuilder = spamBuilderTmp

		return true, false
	})

	if spamBuilder.ConsumedInputsEmpty() {
		return fmt.Errorf("%w: filtered foundry outputs", common.ErrNoUTXOAvailable)
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
		accountSender.AppendBasicOutput(utxoRemainder)
	}

	accountSender.SetAliasOutputs(remainingAliasInputs)
	if err := s.bookCreatedOutputs(createdOutputs, nil, accountSender, nil); err != nil {
		panic(err)
	}

	return nil
}

func AliasIDFromFoundryID(foundryID iotago.FoundryID) (iotago.AliasID, error) {

	aliasAddress := iotago.AliasAddress{}
	if _, err := aliasAddress.Deserialize(foundryID[:iotago.AliasAddressSerializedBytesSize], serializer.DeSeriModePerformValidation, nil); err != nil {
		return iotago.AliasID{}, err
	}

	return aliasAddress.AliasID(), nil
}
