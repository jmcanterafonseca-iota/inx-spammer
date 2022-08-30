package hdwallet

import (
	"crypto/ed25519"
	"fmt"

	"github.com/wollac/iota-crypto-demo/pkg/bip32path"
	"github.com/wollac/iota-crypto-demo/pkg/bip39"
	"github.com/wollac/iota-crypto-demo/pkg/slip10"
	"github.com/wollac/iota-crypto-demo/pkg/slip10/eddsa"

	iotago "github.com/iotaledger/iota.go/v3"
)

// 24 words.
const MnemonicSeedSize = 64

// Registered coin types, https://github.com/satoshilabs/slips/blob/master/slip-0044.md
// BIP44 path scheme: m/purpose'/coin_type'/account'/change/address_index.
const BIP32PathString = "44'/4218'/%d'/%d'/%d'"

type HDWallet struct {
	// protocol parameters
	protoParas *iotago.ProtocolParameters
	// mnemonic seed
	seed []byte
	// BIP32 account index
	accountIndex uint64
	isChange     bool
}

func NewHDWallet(protoParas *iotago.ProtocolParameters, mnemonic []string, passpharse string, accountIndex uint64, isChange bool) (*HDWallet, error) {

	seed, err := bip39.MnemonicToSeed(mnemonic, passpharse)
	if err != nil {
		return nil, fmt.Errorf("mnemonic to seed failed: %w", err)
	}

	if len(seed) != MnemonicSeedSize {
		return nil, fmt.Errorf("invalid mnemonic seed length: %d", len(seed))
	}

	return &HDWallet{
		protoParas:   protoParas,
		accountIndex: accountIndex,
		isChange:     isChange,
		seed:         seed,
	}, nil
}

func (hd *HDWallet) keyPair(acount uint64, isChange bool, addressIndex uint64) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	change := 0
	if isChange {
		change = 1
	}

	path, err := bip32path.ParsePath(fmt.Sprintf(BIP32PathString, acount, change, addressIndex))
	if err != nil {
		return nil, nil, fmt.Errorf("bip32 parse path failed: %w", err)
	}

	curve := eddsa.Ed25519()
	key, err := slip10.DeriveKeyFromPath(hd.seed[:MnemonicSeedSize], curve, path)
	if err != nil {
		return nil, nil, fmt.Errorf("splip10 derive key failed: %w", err)
	}

	pubKey, privKey := key.Key.(eddsa.Seed).Ed25519Key()

	return ed25519.PrivateKey(privKey), ed25519.PublicKey(pubKey), nil
}

func (hd *HDWallet) AddressSigner(index uint64) (iotago.AddressSigner, error) {
	privKey, pubKey, err := hd.keyPair(hd.accountIndex, hd.isChange, index)
	if err != nil {
		return nil, err
	}
	address := iotago.Ed25519AddressFromPubKey(pubKey)

	return iotago.NewInMemoryAddressSigner(iotago.NewAddressKeysForEd25519Address(&address, privKey)), nil
}

func (hd *HDWallet) Ed25519AddressFromIndex(index uint64) (*iotago.Ed25519Address, error) {
	_, pubKey, err := hd.keyPair(hd.accountIndex, hd.isChange, index)
	if err != nil {
		return nil, err
	}
	address := iotago.Ed25519AddressFromPubKey(pubKey)

	return &address, nil
}

func (hd *HDWallet) Ed25519AddressAndSigner(index uint64) (*iotago.Ed25519Address, iotago.AddressSigner, error) {
	privKey, pubKey, err := hd.keyPair(hd.accountIndex, hd.isChange, index)
	if err != nil {
		return nil, nil, err
	}

	address := iotago.Ed25519AddressFromPubKey(pubKey)

	return &address, iotago.NewInMemoryAddressSigner(iotago.NewAddressKeysForEd25519Address(&address, privKey)), nil
}
