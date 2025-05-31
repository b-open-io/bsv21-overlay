package topics

import (
	"context"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

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
	tokensIn  uint64
	tokensOut uint64
	vouts     []uint32
	deploy    bool
}

func (tm *Bsv21ValidatedTopicManager) IdentifyAdmissibleOutputs(ctx context.Context, beefBytes []byte, previousCoins map[uint32]*transaction.TransactionOutput) (admit overlay.AdmittanceInstructions, err error) {
	var tx *transaction.Transaction
	_, tx, txid, err := transaction.ParseBeef(beefBytes)
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

	if len(summary) > 0 {
		for vin, txin := range tx.Inputs {
			if txout, ok := previousCoins[uint32(vin)]; ok {
				outpoint := &overlay.Outpoint{
					Txid:        *txin.SourceTXID,
					OutputIndex: txin.SourceTxOutIndex,
				}

				if b := bsv21.Decode(txout.LockingScript); b != nil {
					if b.Op == string(bsv21.OpMint) {
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
	}

	return
}

func (tm *Bsv21ValidatedTopicManager) IdentifyNeededInputs(ctx context.Context, beefBytes []byte) ([]*overlay.Outpoint, error) {
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
		if inTx := beef.FindTransaction(txin.SourceTXID.String()); inTx != nil {
			outpoint := &overlay.Outpoint{
				Txid:        *txin.SourceTXID,
				OutputIndex: txin.SourceTxOutIndex,
			}
			script := inTx.Outputs[txin.SourceTxOutIndex].LockingScript
			if b := bsv21.Decode(script); b != nil {
				if b.Op == string(bsv21.OpMint) {
					b.Id = (outpoint).OrdinalString()
				}
				if !tm.HasTokenId(b.Id) {
					continue
				}
				inputs = append(inputs, outpoint)
			}
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
