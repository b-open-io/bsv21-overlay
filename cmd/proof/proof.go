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
	"github.com/bsv-blockchain/go-sdk/overlay"
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

	limiter := make(chan struct{}, 24)
	done := make(chan *tokenSummary, 1000)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		txcount := 0
		lastTime := time.Now()
		for {
			select {
			case summary := <-done:
				txcount += summary.tx
				// log.Println("Got done")

			case <-ticker.C:
				log.Printf("Processed tx %d in %v %vtx/s\n", txcount, time.Since(lastTime), float64(txcount)/time.Since(lastTime).Seconds())
				lastTime = time.Now()
				txcount = 0
			case <-ctx.Done():
				log.Println("Context canceled, stopping processing...")
				return
			}
		}
	}()

	for {
		keys, err := rdb.Keys(ctx, "ev:id:*").Result()
		if err != nil {
			log.Fatalf("Failed to scan Redis: %v", err)
		}
		hasRows := false
		var wg sync.WaitGroup
		for _, key := range keys {
			hasRows = true
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
					tokenId := parts[2]

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
						PanicOnError: true,
					}

					// start := time.Now()
					outpoints, err := rdb.ZRangeArgs(ctx, redis.ZRangeArgs{
						Start:   0,
						Stop:    "+inf",
						Key:     key,
						ByScore: true,
						Count:   100,
						// Rev:     true,
					}).Result()
					if err != nil {
						log.Fatalf("Failed to query Redis: %v", err)
					}
					// log.Println("Processing tokenId", parts[1], len(txids), "txids")
					// admitted := 0
					// logTime := time.Now()
					for _, op := range outpoints {
						select {
						case <-ctx.Done():
							log.Println("Context canceled, stopping processing...")
							return
						default:
							// log.Println("Processing", parts[1], txidStr)
							if outpoint, err := overlay.NewOutpointFromString(op); err != nil {
								log.Fatalf("Invalid txid: %v", err)
							} else if tx, err := util.LoadTx(ctx, &outpoint.Txid); err != nil {
								log.Fatalf("Failed to load transaction: %v", err)
							} else if tx.MerklePath != nil {
								if err := e.HandleNewMerkleProof(ctx, &outpoint.Txid, tx.MerklePath); err != nil {
									log.Fatalf("Failed to handle new merkle proof: %v", err)
								}
							}
							done <- &tokenSummary{
								tx:   1,
								time: time.Since(time.Now()),
							}
						}
					}
					// duration := time.Since(logTime)
					// log.Printf("Processed %s tx %d in %v %vtx/s\n", parts[1], len(txids), duration, float64(len(txids))/duration.Seconds())
				}(key)
			}
		}
		if !hasRows {
			break
		}
		wg.Wait()
		// log.Println("Waiting for all goroutines to finish...")
	}

	// Close the database connection
	// sub.QueueDb.Close()
	log.Println("Application shutdown complete.")
}
