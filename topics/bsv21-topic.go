package topics

import (
	"context"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type Bsv21TopicManager struct {
	topic    string
	tokenIds map[string]struct{}
}

func NewBsv21TopicManager(topic string, tokenIds []string) (tm *Bsv21TopicManager) {
	tm = &Bsv21TopicManager{
		topic: topic,
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

func (tm *Bsv21TopicManager) IdentifyAdmissableOutputs(ctx context.Context, beefBytes []byte, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	var tx *transaction.Transaction
	_, tx, txid, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return admit, err
	} else if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

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
			admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
		}
	}

	for vin := range tx.Inputs {
		admit.CoinsToRetain = append(admit.CoinsToRetain, uint32(vin))
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

func (tm *Bsv21TopicManager) GetDocumentation() string {
	return "BSV21 Topic Manager"
}

func (tm *Bsv21TopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21",
	}
}
