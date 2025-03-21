package topics

import (
	"github.com/4chain-ag/go-overlay-services/pkg/engine"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
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

type tokenSummary struct {
	tokensIn  uint64
	tokensOut uint64
	vouts     []uint32
	deploy    bool
}

func (tm *bsv21TopicManager) IdentifyAdmissableOutputs(tx *transaction.Transaction, loadInput func(uint32) (*engine.Output, error)) (result engine.TopicResult, err error) {
	admit := &result.Admit

	summary := make(map[string]*tokenSummary)
	for vout, output := range tx.Outputs {
		if b := bsv21.Decode(output.LockingScript); b != nil {
			if !tm.HasTokenId(b.Id) {
				continue
			}

			if b.Op == string(bsv21.OpMint) {
				admit.CoinsToRetain = append(admit.CoinsToRetain, uint32(vout))
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
		for vin, _ := range tx.Inputs {
			admit.CoinsToRetain = append(admit.CoinsToRetain, uint32(vin))
			if output, err := loadInput(uint32(vin)); err != nil {
				return result, err
			} else if output == nil {
				return result, engine.ErrMissingInput
			} else {
				if b := bsv21.Decode(output.Script); b != nil {
					if !tm.HasTokenId(b.Id) {
						continue
					}

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
				admit.OutputsToAdmit = append(admit.CoinsToRetain, token.vouts...)
			}
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
