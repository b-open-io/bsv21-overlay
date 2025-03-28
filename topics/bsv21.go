package topics

import (
	"context"
	"slices"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type Bsv21TopicManager struct {
	topic    string
	storage  engine.Storage
	tokenIds map[string]struct{}
}

func NewBsv21TopicManager(topic string, storage engine.Storage, tokenIds []string) (tm *Bsv21TopicManager) {
	tm = &Bsv21TopicManager{
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

func (tm *Bsv21TopicManager) HasTokenId(tokenId string) bool {
	if tm.tokenIds == nil {
		return true
	}
	_, ok := tm.tokenIds[tokenId]
	return ok
}

type tokenSummary struct {
	tokensIn  uint64
	tokensOut uint64
	vouts     []uint32
	deploy    bool
}

func (tm *Bsv21TopicManager) IdentifyAdmissableOutputs(ctx context.Context, beefBytes []byte, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	var tx *transaction.Transaction
	beef, tx, txid, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return admit, err
	} else if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	summary := make(map[string]*tokenSummary)
	for vout, output := range tx.Outputs {
		if b := bsv21.Decode(output.LockingScript); b != nil {
			if b.Op == string(bsv21.OpMint) {
				b.Id = (&overlay.Outpoint{
					Txid:        *txid,
					OutputIndex: uint32(vout),
				}).OrdinalString()
			}
			if !tm.HasTokenId(b.Id) {
				continue
			}
			if b.Op == string(bsv21.OpMint) {
				admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
				continue
			}

			if token, ok := summary[b.Id]; !ok {
				summary[b.Id] = &tokenSummary{
					tokensOut: b.Amt,
					vouts:     []uint32{uint32(vout)},
				}
			} else {
				token.tokensOut += b.Amt
				token.vouts = append(token.vouts, uint32(vout))
			}
		}
	}

	deps := make(map[string]*chainhash.Hash, len(tx.Inputs))
	if len(summary) > 0 {
		for vin, txin := range tx.Inputs {
			sourceTx := beef.FindTransaction(txin.SourceTXID.String())
			if sourceTx == nil {
				return admit, engine.ErrMissingInput
			}
			deps[txin.SourceTXID.String()] = txin.SourceTXID
			if slices.Contains(previousCoins, uint32(vin)) {
				if b := bsv21.Decode(sourceTx.Outputs[txin.SourceTxOutIndex].LockingScript); b != nil {
					if b.Op == string(bsv21.OpMint) {
						outpoint := &overlay.Outpoint{
							Txid:        *txin.SourceTXID,
							OutputIndex: txin.SourceTxOutIndex,
						}
						b.Id = outpoint.OrdinalString()
					}
					if !tm.HasTokenId(b.Id) {
						continue
					}
					admit.CoinsToRetain = append(admit.CoinsToRetain, uint32(vin))
					if token, ok := summary[b.Id]; !ok {
						continue
					} else {
						token.tokensIn += b.Amt
					}
				}
			}

		}
		for _, token := range summary {
			if token.tokensIn >= token.tokensOut {
				admit.OutputsToAdmit = append(admit.OutputsToAdmit, token.vouts...)
			}
		}
		for _, depId := range deps {
			admit.TxidsToInclude = append(admit.TxidsToInclude, depId)
		}
	}

	return
}

func (tm *Bsv21TopicManager) IdentifyNeededInputs(ctx context.Context, beefBytes []byte) ([]*overlay.Outpoint, error) {
	beef, tx, _, err := transaction.ParseBeef(beefBytes)
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
	var inputs []*overlay.Outpoint
	for _, txin := range tx.Inputs {
		var lockingScript *script.Script
		if inTx := beef.FindTransaction(txin.SourceTXID.String()); inTx == nil {
			return nil, engine.ErrMissingInput
		} else {
			lockingScript = inTx.Outputs[txin.SourceTxOutIndex].LockingScript
		}

		if b := bsv21.Decode(lockingScript); b != nil {
			if b.Op == string(bsv21.OpMint) {
				b.Id = (&overlay.Outpoint{
					Txid:        *txin.SourceTXID,
					OutputIndex: txin.SourceTxOutIndex,
				}).OrdinalString()
			}
			if !tm.HasTokenId(b.Id) {
				continue
			}
			inputs = append(inputs, &overlay.Outpoint{
				Txid:        *txin.SourceTXID,
				OutputIndex: txin.SourceTxOutIndex,
			})

		}
	}
	return inputs, nil
}

func (tm *Bsv21TopicManager) GetDocumentation() string {
	return "BSV21 Topic Manager"
}

func (tm *Bsv21TopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21",
	}
}
