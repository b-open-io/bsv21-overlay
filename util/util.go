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

	"github.com/GorillaPool/go-junglebus"
	redisStorage "github.com/b-open-io/bsv21-overlay/storage/redis"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

var JUNGLEBUS string
var jb *junglebus.Client
var chaintracker headers_client.Client
var rdb *redis.Client

func init() {
	godotenv.Load("../../.env")
	JUNGLEBUS = os.Getenv("JUNGLEBUS")
	jb, _ = junglebus.New(
		junglebus.WithHTTP(JUNGLEBUS),
	)
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

type inflightRequest struct {
	wg     *sync.WaitGroup
	result *transaction.Transaction
	err    error
}

var inflightMap sync.Map

func LoadTx(ctx context.Context, txid *chainhash.Hash) (*transaction.Transaction, error) {
	txidStr := txid.String()
	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Done()

	// Check if there's already an in-flight request for this txid
	if inflight, loaded := inflightMap.LoadOrStore(txidStr, &inflightRequest{wg: &wg}); loaded {
		req := inflight.(*inflightRequest)
		req.wg.Wait()
		return req.result, req.err
	} else {
		req := inflight.(*inflightRequest)
		req.result, req.err = fetchTransaction(ctx, txid)
		return req.result, req.err
	}
}

func fetchTransaction(ctx context.Context, txid *chainhash.Hash) (*transaction.Transaction, error) {
	// Your existing logic for fetching the transaction
	txidStr := txid.String()
	if beefBytes, err := rdb.HGet(ctx, redisStorage.BeefKey, txidStr).Bytes(); err == nil {
		if _, tx, _, err := transaction.ParseBeef(beefBytes); err == nil && tx != nil && tx.MerklePath != nil {
			return tx, nil
		}
	}

	resp, err := http.Get(fmt.Sprintf("%s/v1/transaction/beef/%s", JUNGLEBUS, txidStr))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, errors.New("missing-tx" + txidStr)
	}

	beefBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	_, tx, _, err := transaction.ParseBeef(beefBytes)
	if err != nil {
		return nil, err
	}

	if tx.MerklePath != nil {
		root, err := tx.MerklePath.ComputeRoot(txid)
		if err != nil {
			return nil, err
		}
		valid, err := chaintracker.IsValidRootForHeight(root, tx.MerklePath.BlockHeight)
		if err != nil || !valid {
			return nil, errors.New("invalid-merkle-path")
		}
	}

	if err := rdb.HSet(ctx, redisStorage.BeefKey, txidStr, beefBytes).Err(); err != nil {
		return nil, err
	}

	return tx, nil
}
