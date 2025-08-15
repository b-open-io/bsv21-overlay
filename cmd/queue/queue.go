package main

import (
	"context"
	"log"
	"math"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/b-open-io/overlay/beef"
	"github.com/b-open-io/overlay/config"
	"github.com/b-open-io/overlay/storage"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
)

var eventStorage storage.EventDataStorage
var chaintracker *headers_client.Client

func init() {
	godotenv.Load(".env")

	// Create storage using the new configuration approach
	var err error
	eventStorage, err = config.CreateEventStorage("", "", "")
	if err != nil {
		log.Fatalf("Failed to create storage: %v", err)
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

	// Storage is already initialized in init() as eventStorage

	for {
		start := time.Now()
		// Get transactions from the main queue
		members, err := eventStorage.ZRange(ctx, "bsv21", -math.MaxFloat64, math.MaxFloat64, 0, 1000)
		if err != nil {
			log.Fatalf("Failed to query Redis: %v", err)
		} else if len(members) == 0 {
			time.Sleep(1 * time.Second)
		} else {
			log.Println("Processing", len(members), "txids")
			var wg sync.WaitGroup
			limiter := make(chan struct{}, 64)
			for _, member := range members {
				txidStr := member.Member
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
								if err := eventStorage.ZAdd(ctx, "tok:"+tokenId, storage.ZMember{
									Score:  score,
									Member: txidStr,
								}); err != nil {
									log.Fatalf("Failed to set token in queue: %v", err)
								}
							}
						}
						if err := eventStorage.ZRem(ctx, "bsv21", txidStr); err != nil {
							log.Fatalf("Failed to remove from queue: %v", err)
						}
					}
				}(txidStr)
			}
			duration := time.Since(start)
			log.Printf("Processed %d txids in %s, %vtx/s\n", len(members), duration, float64(len(members))/duration.Seconds())

		}
	}

	// Close the database connection
}
