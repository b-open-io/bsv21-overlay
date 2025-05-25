package lookups

import (
	"context"
	"fmt"
	"time"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/b-open-io/overlay/lookup/events"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bitcoin-sv/go-templates/template/bsv21/ltm"
	"github.com/bitcoin-sv/go-templates/template/bsv21/pow20"
	"github.com/bitcoin-sv/go-templates/template/cosign"
	"github.com/bitcoin-sv/go-templates/template/ordlock"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

type Bsv21EventsLookup struct {
	events.RedisEventLookup
}

func NewBsv21EventsLookup(connString string, storage engine.Storage, topic string) (*Bsv21EventsLookup, error) {
	r, err := events.NewRedisEventLookup(connString, storage, topic)
	if err != nil {
		return nil, err
	}
	return &Bsv21EventsLookup{
		RedisEventLookup: *r,
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
			events = append(events, fmt.Sprintf("p2pkh:%s", p.AddressString))
		} else if c := cosign.Decode(suffix); c != nil {
			events = append(events, fmt.Sprintf("cos:%s", c.Address))
			events = append(events, fmt.Sprintf("cos:%s", c.Cosigner))
		} else if ltm := ltm.Decode(suffix); ltm != nil {
			events = append(events, fmt.Sprintf("ltm:%s", b.Id))
		} else if pow20 := pow20.Decode(suffix); pow20 != nil {
			events = append(events, fmt.Sprintf("pow20:%s", b.Id))
		} else if ordLock := ordlock.Decode(suffix); ordLock != nil {
			if ordLock.Seller != nil {
				events = append(events, fmt.Sprintf("list:%s", ordLock.Seller.AddressString))
			}
			events = append(events, fmt.Sprintf("list:%s", b.Id))
		}
		if err := l.SaveEvents(ctx, payload.Outpoint, events, blockHeight, blockIdx); err != nil {
			return err
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
