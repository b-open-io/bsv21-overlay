package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/b-open-io/bsv21-overlay/lookups"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/overlay/config"
	"github.com/b-open-io/overlay/storage"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
)

var store engine.Storage
var chaintracker *headers_client.Client

type tokenSummary struct {
	tx   int
	out  int
	time time.Duration
}

func init() {
	godotenv.Load(".env")

	// Create storage using the new configuration approach
	var err error
	store, err = config.CreateEventStorage("", "", "")
	if err != nil {
		log.Fatalf("Failed to create storage: %v", err)
	}

	// Set up chain tracker
	chaintracker = &headers_client.Client{
		Url:    os.Getenv("BLOCK_HEADERS_URL"),
		ApiKey: os.Getenv("BLOCK_HEADERS_API_KEY"),
	}
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
	log.Println("Connecting to Redis", os.Getenv("REDIS_URL"))
	if opts, err := redis.ParseURL(os.Getenv("REDIS_URL")); err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	} else {
		rdb = redis.NewClient(opts)
	}
	defer func() {
		if rdb != nil {
			if err := rdb.Close(); err != nil {
				log.Printf("Error closing Redis connection: %v", err)
			}
		}
	}()

	// Define whitelist key
	const whitelistKey = "bsv21:whitelist"

	// Log current whitelist
	if tokens, err := rdb.SMembers(ctx, whitelistKey).Result(); err == nil {
		log.Printf("Token whitelist: %v", tokens)
	} else {
		log.Printf("Failed to get token whitelist: %v", err)
	}

	// Storage is already initialized in init()
	// Cast store to EventDataStorage for the lookup
	eventStorage, ok := store.(storage.EventDataStorage)
	if !ok {
		log.Fatalf("Storage does not implement EventDataStorage interface")
	}

	bsv21Lookup, err := lookups.NewBsv21EventsLookup(eventStorage)
	if err != nil {
		log.Fatalf("Failed to initialize bsv21 lookup: %v", err)
	}
	defer func() {
		// Bsv21EventsLookup doesn't need closing - storage is closed elsewhere
		log.Println("Application shutdown complete.")
	}()

	limiter := make(chan struct{}, 16)
	done := make(chan *tokenSummary, 1000)
	go func() {
		ticker := time.NewTicker(time.Minute)
		txcount := 0
		outcount := 0
		// accTime
		lastTime := time.Now()
		for {
			select {
			case summary := <-done:
				txcount += summary.tx
				outcount += summary.out
				// log.Println("Got done")

			case <-ticker.C:
				log.Printf("Processed tx %d o %d in %v %vtx/s\n", txcount, outcount, time.Since(lastTime), float64(txcount)/time.Since(lastTime).Seconds())
				lastTime = time.Now()
				txcount = 0
				outcount = 0
			case <-ctx.Done():
				log.Println("Context canceled, stopping processing...")
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			log.Println("Context canceled, exiting main loop...")
			return
		default:
		}

		// Get whitelisted tokens
		whitelistedTokens, err := rdb.SMembers(ctx, whitelistKey).Result()
		if err != nil {
			log.Printf("Failed to get whitelisted tokens: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		hasRows := false
		var wg sync.WaitGroup

		// Process each whitelisted token
		for _, tokenId := range whitelistedTokens {
			key := "tok:" + tokenId

			// Check if this token has any transactions to process
			exists, err := rdb.Exists(ctx, key).Result()
			if err != nil {
				log.Printf("Failed to check existence of key %s: %v", key, err)
				continue
			}
			if exists == 0 {
				// No transactions for this token
				continue
			}

			hasRows = true
			select {
			case <-ctx.Done():
				log.Println("Context canceled, waiting for active goroutines...")
				wg.Wait()
				return
			default:
				limiter <- struct{}{}
				wg.Add(1)
				go func(tokenId string, key string) {
					defer func() {
						wg.Done()
						<-limiter
					}()

					// log.Println("Processing key:", tokenId)

					tm := "tm_" + tokenId
					e := engine.Engine{
						Managers: map[string]engine.TopicManager{
							tm: topics.NewBsv21ValidatedTopicManager(
								tm,
								store,
								[]string{
									tokenId,
								},
							),
						},
						LookupServices: map[string]engine.LookupService{
							"bsv21": bsv21Lookup,
						},
						Storage:      store,
						ChainTracker: chaintracker,
					}

					// start := time.Now()
					txids, err := rdb.ZRangeArgs(ctx, redis.ZRangeArgs{
						Stop:    "+inf",
						Start:   "-inf",
						Key:     key,
						ByScore: true,
						// Count:   1000,
					}).Result()
					if err != nil {
						log.Fatalf("Failed to query Redis: %v", err)
					}
					// log.Println("Processing tokenId", parts[1], len(txids), "txids")
					logTime := time.Now()
					for _, txidStr := range txids {
						select {
						case <-ctx.Done():
							log.Println("Context canceled, stopping processing...")
							return
						default:
							// log.Println("Processing", parts[1], txidStr)
							if txid, err := chainhash.NewHashFromHex(txidStr); err != nil {
								log.Fatalf("Invalid txid: %v", err)
							} else if tx, err := eventStorage.GetBeefStorage().LoadTx(ctx, txid, chaintracker); err != nil {
								log.Fatalf("Failed to load transaction: %v", err)
							} else {
								beefDoc := &transaction.Beef{
									Version:      transaction.BEEF_V2,
									Transactions: map[chainhash.Hash]*transaction.BeefTx{},
								}
								for _, input := range tx.Inputs {
									if input.SourceTransaction, err = eventStorage.GetBeefStorage().LoadTx(ctx, input.SourceTXID, chaintracker); err != nil {
										log.Fatalf("Failed to load source transaction: %v", err)
									} else if _, err := beefDoc.MergeTransaction(input.SourceTransaction); err != nil {
										log.Fatalf("Failed to merge source transaction: %v", err)
									}
								}
								if _, err := beefDoc.MergeTransaction(tx); err != nil {
									log.Fatalf("Failed to merge source transaction: %v", err)
								}

								taggedBeef := overlay.TaggedBEEF{
									Topics: []string{tm},
								}
								// log.Println("Tx Loaded", tx.TxID().String(), "in", time.Since(start))
								logTime := time.Now()
								if taggedBeef.Beef, err = beefDoc.AtomicBytes(txid); err != nil {
									log.Fatalf("Failed to generate BEEF: %v", err)
								} else if admit, err := e.Submit(ctx, taggedBeef, engine.SubmitModeHistorical, nil); err != nil {
									log.Fatalf("Failed to submit transaction: %v", err)
								} else {
									// log.Println("Submitted generated", tx.TxID().String(), "in", time.Since(logTime))
									// logTime = time.Now()
									if err := rdb.ZRem(ctx, key, txidStr).Err(); err != nil {
										log.Fatalf("Failed to delete from queue: %v", err)
									}
									log.Println("Processed", txid, "in", time.Since(logTime), "as", admit[tm].OutputsToAdmit)
									done <- &tokenSummary{
										tx:  1,
										out: len(admit[tm].OutputsToAdmit),
									}
									// start = time.Now()
								}
							}
						}
					}
					duration := time.Since(logTime)
					log.Printf("Processed %s tx %d in %v %vtx/s\n", tokenId, len(txids), duration, float64(len(txids))/duration.Seconds())
				}(tokenId, key)
			}
		}
		wg.Wait()
		if !hasRows {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				// Continue to next iteration
			}
		}
		// log.Println("Waiting for all goroutines to finish...")
	}
}
