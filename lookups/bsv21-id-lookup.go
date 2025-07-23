package lookups

import (
	"context"
	"fmt"

	"github.com/b-open-io/overlay/lookup/events"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
)

type Bsv21IdLookup struct {
	events.EventLookup
}

func (l *Bsv21IdLookup) OutputAdded(ctx context.Context, outpoint *transaction.Outpoint, outputScript *script.Script, topic string, blockHeight uint32, blockIdx uint64) error {
	if b := bsv21.Decode(outputScript); b != nil {
		if b.Op == string(bsv21.OpMint) {
			b.Id = outpoint.OrdinalString()
		}
		if err := l.SaveEvent(ctx, outpoint, fmt.Sprintf("id:%s", b.Id), blockHeight, blockIdx); err != nil {
			return err
		}
	}
	return nil
}

func (l *Bsv21IdLookup) GetDocumentation() string {
	return "BSV21 lookup"
}

func (l *Bsv21IdLookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21",
	}
}
