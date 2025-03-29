package lookups

import (
	"context"
	"fmt"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bitcoin-sv/go-templates/template/cosign"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

type Bsv21Lookup struct {
	EventLookup
}

func NewBsv21Lookup(storage engine.Storage, dbPath string) *Bsv21Lookup {
	evtLookup := NewEventLookup(storage, dbPath)
	l := &Bsv21Lookup{
		EventLookup: *evtLookup,
	}

	return l
}

func (l *Bsv21Lookup) OutputAdded(ctx context.Context, output *engine.Output) error {
	if b := bsv21.Decode(output.Script); b != nil {
		if b.Op == string(bsv21.OpMint) {
			b.Id = output.Outpoint.OrdinalString()
		}
		if err := l.SaveEvent(fmt.Sprintf("id:%s", b.Id), output); err != nil {
			return err
		}
		suffix := script.NewFromBytes(b.Insc.ScriptSuffix)
		c := cosign.Decode(suffix)
		if c != nil {
			if err := l.SaveEvent(fmt.Sprintf("cos:%s", c.Address), output); err != nil {
				return err
			} else if l.SaveEvent(fmt.Sprintf("cos:%s", c.Cosigner), output); err != nil {
				return err
			}
		} else if p := p2pkh.Decode(suffix, true); p != nil {
			if err := l.SaveEvent(fmt.Sprintf("p2pkh:%s", p.AddressString), output); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *Bsv21Lookup) GetDocumentation() string {
	return "BSV21 lookup"
}

func (l *Bsv21Lookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21",
	}
}
