package bap

import (
	"context"

	"github.com/b-open-io/bsv21-overlay/lookups/events"
	"github.com/bitcoin-sv/go-templates/template/bitcom"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/script"
)

type BapLookup struct {
	events.EventLookup
}

func (l *BapLookup) OutputAdded(ctx context.Context, outpoint *overlay.Outpoint, outputScript *script.Script, topic string, blockHeight uint32, blockIdx uint64) error {
	if bc := bitcom.Decode(outputScript); bc != nil {
		if bap := bitcom.DecodeBAP(bc); bap != nil {
			switch bap.Type {

			}
		}
	}
	return nil
}

func (l *BapLookup) GetDocumentation() string {
	return "BAP lookup"
}

func (l *BapLookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BAP",
	}
}
