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
	"github.com/b-open-io/overlay/config"
	"github.com/b-open-io/overlay/queue"
	"github.com/b-open-io/overlay/storage"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
)

var eventStorage storage.EventDataStorage
var chaintracker *headers_client.Client
var redisClient *redis.Client

func init() {
	godotenv.Load(".env")

	// Create storage using the new configuration approach
	var err error
	eventStorage, err = config.CreateEventStorage(
		os.Getenv("EVENTS_URL"), 
		os.Getenv("BEEF_URL"), 
		os.Getenv("QUEUE_URL"),
		os.Getenv("PUBSUB_URL"),
	)
	if err != nil {
		log.Fatalf("Failed to create storage: %v", err)
	}
	
	// Storage layer handles all database operations via unified interface

	// Set up chain tracker
	chaintracker = &headers_client.Client{
		Url:    os.Getenv("HEADERS_URL"),
		ApiKey: os.Getenv("HEADERS_KEY"),
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

	// Storage is already initialized in init() as eventStorage

	for {
		start := time.Now()
		// Get transactions from the main queue
		queueStore := eventStorage.GetQueueStorage()
		members, err := queueStore.ZRangeByScore(ctx, "bsv21", -1e9, 1e9, 0, 1000)
		if err != nil {
			log.Fatalf("Failed to query Redis: %v", err)
		} else if len(members) == 0 {
			time.Sleep(1 * time.Second)
		} else {
			log.Println("Processing", len(members), "txids")
			var wg sync.WaitGroup
			limiter := make(chan struct{}, 64)
			for _, member := range members {
				wg.Add(1)
				limiter <- struct{}{}
				go func(z queue.ScoredMember) {
					txidStr := z.Member
					score := z.Score
					defer func() {
						wg.Done()
						<-limiter
					}()
					log.Println("Processing txid", txidStr)
					if txid, err := chainhash.NewHashFromHex(txidStr); err != nil {
						log.Fatalf("Failed to parse txid: %v", err)
					} else if tx, err := beef.LoadTx(ctx, eventStorage.GetBeefStorage(), txid, chaintracker); err != nil {
						log.Fatalf("Failed to load tx: %v", err)
					} else {
						for _, input := range tx.Inputs {
							if input.SourceTransaction, err = beef.LoadTx(ctx, eventStorage.GetBeefStorage(), input.SourceTXID, chaintracker); err != nil {
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
							// Use the score from the original queue entry
							// It already contains the block height + index information
							for tokenId := range tokenIds {
								if err := queueStore.ZAdd(ctx, "tok:"+tokenId, queue.ScoredMember{
									Score:  score,
									Member: txidStr,
								}); err != nil {
									log.Fatalf("Failed to set token in queue: %v", err)
								}
							}
						}
						if err := queueStore.ZRem(ctx, "bsv21", txidStr); err != nil {
							log.Fatalf("Failed to remove from queue: %v", err)
						}
					}
				}(member)
			}
			duration := time.Since(start)
			log.Printf("Processed %d txids in %s, %vtx/s\n", len(members), duration, float64(len(members))/duration.Seconds())

		}
	}

	// Close the database connection
}
