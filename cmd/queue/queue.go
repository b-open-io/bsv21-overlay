package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/GorillaPool/go-junglebus"
	"github.com/b-open-io/bsv21-overlay/lookups"
	sqlite "github.com/b-open-io/bsv21-overlay/storage"
	"github.com/b-open-io/bsv21-overlay/sub"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
)

var JUNGLEBUS = "https://texas1.junglebus.gorillapool.io"
var CACHE_DIR string
var jb *junglebus.Client
var chaintracker headers_client.Client

func init() {
	godotenv.Load("../../.env")
	jb, _ = junglebus.New(
		junglebus.WithHTTP(JUNGLEBUS),
	)
	CACHE_DIR = os.Getenv("CACHE_DIR")
	chaintracker = headers_client.Client{
		Url:    os.Getenv("BLOCK_HEADERS_URL"),
		ApiKey: os.Getenv("BLOCK_HEADERS_API_KEY"),
	}
}

func main() {
	ctx := context.Background()
	storage, err := sqlite.NewSQLiteStorage(os.Getenv("TOPIC_DB"))
	if err != nil {
		panic(err)
	}
	bsv21Lookup := lookups.NewBsv21Lookup(storage, os.Getenv("LOOKUP_DB"))

	e := engine.Engine{
		Managers: map[string]engine.TopicManager{
			"bsv21": topics.NewBsv21TopicManager(
				"bsv21",
				storage,
				[]string{
					"ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0", //MNEE
				}),
		},
		LookupServices: map[string]engine.LookupService{
			"bsv21": bsv21Lookup,
		},
		Storage:      storage,
		ChainTracker: chaintracker,
		Verbose:      true,
		PanicOnError: true,
	}

	rows, err := sub.QueueDb.Query(`SELECT txid FROM queue ORDER BY height, idx`)
	for rows.Next() {
		start := time.Now()
		var txidStr string
		if err := rows.Scan(&txidStr); err != nil {
			panic(err)
		}
		if txid, err := chainhash.NewHashFromHex(txidStr); err != nil {
			panic(err)
		} else if tx, err := loadTx(ctx, txid); err != nil {
			panic(err)
		} else {
			merklePath := tx.MerklePath
			tx.MerklePath = nil
			for _, input := range tx.Inputs {
				if input.SourceTransaction, err = loadTx(ctx, input.SourceTXID); err != nil {
					panic(err)
				}
			}
			taggedBeef := overlay.TaggedBEEF{
				Topics: []string{"bsv21"},
			}
			log.Println("Processing", txid)
			if taggedBeef.Beef, err = tx.BEEF(); err != nil {
				panic(err)
			} else if _, _, _, err := transaction.ParseBeef(taggedBeef.Beef); err != nil {
				log.Panicf("Error parsing beef %s: %x", txid, taggedBeef.Beef)
			} else if steak, err := e.Submit(ctx, taggedBeef, engine.SubmitModeHistorical, nil); err != nil {
				panic(err)
			} else if s, err := json.Marshal(steak); err != nil {
				panic(err)
			} else {
				if merklePath != nil {
					if err := e.HandleNewMerkleProof(ctx, tx.TxID(), merklePath); err != nil {
						panic(err)
					}
					log.Println("Merkle proof for", txid, "updated")
				}
				if _, err := sub.QueueDb.Exec(`DELETE FROM queue WHERE txid = ?`, txid.String()); err != nil {
					panic(err)
				}
				log.Println("Processed", txid, "in", time.Since(start), "as", string(s))
				start = time.Now()
			}
		}
	}
	sub.QueueDb.Close()
	storage.Close()
	bsv21Lookup.Close()
}

func loadTx(ctx context.Context, txid *chainhash.Hash) (*transaction.Transaction, error) {
	start := time.Now()
	txidStr := txid.String()
	if beefBytes, err := os.ReadFile(fmt.Sprintf("%s/%s.beef", CACHE_DIR, txid)); err == nil {
		if tx, err := transaction.NewTransactionFromBEEF(beefBytes); err != nil {
			log.Println("Error loading beef", err)
		} else if tx == nil {
			log.Println("Missing tx", txid)
		} else {
			return tx, nil
		}
	}
	if t, err := jb.GetTransaction(ctx, txidStr); err != nil {
		panic(err)
	} else if tx, err := transaction.NewTransactionFromBytes(t.Transaction); err != nil {
		panic(err)
	} else if resp, err := http.Get(fmt.Sprintf("%s/v1/transaction/proof/%s/bin", JUNGLEBUS, txid)); err != nil {
		panic(err)
	} else if resp.StatusCode < 300 {
		prf, _ := io.ReadAll(resp.Body)
		if merklePath, err := transaction.NewMerklePathFromBinary(prf); err != nil {
			panic(err)
		} else if root, err := merklePath.ComputeRoot(tx.TxID()); err != nil {
			panic(err)
		} else if valid, err := chaintracker.IsValidRootForHeight(root, merklePath.BlockHeight); err != nil {
			panic(err)
		} else if !valid {
			panic("invalid-merkle-path")
		} else {
			tx.MerklePath = merklePath
		}
		if beefBytes, err := tx.AtomicBEEF(false); err != nil {
			panic(err)
		} else if err := os.WriteFile(fmt.Sprintf("%s/%s.beef", CACHE_DIR, txid), beefBytes, 0644); err != nil {
			panic(err)
		}
		log.Println(txid, " loaded in ", time.Since(start))
		return tx, nil
	} else {
		return nil, errors.New("missing-tx" + txidStr)
	}
}
