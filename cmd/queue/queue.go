package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/b-open-io/overlay/beef"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
)

var beefStorage beef.BeefStorage
var chaintracker *headers_client.Client

func init() {
	godotenv.Load("../../.env")

	// Set up BEEF storage
	redisBeefURL := os.Getenv("REDIS_BEEF")
	if redisBeefURL == "" {
		redisBeefURL = os.Getenv("REDIS")
	}
	var err error
	beefStorage, err = beef.NewRedisBeefStorage(redisBeefURL, 0)
	if err != nil {
		log.Fatalf("Failed to create BEEF storage: %v", err)
	}

	// Set up chain tracker
	chaintracker = &headers_client.Client{
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
			Key:     "mnee",
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
					log.Println("Processing txid", txidStr)
					if txid, err := chainhash.NewHashFromHex(txidStr); err != nil {
						log.Fatalf("Failed to parse txid: %v", err)
					} else if tx, err := beefStorage.LoadTx(ctx, txid, chaintracker); err != nil {
						log.Fatalf("Failed to load tx: %v", err)
					} else {
						for _, input := range tx.Inputs {
							if input.SourceTransaction, err = beefStorage.LoadTx(ctx, input.SourceTXID, chaintracker); err != nil {
								log.Fatalf("Failed to load input tx: %v", err)
							}
						}
						tokenIds := make(map[string]struct{})
						for vout, output := range tx.Outputs {
							b := bsv21.Decode(output.LockingScript)
							if b != nil {
								if b.Op == string(bsv21.OpMint) {
									b.Id = (&transaction.Outpoint{
										Txid:  *txid,
										Index: uint32(vout),
									}).OrdinalString()
								}
								tokenIds[b.Id] = struct{}{}
							}
						}
						if len(tokenIds) > 0 {
							var score float64
							if tx.MerklePath != nil {
								score = float64(tx.MerklePath.BlockHeight)
								for _, leaf := range tx.MerklePath.Path[0] {
									if leaf.Hash != nil && leaf.Hash.Equal(*txid) {
										score += float64(leaf.Offset) / 1e9
										break
									}
								}
							} else {
								score = float64(time.Now().Unix())
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
						if err := rdb.ZRem(ctx, "mnee", txidStr).Err(); err != nil {
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
