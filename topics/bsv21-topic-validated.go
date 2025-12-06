package topics

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

// SyncMode defines how the topic manager handles missing inputs during processing
type SyncMode int

const (
	// SyncModeFull assumes all inputs are already in overlay, missing inputs don't exist (GASP sync)
	SyncModeFull SyncMode = iota
	// SyncModeAdhoc processes transactions as encountered, missing inputs may arrive later (SSE/LibP2P/submit)
	SyncModeAdhoc
)

// MissingInputError represents a missing input with specific transaction details
type MissingInputError struct {
	TransactionID *chainhash.Hash // The transaction that's missing an input
	InputIndex    uint32          // Which input index is missing
	MissingTxID   *chainhash.Hash // The source transaction ID that's missing
	OutputIndex   uint32          // The output index in the missing transaction
	Message       string          // Additional context
}

func (e *MissingInputError) Error() string {
	return fmt.Sprintf("%s: transaction %s input[%d] missing source %s:%d",
		e.Message, e.TransactionID.String(), e.InputIndex, e.MissingTxID.String(), e.OutputIndex)
}

// NewMissingInputError creates a new MissingInputError
func NewMissingInputError(txid, missingTxID *chainhash.Hash, inputIndex, outputIndex uint32, message string) *MissingInputError {
	return &MissingInputError{
		TransactionID: txid,
		InputIndex:    inputIndex,
		MissingTxID:   missingTxID,
		OutputIndex:   outputIndex,
		Message:       message,
	}
}

type Bsv21ValidatedTopicManager struct {
	topic    string
	storage  engine.Storage
	tokenIds map[string]struct{}
}

func NewBsv21ValidatedTopicManager(topic string, storage engine.Storage, tokenIds []string) (tm *Bsv21ValidatedTopicManager) {
	tm = &Bsv21ValidatedTopicManager{
		topic:   topic,
		storage: storage,
	}
	if len(tokenIds) > 0 {
		tm.tokenIds = make(map[string]struct{}, len(tokenIds))
		for _, tokenId := range tokenIds {
			tm.tokenIds[tokenId] = struct{}{}
		}
	}
	return
}

func (tm *Bsv21ValidatedTopicManager) HasTokenId(tokenId string) bool {
	if tm.tokenIds == nil {
		return true
	}
	_, ok := tm.tokenIds[tokenId]
	return ok
}

type tokenSummary struct {
	tokensIn   uint64
	tokensOut  uint64
	deploy     bool
	hasAuth    bool     // Track if this token has auth input
	tokenVouts []uint32 // Outputs that transfer/burn tokens
	authVouts  []uint32 // Auth outputs that need auth input
	invalid    bool
}

func (tm *Bsv21ValidatedTopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beefBytes []byte, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	_, tx, txid, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return admit, err
	} else if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	summary := make(map[string]*tokenSummary)
	relevantTokenIds := make(map[string]struct{})

	// First pass: identify all relevant token IDs in outputs
	for vout, output := range tx.Outputs {
		if b := bsv21.Decode(output.LockingScript); b != nil {
			// Set ID for deploy operations
			if b.Op == string(bsv21.OpDeployMint) || b.Op == string(bsv21.OpDeployAuth) {
				b.Id = (&transaction.Outpoint{
					Txid:  *txid,
					Index: uint32(vout),
				}).OrdinalString()
			}
			if !tm.HasTokenId(b.Id) {
				continue
			}
			relevantTokenIds[b.Id] = struct{}{}

			// Handle deploy operations - auto-admit
			if b.Op == string(bsv21.OpDeployMint) || b.Op == string(bsv21.OpDeployAuth) {
				admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
				continue
			}

			// Ensure token summary exists
			if _, ok := summary[b.Id]; !ok {
				summary[b.Id] = &tokenSummary{}
			}
			token := summary[b.Id]

			switch b.Op {
			case string(bsv21.OpAuth), string(bsv21.OpMint):
				token.authVouts = append(token.authVouts, uint32(vout))
			case string(bsv21.OpTransfer), string(bsv21.OpBurn):
				token.tokensOut += b.Amt
				token.tokenVouts = append(token.tokenVouts, uint32(vout))
			}

			// Handle transfer/burn outputs - require token balance validation
		}
	}

	if len(summary) > 0 {
		ancillaryTxids := make(map[chainhash.Hash]struct{}, len(tx.Inputs))

		// Single loop to process inputs and detect missing Inputs
		for vin, txin := range tx.Inputs {
			ancillaryTxids[*txin.SourceTXID] = struct{}{}
			outpoint := &transaction.Outpoint{
				Txid:  *txin.SourceTXID,
				Index: txin.SourceTxOutIndex,
			}
			if sourceOutput := txin.SourceTxOutput(); sourceOutput != nil {
				if b := bsv21.Decode(sourceOutput.LockingScript); b != nil {
					if b.Op == string(bsv21.OpDeployMint) || b.Op == string(bsv21.OpDeployAuth) {
						b.Id = outpoint.OrdinalString()
					}
					if !tm.HasTokenId(b.Id) {
						continue
					}
					if slices.Contains(previousCoins, uint32(vin)) {
						// We have this input - process it
						slog.Debug("BSV21_INPUT_FOUND", "topic", tm.topic, "txid", txid.String(), "vin", vin, "source_txid", txin.SourceTXID.String())
						admit.CoinsToRetain = append(admit.CoinsToRetain, uint32(vin))

						if token, ok := summary[b.Id]; ok {
							// Track auth inputs
							switch b.Op {
							case string(bsv21.OpAuth), string(bsv21.OpDeployAuth):
								token.hasAuth = true
							case string(bsv21.OpTransfer), string(bsv21.OpMint), string(bsv21.OpDeployMint):
								token.tokensIn += b.Amt
							case string(bsv21.OpBurn):
								token.invalid = true // Cannot burn from input
							}
						}
					} else {
						// NOW log the missing input - we confirmed it's a relevant BSV21 token
						return admit, NewMissingInputError(txid, txin.SourceTXID, uint32(vin), txin.SourceTxOutIndex, "BSV21_INPUT_MISSING")
					}
				}
			}
		}

		for _, token := range summary {
			if len(token.authVouts) > 0 {
				if !token.hasAuth {
					continue // Missing auth input
				} else {
					admit.OutputsToAdmit = append(admit.OutputsToAdmit, token.authVouts...)
				}
			}
			if !token.invalid && token.tokensIn >= token.tokensOut {
				admit.OutputsToAdmit = append(admit.OutputsToAdmit, token.tokenVouts...)
			}
		}

		// Add ancillary txids if any
		if len(ancillaryTxids) > 0 {
			admit.AncillaryTxids = make([]*chainhash.Hash, 0, len(ancillaryTxids))
			for txidHash := range ancillaryTxids {
				hash := txidHash // Must copy - loop variable is reused
				admit.AncillaryTxids = append(admit.AncillaryTxids, &hash)
			}
		}
	}

	return
}

func (tm *Bsv21ValidatedTopicManager) IdentifyNeededInputs(ctx context.Context, beefBytes []byte) ([]*transaction.Outpoint, error) {
	_, tx, _, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return nil, err
	} else if tx == nil {
		return nil, engine.ErrInvalidBeef
	}

	tokens := make(map[string]struct{})
	for _, output := range tx.Outputs {
		if b := bsv21.Decode(output.LockingScript); b != nil {
			if !tm.HasTokenId(b.Id) {
				continue
			}
			tokens[b.Id] = struct{}{}
		}
	}

	if len(tokens) == 0 {
		return nil, nil
	}
	var inputs []*transaction.Outpoint
	for _, txin := range tx.Inputs {
		// if inTx := beef.FindTransaction(txin.SourceTXID.String()); inTx != nil {
		// 	outpoint := &transaction.Outpoint{
		// 		Txid:  *txin.SourceTXID,
		// 		Index: txin.SourceTxOutIndex,
		// 	}
		// 	script := inTx.Outputs[txin.SourceTxOutIndex].LockingScript
		// 	if b := bsv21.Decode(script); b != nil {
		// 		if b.Op == string(bsv21.OpMint) {
		// 			b.Id = (outpoint).OrdinalString()
		// 		}
		// 		if _, exists := tokens[b.Id]; !exists {
		// 			continue
		// 		}
		// 		inputs = append(inputs, outpoint)
		// 	}
		// }
		if txin.SourceTransaction == nil {
			inputs = append(inputs, &transaction.Outpoint{
				Txid:  *txin.SourceTXID,
				Index: txin.SourceTxOutIndex,
			})
		}
	}
	return inputs, nil
}

func (tm *Bsv21ValidatedTopicManager) GetDocumentation() string {
	return "BSV21 Topic Manager"
}

func (tm *Bsv21ValidatedTopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21",
	}
}
