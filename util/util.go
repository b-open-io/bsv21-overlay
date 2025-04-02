package util

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/GorillaPool/go-junglebus"
	redisStorage "github.com/b-open-io/bsv21-overlay/storage/redis"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

var JUNGLEBUS string
var CACHE_DIR string
var jb *junglebus.Client
var chaintracker headers_client.Client
var rdb *redis.Client

func init() {
	godotenv.Load("../../.env")
	JUNGLEBUS = os.Getenv("JUNGLEBUS")
	jb, _ = junglebus.New(
		junglebus.WithHTTP(JUNGLEBUS),
	)
	CACHE_DIR = os.Getenv("CACHE_DIR")
	chaintracker = headers_client.Client{
		Url:    os.Getenv("BLOCK_HEADERS_URL"),
		ApiKey: os.Getenv("BLOCK_HEADERS_API_KEY"),
	}

	if opts, err := redis.ParseURL(os.Getenv("REDIS")); err != nil {
		log.Println("Error parsing redis URL", err)
	} else {
		rdb = redis.NewClient(opts)
	}
}

type InFlight struct {
	Result *transaction.Transaction
	Wg     sync.WaitGroup
}

var inflightMap = map[string]*InFlight{}
var inflightM sync.Mutex

func LoadTx(ctx context.Context, txid *chainhash.Hash) (tx *transaction.Transaction, err error) {
	start := time.Now()
	txidStr := txid.String()

	if beefBytes, err := rdb.HGet(ctx, redisStorage.BeefKey, txidStr).Bytes(); err == nil {
		if _, tx, txid, err = transaction.ParseBeef(beefBytes); err != nil {
			log.Println("Error loading beef", err)
		} else if tx == nil {
			log.Println("Missing tx", txidStr)
		} else if tx.MerklePath != nil {
			return tx, nil
		}
	}
	inflightM.Lock()
	inflight, ok := inflightMap[txidStr]
	if !ok {
		inflight = &InFlight{}
		inflight.Wg.Add(1)
		inflightMap[txidStr] = inflight
	}
	inflightM.Unlock()
	if ok {
		log.Println("Already inflight", txidStr)
		inflight.Wg.Wait()
		return inflight.Result, nil
	}
	return func(txidStr string) (tx *transaction.Transaction, err error) {
		// log.Println("Loading tx", txidStr)
		defer func() {
			inflight.Result = tx
			inflight.Wg.Done()
			inflightM.Lock()
			delete(inflightMap, txidStr)
			inflightM.Unlock()
		}()
		// if tx == nil {
		// 	log.Println("Loading tx from Junglebus", txidStr)
		// 	if t, err := jb.GetTransaction(ctx, txidStr); err != nil {
		// 		panic(err)
		// 	} else if tx, err = transaction.NewTransactionFromBytes(t.Transaction); err != nil {
		// 		panic(err)
		// 	}
		// }
		log.Println("Loading proof from Junglebus", txidStr)
		if resp, err := http.Get(fmt.Sprintf("%s/v5/tx/%s/beef", os.Getenv("1SAT"), txid)); err != nil {
			panic(err)
		} else if resp.StatusCode < 300 {
			beefBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				panic(err)
			} else if _, tx, txid, err = transaction.ParseBeef(beefBytes); err != nil {
				panic(err)
			} else if tx.MerklePath != nil {
				if root, err := tx.MerklePath.ComputeRoot(txid); err != nil {
					panic(err)
				} else if valid, err := chaintracker.IsValidRootForHeight(root, tx.MerklePath.BlockHeight); err != nil {
					panic(err)
				} else if !valid {
					panic("invalid-merkle-path")
				}
			}
			if err = rdb.HSet(ctx, redisStorage.BeefKey, txidStr, beefBytes).Err(); err != nil {
				panic(err)
			}
			log.Println(txid, " loaded in ", time.Since(start))
			return tx, nil
		} else {
			return nil, errors.New("missing-tx" + txidStr)
		}
	}(txidStr)
}
