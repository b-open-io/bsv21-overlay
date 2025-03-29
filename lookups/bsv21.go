package lookups

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bitcoin-sv/go-templates/template/cosign"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

type Question struct {
	Event string `json:"event"`
	From  struct {
		Height uint32 `json:"height"`
		Idx    uint64 `json:"idx"`
	} `json:"from"`
	Limit   int   `json:"limit"`
	Spent   *bool `json:"spent"`
	Reverse bool  `json:"rev"`
}

type Bsv21Lookup struct {
	db             *sql.DB
	insEvent       *sql.Stmt
	updSpent       *sql.Stmt
	delOutpoint    *sql.Stmt
	updBlockHeight *sql.Stmt
	storage        engine.Storage
}

func NewBsv21Lookup(storage engine.Storage, dbPath string) *Bsv21Lookup {
	var err error
	l := &Bsv21Lookup{
		storage: storage,
	}
	if l.db, err = sql.Open("sqlite3", dbPath); err != nil {
		log.Panic(err)
	} else if _, err = l.db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		log.Panic(err)
	} else if _, err = l.db.Exec("PRAGMA synchronous=NORMAL;"); err != nil {
		log.Panic(err)
	} else if _, err = l.db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		log.Panic(err)
	} else if _, err = l.db.Exec("PRAGMA temp_store=MEMORY;"); err != nil {
		log.Panic(err)
	} else if _, err = l.db.Exec("PRAGMA mmap_size=30000000000;"); err != nil {
		log.Panic(err)
	} else if _, err = l.db.Exec(`CREATE TABLE IF NOT EXISTS events(
		event TEXT, 
		outpoint TEXT,
		height INTEGER,
		idx BIGINT,
		spent BOOLEAN NOT NULL DEFAULT FALSE,
		PRIMARY KEY(outpoint, event)
	)`); err != nil {
		log.Panic(err)
	} else if _, err = l.db.Exec(`CREATE INDEX IF NOT EXISTS idx_events_event ON events(event, height, idx)`); err != nil {
		log.Panic(err)
	} else if l.insEvent, err = l.db.Prepare(`INSERT INTO events(event, outpoint, height, idx) 
		VALUES(?1, ?2, ?3, ?4)
		ON CONFLICT DO UPDATE  SET height = ?3, idx = ?4`,
	); err != nil {
		log.Panic(err)
	} else if l.updSpent, err = l.db.Prepare(`UPDATE events SET spent = TRUE WHERE outpoint = ?`); err != nil {
		log.Panic(err)
	} else if l.delOutpoint, err = l.db.Prepare(`DELETE FROM events WHERE outpoint = ?`); err != nil {
		log.Panic(err)
	} else if l.updBlockHeight, err = l.db.Prepare(`UPDATE events 
			SET height=?2, idx=?3
			WHERE outpoint=?1`); err != nil {
		log.Panic(err)
	}
	l.db.SetMaxOpenConns(1)
	return l
}

func (l *Bsv21Lookup) saveEvent(event string, output *engine.Output) error {
	_, err := l.insEvent.Exec(
		event,
		output.Outpoint.String(),
		output.BlockHeight,
		output.BlockIdx,
	)
	return err
}

func (l *Bsv21Lookup) OutputAdded(ctx context.Context, output *engine.Output) error {
	if b := bsv21.Decode(output.Script); b != nil {
		if b.Op == string(bsv21.OpMint) {
			b.Id = output.Outpoint.OrdinalString()
		}
		if err := l.saveEvent(fmt.Sprintf("id:%s", b.Id), output); err != nil {
			return err
		}
		suffix := script.NewFromBytes(b.Insc.ScriptSuffix)
		c := cosign.Decode(suffix)
		if c != nil {
			if err := l.saveEvent(fmt.Sprintf("cos:%s", c.Address), output); err != nil {
				return err
			} else if l.saveEvent(fmt.Sprintf("cos:%s", c.Cosigner), output); err != nil {
				return err
			}
		} else if p := p2pkh.Decode(suffix, true); p != nil {
			if err := l.saveEvent(fmt.Sprintf("p2pkh:%s", p.AddressString), output); err != nil {
				return err
			}
		}

		// TODO: any other events?
	}
	return nil
}

func (l *Bsv21Lookup) OutputSpent(ctx context.Context, outpoint *overlay.Outpoint, _ string) error {
	if _, err := l.updSpent.Exec(outpoint.String()); err != nil {
		return err
	}
	return nil
}

func (l *Bsv21Lookup) OutputDeleted(ctx context.Context, outpoint *overlay.Outpoint, topic string) error {
	if _, err := l.delOutpoint.Exec(outpoint.String()); err != nil {
		return err
	}
	return nil
}

func (l *Bsv21Lookup) OutputBlockHeightUpdated(ctx context.Context, outpoint *overlay.Outpoint, blockHeight uint32, blockIdx uint64) error {
	if _, err := l.updBlockHeight.Exec(outpoint.String(), blockHeight, blockIdx); err != nil {
		return err
	}
	return nil
}

func (l *Bsv21Lookup) Lookup(ctx context.Context, q *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	var sql strings.Builder
	question := &Question{}
	if err := json.Unmarshal(q.Query, question); err != nil {
		return nil, err
	}
	args := []interface{}{question.Event, question.From.Height, question.From.Idx}
	sql.WriteString(`SELECT outpoint FROM events 
		WHERE event = ?1 AND (height >= ?2 OR (height=?2 AND idx >= ?3))`)
	if question.Spent != nil {
		sql.WriteString(" AND spent = ?")
		args = append(args, *question.Spent)
	}
	if question.Reverse {
		sql.WriteString(" ORDER BY event, height DESC, idx DESC")
	} else {
		sql.WriteString(" ORDER BY event, height, idx")
	}
	if question.Limit > 0 {
		sql.WriteString(" LIMIT ?")
		args = append(args, question.Limit)
	}
	if rows, err := l.db.Query(sql.String(), args...); err != nil {
		return nil, err
	} else {
		defer rows.Close()
		answer := &lookup.LookupAnswer{
			Type: lookup.AnswerTypeOutputList,
		}
		for rows.Next() {
			var op string
			if err := rows.Scan(&op); err != nil {
				return nil, err
			}
			if outpoint, err := overlay.NewOutpointFromString(op); err != nil {
				return nil, err
			} else if outpoint != nil {
				if output, err := l.storage.FindOutput(ctx, outpoint, nil, nil, true); err != nil {
					return nil, err
				} else if output != nil {
					if beef, _, _, err := transaction.ParseBeef(output.Beef); err != nil {
						return nil, err
					} else {
						if output.AncillaryBeef != nil {
							if err = beef.MergeBeefBytes(output.AncillaryBeef); err != nil {
								return nil, err
							}
						}
						if beefBytes, err := beef.AtomicBytes(&outpoint.Txid); err != nil {
							return nil, err
						} else {
							answer.Outputs = append(answer.Outputs, &lookup.OutputListItem{
								OutputIndex: output.Outpoint.OutputIndex,
								Beef:        beefBytes,
							})
						}
					}
				}
			}
		}
		return answer, nil
	}

}

func (l *Bsv21Lookup) GetDocumentation() string {
	return "BSV21 lookup"
}

func (l *Bsv21Lookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21",
	}
}

func (l *Bsv21Lookup) Close() {
	l.db.Close()
}
