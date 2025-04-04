package bap

import (
	"context"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/bitcoin-sv/go-templates/template/bitcom"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type BapTopicManager struct {
	Storage bapStorage
}

func (tm *BapTopicManager) IdentifyAdmissableOutputs(ctx context.Context, beefBytes []byte, previousCoins []uint32) (admit overlay.AdmittanceInstructions, err error) {
	_, tx, _, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return admit, err
	} else if tx == nil {
		return admit, engine.ErrInvalidBeef
	}

	for vout, output := range tx.Outputs {
		bc := bitcom.Decode(output.LockingScript)
		if bc == nil {
			continue
		}
		bap := bitcom.DecodeBAP(bc)
		if bap == nil {
			continue
		}
		if id, err := tm.Storage.FindIdentity(bap.Identity); err != nil {
			return admit, err
		} else {
			switch bap.Type {
			case bitcom.ID:
				if id == nil {
					admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
					break
				}
				for _, aip := range bitcom.DecodeAIP(bc) {
					if aip.Address == bap.Address {
						admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
						break
					}
				}
			case bitcom.ATTEST:
				if id == nil {
					break
				}
				if attest, err := tm.Storage.FindAttest(bap.Identity); err != nil {
					return admit, err
				} else if attest == nil {
					admit.OutputsToAdmit = append(admit.OutputsToAdmit, uint32(vout))
				} else {
					// for i, s := range attest.Signers {
					// 	// if s.IDKey == id.IDKey && s.Sequence <  bap.SequenceNum
					// }
				}
			}
		}

	}

	return
}

func (tm *BapTopicManager) IdentifyNeededInputs(ctx context.Context, beefBytes []byte) ([]*overlay.Outpoint, error) {
	return nil, nil
}

func (tm *BapTopicManager) GetDocumentation() string {
	return "BAP Topic Manager"
}

func (tm *BapTopicManager) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BAP",
	}
}
