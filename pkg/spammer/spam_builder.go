package spammer

import (
	iotago "github.com/iotaledger/iota.go/v3"
)

type OutputWithOwnership struct {
	Output       iotago.Output
	OwnedOutputs []*UTXO
}

type SpamBuilder struct {
	accountSender *LedgerAccount
	additionalTag []string

	consumedInputs    []UTXOInterface
	consumedInputsMap map[iotago.OutputID]struct{}
	createdOutputs    []*OutputWithOwnership
	requiredTips      iotago.BlockIDs
	requiredTipsMap   map[iotago.BlockID]struct{}
	maxTipsCount      int
	maxInputsCount    int
	maxOutputsCount   int
}

func NewSpamBuilder(accountSender *LedgerAccount, additionalTag ...string) *SpamBuilder {
	return &SpamBuilder{
		accountSender: accountSender,
		additionalTag: additionalTag,

		consumedInputs:    make([]UTXOInterface, 0),
		consumedInputsMap: make(map[iotago.OutputID]struct{}),
		createdOutputs:    make([]*OutputWithOwnership, 0),
		requiredTips:      make([]iotago.BlockID, 0),
		requiredTipsMap:   make(map[iotago.BlockID]struct{}),
		maxTipsCount:      iotago.BlockMaxParents,
		maxInputsCount:    iotago.MaxInputsCount,
		maxOutputsCount:   iotago.MaxOutputsCount - 1, // we leave some space for the remainder output
	}
}

// getRequiredTips collects the required tips for the given inputs.
func (b *SpamBuilder) getRequiredTips(inputs ...UTXOInterface) iotago.BlockIDs {

	tmpRequiredTips := iotago.BlockIDs{}
	tmpRequiredTipsMap := make(map[iotago.BlockID]struct{})

	for _, input := range inputs {
		blockID := input.PendingBlockID()

		if blockID != iotago.EmptyBlockID() {
			if _, exists := tmpRequiredTipsMap[blockID]; exists {
				// parent already added
				continue
			}

			if _, exists := b.requiredTipsMap[blockID]; exists {
				// parent already added
				continue
			}

			tmpRequiredTips = append(tmpRequiredTips, blockID)
			tmpRequiredTipsMap[blockID] = struct{}{}
		}
	}

	return tmpRequiredTips
}

// checkRequiredTips checks if the required tips for
// the given inputs would fit in the transaction.
func (b *SpamBuilder) checkRequiredTips(input UTXOInterface, options *AddInputOptions) bool {

	tmpRequiredTips := b.getRequiredTips(input)

	return len(b.requiredTips)+len(tmpRequiredTips) <= options.maxTipsCount
}

// addRequiredTips adds the required tips for the given inputs to the transaction.
func (b *SpamBuilder) addRequiredTips(inputs ...UTXOInterface) {
	tmpRequiredTips := b.getRequiredTips(inputs...)

	b.requiredTips = append(b.requiredTips, tmpRequiredTips...)
	for _, blockID := range tmpRequiredTips {
		b.requiredTipsMap[blockID] = struct{}{}
	}
}

// getInputsToConsume collects the inputs that need to be consumed.
func (b *SpamBuilder) getInputsToConsume(inputs ...UTXOInterface) []UTXOInterface {

	tmpConsumedInputs := []UTXOInterface{}
	tmpConsumedInputsMap := make(map[iotago.OutputID]struct{})

	for _, input := range inputs {
		outputID := input.OutputID()

		if _, exists := tmpConsumedInputsMap[outputID]; exists {
			// input already added
			continue
		}

		if _, exists := b.consumedInputsMap[outputID]; exists {
			// input already added
			continue
		}

		tmpConsumedInputs = append(tmpConsumedInputs, input)
		tmpConsumedInputsMap[outputID] = struct{}{}
	}

	return tmpConsumedInputs
}

// checkInputsToConsume checks if the given inputs would fit in the transaction.
func (b *SpamBuilder) checkInputsToConsume(input UTXOInterface, options *AddInputOptions) bool {

	tmpConsumedInputs := b.getInputsToConsume(input)

	return len(b.consumedInputs)+len(tmpConsumedInputs) <= options.maxInputsCount
}

// addInputsToConsume adds the given inputs to the transaction.
func (b *SpamBuilder) addInputsToConsume(inputs ...UTXOInterface) {
	tmpConsumedInputs := b.getInputsToConsume(inputs...)

	for _, input := range tmpConsumedInputs {
		b.consumedInputs = append(b.consumedInputs, input)
		b.consumedInputsMap[input.OutputID()] = struct{}{}
	}
}

// InputConsumed checks if the given input is already consumed.
func (b *SpamBuilder) InputConsumed(outputID iotago.OutputID) bool {
	_, exists := b.consumedInputsMap[outputID]

	return exists
}

// AddInput checks if the given input would fit in the transaction and it them if possible.
func (b *SpamBuilder) AddInput(input UTXOInterface, opts ...AddInputOption) bool {

	options := &AddInputOptions{
		maxTipsCount:   b.maxTipsCount,
		maxInputsCount: b.maxInputsCount,
	}
	options.apply(opts...)

	if !b.checkRequiredTips(input, options) {
		// already reached the parents limit
		return false
	}

	if !b.checkInputsToConsume(input, options) {
		// already reached the inputs limit
		return false
	}

	b.addRequiredTips(input)
	b.addInputsToConsume(input)

	return true
}

// AddOutput adds the given output to the transaction.
func (b *SpamBuilder) AddOutput(output iotago.Output, opts ...AddOutputOption) bool {
	return b.AddOutputWithOwnership(output, make([]*UTXO, 0))
}

// AddOutput adds the given output to the transaction.
func (b *SpamBuilder) AddOutputWithOwnership(output iotago.Output, ownedOutputs []*UTXO, opts ...AddOutputOption) bool {

	options := &AddOutputOptions{
		maxOutputsCount: b.maxOutputsCount,
	}
	options.apply(opts...)

	if len(b.createdOutputs) >= options.maxOutputsCount {
		return false
	}

	b.createdOutputs = append(b.createdOutputs, &OutputWithOwnership{
		Output:       output,
		OwnedOutputs: ownedOutputs,
	})

	return true
}

func (b *SpamBuilder) ConsumedInputsEmpty() bool {
	return len(b.consumedInputs) == 0
}

func (b *SpamBuilder) Clone() *SpamBuilder {
	consumedInputs := make([]UTXOInterface, len(b.consumedInputs))
	copy(consumedInputs, b.consumedInputs)

	consumedInputsMap := make(map[iotago.OutputID]struct{})
	for _, input := range consumedInputs {
		consumedInputsMap[input.OutputID()] = struct{}{}
	}

	createdOutputs := make([]*OutputWithOwnership, len(b.createdOutputs))
	copy(createdOutputs, b.createdOutputs)

	requiredTips := make([]iotago.BlockID, len(b.requiredTips))
	copy(requiredTips, b.requiredTips)

	requiredTipsMap := make(map[iotago.BlockID]struct{})
	for _, tip := range requiredTips {
		requiredTipsMap[tip] = struct{}{}
	}

	return &SpamBuilder{
		accountSender: b.accountSender,
		additionalTag: b.additionalTag,

		consumedInputs:    consumedInputs,
		consumedInputsMap: consumedInputsMap,
		createdOutputs:    createdOutputs,
		requiredTips:      requiredTips,
		requiredTipsMap:   requiredTipsMap,
		maxTipsCount:      b.maxTipsCount,
		maxInputsCount:    b.maxInputsCount,
		maxOutputsCount:   b.maxOutputsCount,
	}
}

// AddInputOption is a function setting an AddInput option.
type AddInputOption func(opts *AddInputOptions)

// AddInputOptions define options for the AddInput function.
type AddInputOptions struct {
	maxTipsCount   int
	maxInputsCount int
}

// applies the given Option.
func (so *AddInputOptions) apply(opts ...AddInputOption) {
	for _, opt := range opts {
		opt(so)
	}
}

// WithInputMaxTipsCount sets the maximum allowed required tips.
func WithInputMaxTipsCount(maxTipsCount int) AddInputOption {
	return func(opts *AddInputOptions) {
		opts.maxTipsCount = maxTipsCount
	}
}

// WithInputMaxInputsCount sets the maximum allowed inputs.
func WithInputMaxInputsCount(maxInputsCount int) AddInputOption {
	return func(opts *AddInputOptions) {
		opts.maxInputsCount = maxInputsCount
	}
}

// AddOutputOption is a function setting an AddOutput option.
type AddOutputOption func(opts *AddOutputOptions)

// AddOutputOptions define options for the AddOutput function.
type AddOutputOptions struct {
	maxOutputsCount int
}

// applies the given Option.
func (so *AddOutputOptions) apply(opts ...AddOutputOption) {
	for _, opt := range opts {
		opt(so)
	}
}

// WithInputMaxInputsCount sets the maximum allowed outputs.
func WithOutputMaxOutputsCount(maxOutputsCount int) AddOutputOption {
	return func(opts *AddOutputOptions) {
		opts.maxOutputsCount = maxOutputsCount
	}
}
