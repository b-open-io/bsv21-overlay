package lookups

import (
	"context"
	"fmt"
	"time"

	"github.com/b-open-io/overlay/lookup/events"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bitcoin-sv/go-templates/template/bsv21/ltm"
	"github.com/bitcoin-sv/go-templates/template/bsv21/pow20"
	"github.com/bitcoin-sv/go-templates/template/cosign"
	"github.com/bitcoin-sv/go-templates/template/ordlock"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

type Bsv21EventsLookup struct {
	events.MongoEventLookup
}

func NewBsv21EventsLookup(connString string, dbName string, storage engine.Storage) (*Bsv21EventsLookup, error) {
	m, err := events.NewMongoEventLookup(connString, dbName, storage)
	if err != nil {
		return nil, err
	}
	return &Bsv21EventsLookup{
		MongoEventLookup: *m,
	}, nil
}

func (l *Bsv21EventsLookup) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	blockHeight := uint32(time.Now().Unix())
	var blockIdx uint64
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}
	if tx.MerklePath != nil {
		blockHeight = tx.MerklePath.BlockHeight
		for _, pe := range tx.MerklePath.Path[0] {
			if pe.Hash.Equal(*txid) {
				blockIdx = pe.Offset
				break
			}
		}
	}
	if b := bsv21.Decode(payload.LockingScript); b != nil {
		events := make([]string, 0, 5)
		if b.Op == string(bsv21.OpMint) {
			b.Id = payload.Outpoint.OrdinalString()
			if b.Symbol != nil {
				events = append(events, fmt.Sprintf("sym:%s", *b.Symbol))
			}
		}
		events = append(events, fmt.Sprintf("id:%s", b.Id))
		suffix := script.NewFromBytes(b.Insc.ScriptSuffix)
		if p := p2pkh.Decode(suffix, true); p != nil {
			events = append(events, fmt.Sprintf("p2pkh:%s:%s", p.AddressString, b.Id))
		} else if c := cosign.Decode(suffix); c != nil {
			events = append(events, fmt.Sprintf("cos:%s:%s", c.Address, b.Id))
			// events = append(events, fmt.Sprintf("cos:%s:%s", c.Cosigner, b.Id))
		} else if ltm := ltm.Decode(suffix); ltm != nil {
			events = append(events, fmt.Sprintf("ltm:%s", b.Id))
		} else if pow20 := pow20.Decode(suffix); pow20 != nil {
			events = append(events, fmt.Sprintf("pow20:%s", b.Id))
		} else if ordLock := ordlock.Decode(suffix); ordLock != nil {
			if ordLock.Seller != nil {
				events = append(events, fmt.Sprintf("list:%s:%s", ordLock.Seller.AddressString, b.Id))
			}
			events = append(events, fmt.Sprintf("list:%s", b.Id))
		}

		// Save all events with the amount value if present
		var value any
		if b.Amt > 0 {
			value = b.Amt // Keep as uint64
		}

		for _, event := range events {
			if err := l.SaveEvent(ctx, payload.Outpoint, event, blockHeight, blockIdx, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *Bsv21EventsLookup) GetDocumentation() string {
	return "BSV21 lookup"
}

func (l *Bsv21EventsLookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21",
	}
}
