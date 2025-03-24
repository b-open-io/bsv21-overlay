package topics

import (
	"github.com/4chain-ag/go-overlay-services/pkg/engine"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type bsv21TopicManager struct {
	tokenIds map[string]struct{}
}

func NewBsv21TopicManager(tokenIds []string) (tm *bsv21TopicManager) {
	tm = &bsv21TopicManager{}
	if len(tokenIds) > 0 {
		tm.tokenIds = make(map[string]struct{}, len(tokenIds))
		for _, tokenId := range tokenIds {
			tm.tokenIds[tokenId] = struct{}{}
		}
	}
	return
}

func (tm *bsv21TopicManager) HasTokenId(tokenId string) bool {
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

func (tm *bsv21TopicManager) IdentifyAdmissableOutputs(beef *transaction.Beef, txid *chainhash.Hash, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	tx := beef.FindTransaction(txid.String())
	if tx == nil {
		return admit, engine.ErrNotFound
	}
	summary := make(map[string]*tokenSummary)
	for vout, output := range tx.Outputs {
		if b := bsv21.Decode(output.LockingScript); b != nil {
			if !tm.HasTokenId(b.Id) {
				continue
			}

			if b.Op == string(bsv21.OpMint) {
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
		for _, vin := range previousCoins {
			txin := tx.Inputs[vin]
			var lockingScript *script.Script
			if inTx := beef.FindTransaction(txin.SourceTXID.String()); inTx == nil {
				err = engine.ErrMissingInput
				return
			} else {
				lockingScript = inTx.Outputs[txin.SourceTxOutIndex].LockingScript
			}

			if b := bsv21.Decode(lockingScript); b != nil {
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
		for _, token := range summary {
			if token.tokensIn >= token.tokensOut {
				admit.OutputsToAdmit = append(admit.CoinsToRetain, token.vouts...)
			}
		}
	}

	return
}

func (tm *bsv21TopicManager) IdentifyNeededInputs(beef *transaction.Beef, txid *chainhash.Hash) (inputs []*overlay.Outpoint, err error) {
	tx := beef.FindTransaction(txid.String())
	if tx == nil {
		return nil, engine.ErrNotFound
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

	for _, txin := range tx.Inputs {
		var lockingScript *script.Script
		if inTx := beef.FindTransaction(txin.SourceTXID.String()); inTx == nil {
			err = engine.ErrMissingInput
			return
		} else {
			lockingScript = inTx.Outputs[txin.SourceTxOutIndex].LockingScript
		}

		if b := bsv21.Decode(lockingScript); b != nil {
			if !tm.HasTokenId(b.Id) {
				continue
			}
			inputs = append(inputs, &overlay.Outpoint{
				Txid:        *txin.SourceTXID,
				OutputIndex: txin.SourceTxOutIndex,
			})

		}
	}
	return
}

func (tm *bsv21TopicManager) GetDocumentation() string {
	return "BSV21 Topic Manager"
}

func (tm *bsv21TopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21",
	}
}

// type TopicManager interface {
// 	IdentifyAdmissableOutputs(beef *transaction.Beef, txid *chainhash.Hash, previousCoins map[uint32]*engine.Output) (overlay.AdmittanceInstructions, error)
// 	IdentifyNeededInputs(beef *transaction.Beef, txid *chainhash.Hash) ([]*overlay.Outpoint, error)
// 	GetDependencies() []string
// 	GetDocumentation() string
// 	GetMetaData() *overlay.MetaData
// }
