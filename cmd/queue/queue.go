package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/GorillaPool/go-junglebus"
	"github.com/b-open-io/bsv21-overlay/util"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
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

type progress struct {
	txid string
	time time.Duration
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS signals for graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalChan
		log.Println("Received shutdown signal, cleaning up...")
		cancel()
	}()

	var rdb *redis.Client
	log.Println("Connecting to Redis", os.Getenv("REDIS"))
	if opts, err := redis.ParseURL(os.Getenv("REDIS")); err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	} else {
		rdb = redis.NewClient(opts)
	}

	for {
		start := time.Now()
		query := redis.ZRangeArgs{
			Stop:    "+inf",
			Start:   "-inf",
			Key:     "bsv21",
			ByScore: true,
			Count:   1000,
		}
		if txids, err := rdb.ZRangeArgs(ctx, query).Result(); err != nil {
			log.Fatalf("Failed to query Redis: %v", err)
		} else if len(txids) == 0 {
			time.Sleep(1 * time.Second)
		} else {
			log.Println("Processing", len(txids), "txids")
			var wg sync.WaitGroup
			limiter := make(chan struct{}, 16)
			for _, txidStr := range txids {
				wg.Add(1)
				limiter <- struct{}{}
				go func(txidStr string) {
					defer func() {
						wg.Done()
						<-limiter
					}()
					if txid, err := chainhash.NewHashFromHex(txidStr); err != nil {
						log.Fatalf("Failed to parse txid: %v", err)
					} else if tx, err := util.LoadTx(ctx, txid); err != nil {
						log.Fatalf("Failed to load tx: %v", err)
					} else {
						tokenIds := make(map[string]struct{})
						for vout, output := range tx.Outputs {
							b := bsv21.Decode(output.LockingScript)
							if b != nil {
								if b.Op == string(bsv21.OpMint) {
									b.Id = (&overlay.Outpoint{
										Txid:        *txid,
										OutputIndex: uint32(vout),
									}).OrdinalString()
								}
								tokenIds[b.Id] = struct{}{}
							}
						}
						if len(tokenIds) > 0 {
							var score float64
							if tx.MerklePath != nil {
								score = float64(tx.MerklePath.BlockHeight) * 1e9
								for _, leaf := range tx.MerklePath.Path[0] {
									if leaf.Hash != nil && leaf.Hash.Equal(*txid) {
										score += float64(leaf.Offset)
										break
									}
								}

							}
							for tokenId := range tokenIds {
								if _, err := rdb.ZAdd(ctx, "tok:"+tokenId, redis.Z{
									Score:  score,
									Member: txidStr,
								}).Result(); err != nil {
									log.Fatalf("Failed to set token in Redis: %v", err)
								}
							}
						}
						if err := rdb.ZRem(ctx, "bsv21", txidStr).Err(); err != nil {
							log.Fatalf("Failed to remove from Redis: %v", err)
						}
					}
				}(txidStr)
			}
			duration := time.Since(start)
			log.Printf("Processed %d txids in %s, %vtx/s\n", len(txids), duration, float64(len(txids))/duration.Seconds())

		}
	}

	// Close the database connection
}
