package spammer

import (
	"context"
	"fmt"

	"github.com/iotaledger/inx-spammer/pkg/common"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/nodeclient"
)

// collects NFT outputs from a given address
func collectNFTOutputsQuery(addressBech32 string) nodeclient.IndexerQuery {
	falseCondition := false
	return &nodeclient.NFTsQuery{
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
}

func (s *Spammer) nftOutputCreate(ctx context.Context, accountSender *LedgerAccount, additionalTag ...string) error {

	if len(accountSender.BasicOutputs()) < 1 {
		return fmt.Errorf("%w: basic outputs", common.ErrNoUTXOAvailable)
	}

	spamBuilder := NewSpamBuilder(accountSender, additionalTag...)

	_, remainingBasicInputs := consumeInputs(accountSender.BasicOutputs(), func(basicInput *UTXO) (consume bool, abort bool) {
		basicOutput := basicInput.Output().(*iotago.BasicOutput)

		nativeTokens := basicOutput.NativeTokenList().MustSet()
		if len(nativeTokens) != 0 {
			// output contains native tokens, do not consume the basic output
			return false, true
		}

		if !spamBuilder.AddInput(basicInput) {
			return false, true
		}

		return true, false
	})

	if spamBuilder.ConsumedInputsEmpty() {
		return fmt.Errorf("%w: filtered basic outputs", common.ErrNoUTXOAvailable)
	}

	// create the new NFT output
	targetNFTOutput := &iotago.NFTOutput{
		NFTID: iotago.NFTID{},
		Conditions: iotago.UnlockConditions{
			&iotago.AddressUnlockCondition{Address: accountSender.Address()},
		},
		ImmutableFeatures: iotago.Features{
			&iotago.IssuerFeature{Address: accountSender.Address()},
			&iotago.MetadataFeature{Data: []byte("some-ipfs-link")},
		},
	}
	if !spamBuilder.AddOutput(targetNFTOutput) {
		return fmt.Errorf("%w: NFT outputs", common.ErrMaxOutputsCountExceeded)
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
	if err := s.bookCreatedOutputs(createdOutputs, nil, nil, accountSender); err != nil {
		panic(err)
	}

	return nil
}

func (s *Spammer) nftOutputSend(ctx context.Context, accountSender *LedgerAccount, accountReceiver *LedgerAccount, additionalTag ...string) error {

	if len(accountSender.NFTOutputs()) < 1 {
		return fmt.Errorf("%w: NFT outputs", common.ErrNoUTXOAvailable)
	}

	spamBuilder := NewSpamBuilder(accountSender, additionalTag...)

	_, remainingNFTInputs := consumeInputs(accountSender.NFTOutputs(), func(nftInput *UTXO) (consume bool, abort bool) {
		nftOutput := nftInput.Output().(*iotago.NFTOutput)

		// create the new NFT output
		transitionedNFTOutput := nftOutput.Clone().(*iotago.NFTOutput)
		transitionedNFTOutput.UnlockConditionSet().Address().Address = accountReceiver.Address()
		if transitionedNFTOutput.NFTID.Empty() {
			transitionedNFTOutput.NFTID = iotago.NFTIDFromOutputID(nftInput.OutputID())
		}

		tmpSpamBuilder := spamBuilder.Clone()

		if !tmpSpamBuilder.AddInput(nftInput) {
			return false, true
		}

		if !tmpSpamBuilder.AddOutput(transitionedNFTOutput) {
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

	accountSender.SetNFTOutputs(remainingNFTInputs)
	if err := s.bookCreatedOutputs(createdOutputs, nil, nil, accountReceiver); err != nil {
		panic(err)
	}

	return nil
}

func (s *Spammer) nftOutputDestroy(ctx context.Context, accountSender *LedgerAccount, additionalTag ...string) error {

	if len(accountSender.NFTOutputs()) < 1 {
		return fmt.Errorf("%w: NFT outputs", common.ErrNoUTXOAvailable)
	}

	spamBuilder := NewSpamBuilder(accountSender, additionalTag...)

	_, remainingNFTInputs := consumeInputs(accountSender.NFTOutputs(), func(nftInput *UTXO) (consume bool, abort bool) {
		if !spamBuilder.AddInput(nftInput) {
			return false, true
		}

		return true, false
	})

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

	accountSender.SetNFTOutputs(remainingNFTInputs)

	return nil
}
