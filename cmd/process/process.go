package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/GorillaPool/go-junglebus"
	"github.com/b-open-io/bsv21-overlay/lookups"
	lookupRedis "github.com/b-open-io/bsv21-overlay/lookups/events/redis"
	storageRedis "github.com/b-open-io/bsv21-overlay/storage/redis"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/bsv21-overlay/util"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
)

var JUNGLEBUS = "https://texas1.junglebus.gorillapool.io"
var jb *junglebus.Client
var chaintracker headers_client.Client

type tokenSummary struct {
	tx   int
	out  int
	time time.Duration
}

func init() {
	godotenv.Load("../../.env")
	jb, _ = junglebus.New(
		junglebus.WithHTTP(JUNGLEBUS),
	)
	chaintracker = headers_client.Client{
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
	log.Println("Connecting to Redis", os.Getenv("REDIS"))
	if opts, err := redis.ParseURL(os.Getenv("REDIS")); err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	} else {
		rdb = redis.NewClient(opts)
	}
	// Initialize storage
	storage, err := storageRedis.NewRedisStorage(os.Getenv("REDIS"))
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer storage.Close()

	eventLookup, err := lookupRedis.NewRedisEventLookup(
		os.Getenv("REDIS"),
		storage,
	)
	bsv21Lookup := &lookups.Bsv21EventsLookup{
		EventLookup: eventLookup,
	}

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
		iter := rdb.Scan(ctx, 0, "tok:*", 0).Iterator()
		hasRows := false
		var wg sync.WaitGroup
		for iter.Next(ctx) {
			hasRows = true
			key := iter.Val()
			select {
			case <-ctx.Done():
				log.Println("Context canceled, stopping processing...")
				return
			default:
				limiter <- struct{}{}
				wg.Add(1)
				go func(key string) {
					defer func() {
						wg.Done()
						<-limiter
					}()
					parts := strings.Split(key, ":")
					tokenId := parts[1]
					// log.Println("Processing key:", tokenId)

					tm := "tm_" + tokenId
					e := engine.Engine{
						Managers: map[string]engine.TopicManager{
							tm: topics.NewBsv21ValidatedTopicManager(
								tm,
								storage,
								[]string{
									tokenId,
								},
							),
						},
						LookupServices: map[string]engine.LookupService{
							"bsv21": bsv21Lookup,
						},
						Storage:      storage,
						ChainTracker: chaintracker,
						Verbose:      false,
						PanicOnError: true,
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
							} else if tx, err := util.LoadTx(ctx, txid); err != nil {
								log.Fatalf("Failed to load transaction: %v", err)
							} else {
								beef := &transaction.Beef{
									Version:      transaction.BEEF_V2,
									Transactions: map[string]*transaction.BeefTx{},
								}
								for _, input := range tx.Inputs {
									if input.SourceTransaction, err = util.LoadTx(ctx, input.SourceTXID); err != nil {
										log.Fatalf("Failed to load source transaction: %v", err)
									} else if _, err := beef.MergeTransaction(input.SourceTransaction); err != nil {
										log.Fatalf("Failed to merge source transaction: %v", err)
									}
								}
								if _, err := beef.MergeTransaction(tx); err != nil {
									log.Fatalf("Failed to merge source transaction: %v", err)
								}

								taggedBeef := overlay.TaggedBEEF{
									Topics: []string{tm},
								}
								// log.Println("Tx Loaded", tx.TxID().String(), "in", time.Since(start))
								// logTime := time.Now()
								if taggedBeef.Beef, err = beef.AtomicBytes(txid); err != nil {
									log.Fatalf("Failed to generate BEEF: %v", err)
								} else if admit, err := e.Submit(ctx, taggedBeef, engine.SubmitModeHistorical, nil); err != nil {
									log.Fatalf("Failed to submit transaction: %v", err)
								} else {
									// log.Println("Submitted generated", tx.TxID().String(), "in", time.Since(logTime))
									// logTime = time.Now()
									if err := rdb.ZRem(ctx, key, txidStr).Err(); err != nil {
										log.Fatalf("Failed to delete from queue: %v", err)
									}
									// log.Println("Processed", txid, "in", time.Since(logTime), "as", admit[tm].OutputsToAdmit)
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
				}(key)
			}
		}
		wg.Wait()
		if !hasRows {
			time.Sleep(5 * time.Second)
		}
		// log.Println("Waiting for all goroutines to finish...")
	}

	// Close the database connection
	// sub.QueueDb.Close()
	log.Println("Application shutdown complete.")
}
