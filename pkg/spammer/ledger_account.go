package spammer

import (
	"context"
	"fmt"

	"github.com/iotaledger/inx-spammer/pkg/hdwallet"
	iotago "github.com/iotaledger/iota.go/v3"
	"github.com/iotaledger/iota.go/v3/nodeclient"
)

type LedgerAccount struct {
	basicOutputs []*UTXO
	aliasOutputs []*AliasUTXO
	nftOutputs   []*UTXO

	protocolParametersFunc func() *iotago.ProtocolParameters
	address                iotago.Address
	signer                 iotago.AddressSigner
}

func NewLedgerAccount(wallet *hdwallet.HDWallet, addressIndex uint64, protocolParametersFunc func() *iotago.ProtocolParameters) (*LedgerAccount, error) {

	walletAddress, walletSigner, err := wallet.Ed25519AddressAndSigner(addressIndex)
	if err != nil {
		return nil, err
	}

	return &LedgerAccount{
		basicOutputs:           make([]*UTXO, 0),
		aliasOutputs:           make([]*AliasUTXO, 0),
		nftOutputs:             make([]*UTXO, 0),
		protocolParametersFunc: protocolParametersFunc,
		address:                walletAddress,
		signer:                 walletSigner,
	}, nil
}

func (la *LedgerAccount) Address() iotago.Address {
	return la.address
}

func (la *LedgerAccount) AddressBech32() string {
	return la.address.Bech32(la.protocolParametersFunc().Bech32HRP)
}

func (la *LedgerAccount) Signer() iotago.AddressSigner {
	return la.signer
}

func (la *LedgerAccount) ResetOutputs() {
	la.basicOutputs = make([]*UTXO, 0)
	la.aliasOutputs = make([]*AliasUTXO, 0)
	la.nftOutputs = make([]*UTXO, 0)
}

func (la *LedgerAccount) ResetBasicOutputs() {
	la.basicOutputs = make([]*UTXO, 0)
}

func (la *LedgerAccount) ResetAliasOutputs() {
	la.aliasOutputs = make([]*AliasUTXO, 0)
}

func (la *LedgerAccount) ResetFoundryOutputs() {
	for _, aliasInput := range la.aliasOutputs {
		aliasInput.foundryOutputs = make([]*UTXO, 0)
	}
}

func (la *LedgerAccount) ResetNFTOutputs() {
	la.nftOutputs = make([]*UTXO, 0)
}

func (la *LedgerAccount) Empty() bool {
	return len(la.basicOutputs) == 0 &&
		len(la.aliasOutputs) == 0 &&
		len(la.nftOutputs) == 0
}

func (la *LedgerAccount) BasicOutputs() []*UTXO {
	return la.basicOutputs
}

func (la *LedgerAccount) BasicOutputsCount() int {
	return len(la.basicOutputs)
}

func (la *LedgerAccount) SetBasicOutputs(basicOutputs []*UTXO) {
	la.basicOutputs = basicOutputs
}

func (la *LedgerAccount) AppendBasicOutput(basicOutput *UTXO) {
	la.basicOutputs = append(la.basicOutputs, basicOutput)
}

func (la *LedgerAccount) AliasOutputs() []*AliasUTXO {
	return la.aliasOutputs
}

func (la *LedgerAccount) AliasOutputsCount() int {
	return len(la.aliasOutputs)
}

func (la *LedgerAccount) SetAliasOutputs(aliasOutputs []*AliasUTXO) {
	la.aliasOutputs = aliasOutputs
}

func (la *LedgerAccount) AppendAliasOutput(aliasOutput *AliasUTXO) {
	la.aliasOutputs = append(la.aliasOutputs, aliasOutput)
}

func (la *LedgerAccount) AppendFoundryOutput(foundryOutput *UTXO) error {
	foundryInput, ok := foundryOutput.Output().(*iotago.FoundryOutput)
	if !ok {
		panic(fmt.Sprintf("invalid type: expected *iotago.FoundryOutput, got %T", foundryOutput.Output()))
	}

	foundryID := foundryInput.MustID()
	aliasID, err := AliasIDFromFoundryID(foundryID)
	if err != nil {
		return err
	}

	for _, aliasOutput := range la.aliasOutputs {
		aliasInput, ok := aliasOutput.Output().(*iotago.AliasOutput)
		if !ok {
			panic(fmt.Sprintf("invalid type: expected *iotago.AliasOutput, got %T", aliasOutput.Output()))
		}

		if !aliasInput.AliasID.Matches(aliasID) {
			continue
		}

		aliasOutput.AppendFoundryOutput(foundryOutput)

		return nil
	}

	return fmt.Errorf("no alias output found for foundry output: %s", foundryID.String())
}

func (la *LedgerAccount) FoundryOutputsCount() int {
	count := 0
	for _, aliasOutput := range la.aliasOutputs {
		count += len(aliasOutput.FoundryOutputs())
	}

	return count
}

func (la *LedgerAccount) NFTOutputs() []*UTXO {
	return la.nftOutputs
}

func (la *LedgerAccount) NFTOutputsCount() int {
	return len(la.nftOutputs)
}

func (la *LedgerAccount) SetNFTOutputs(nftOutputs []*UTXO) {
	la.nftOutputs = nftOutputs
}

func (la *LedgerAccount) AppendNFTOutput(nftOutput *UTXO) {
	la.nftOutputs = append(la.nftOutputs, nftOutput)
}

func (la *LedgerAccount) queryIndexer(ctx context.Context, indexer nodeclient.IndexerClient, query nodeclient.IndexerQuery, maxResults ...int) ([]*UTXO, error) {

	result, err := indexer.Outputs(ctx, query)
	if err != nil {
		return nil, err
	}

	utxos := []*UTXO{}
	var utxosCount int
	for result.Next() {

		outputs, err := result.Outputs()
		if err != nil {
			return nil, err
		}
		outputIDs := result.Response.Items.MustOutputIDs()

		for i := range outputs {
			if (len(maxResults) > 0) && (utxosCount >= maxResults[0]) {
				return utxos, nil
			}

			utxos = append(utxos, NewUTXO(
				outputIDs[i],
				outputs[i],
				iotago.EmptyBlockID(),
			))

			utxosCount++
		}
	}
	if result.Error != nil {
		return nil, result.Error
	}

	return utxos, nil
}

func (la *LedgerAccount) ClearSpentOutputs(spentsMap map[iotago.OutputID]struct{}) {

	remainingBasicInputs := make([]*UTXO, 0)
	for _, input := range la.basicOutputs {
		if _, spent := spentsMap[input.OutputID()]; !spent {
			remainingBasicInputs = append(remainingBasicInputs, input)
		}
	}

	remainingAliasInputs := make([]*AliasUTXO, 0)
	for _, input := range la.aliasOutputs {
		if _, spent := spentsMap[input.OutputID()]; !spent {
			// check the foundry outputs of every alias input
			remainingFoundryInputs := make([]*UTXO, 0)
			for _, input := range input.foundryOutputs {
				if _, spent := spentsMap[input.OutputID()]; !spent {
					remainingFoundryInputs = append(remainingFoundryInputs, input)
				}
			}
			input.foundryOutputs = remainingFoundryInputs

			remainingAliasInputs = append(remainingAliasInputs, input)
		}
	}

	remainingNFTInputs := make([]*UTXO, 0)
	for _, input := range la.nftOutputs {
		if _, spent := spentsMap[input.OutputID()]; !spent {
			remainingNFTInputs = append(remainingNFTInputs, input)
		}
	}

	la.basicOutputs = remainingBasicInputs
	la.aliasOutputs = remainingAliasInputs
	la.nftOutputs = remainingNFTInputs
}

func (la *LedgerAccount) QueryOutputsFromIndexer(ctx context.Context,
	indexer nodeclient.IndexerClient,
	allowNativeTokens bool,
	queryBasicOutputs bool,
	queryAliasOutputs bool,
	queryFoundryOutputs bool,
	queryNFTOutputs bool,
	maxResults ...int) error {

	// first reset all known outputs
	la.ResetOutputs()

	if queryBasicOutputs {
		// get current unspent basic outputs
		unspentBasicOutputs, err := la.queryIndexer(ctx, indexer, collectBasicOutputsQuery(la.AddressBech32(), allowNativeTokens), maxResults...)
		if err != nil {
			return err
		}
		la.basicOutputs = append(la.basicOutputs, unspentBasicOutputs...)
	}

	if queryAliasOutputs {
		// get current unspent alias outputs
		unspentAliasOutputsWithoutFoundries, err := la.queryIndexer(ctx, indexer, collectAliasOutputsQuery(la.AddressBech32()), maxResults...)
		if err != nil {
			return err
		}

		// get current unspent foundry outputs per alias
		unspentAliasOutputs := make([]*AliasUTXO, 0)
		for _, aliasOutput := range unspentAliasOutputsWithoutFoundries {
			if queryFoundryOutputs {
				foundryOutputs, err := la.queryIndexer(ctx, indexer, collectFoundryOutputsQuery(aliasOutput.Output().(*iotago.AliasOutput).AliasID.ToAddress().Bech32(la.protocolParametersFunc().Bech32HRP)), maxResults...)
				if err != nil {
					return err
				}
				unspentAliasOutputs = append(unspentAliasOutputs, NewAliasUTXO(aliasOutput.OutputID(), aliasOutput.Output(), aliasOutput.PendingBlockID(), foundryOutputs))
			}
		}
		la.aliasOutputs = append(la.aliasOutputs, unspentAliasOutputs...)
	}

	if queryNFTOutputs {
		// get current unspent NFT outputs
		unspentNFTOutputs, err := la.queryIndexer(ctx, indexer, collectNFTOutputsQuery(la.AddressBech32()), maxResults...)
		if err != nil {
			return err
		}
		la.nftOutputs = append(la.nftOutputs, unspentNFTOutputs...)
	}

	return nil
}

func (la *LedgerAccount) CleanupOwnershipBasicOutputs() {
	basicOutputs := make([]*UTXO, 0)
	for _, basicInput := range la.basicOutputs {

		basicOutput, ok := basicInput.Output().(*iotago.BasicOutput)
		if !ok {
			panic(fmt.Sprintf("invalid type: expected *iotago.BasicOutput, got %T", basicInput.Output()))
		}

		nativeTokens := basicOutput.NativeTokenList().MustSet()
		if len(nativeTokens) != 0 {
			// output contains native tokens, ignore that output
			continue
		}

		basicOutputs = append(basicOutputs, basicInput)
	}

	la.basicOutputs = basicOutputs
}
