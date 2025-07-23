package main

import (
	"context"
	"log"
	"os"
	"sync"

	"github.com/b-open-io/bsv21-overlay/sub"
	"github.com/b-open-io/overlay/beef"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/joho/godotenv"
)

func main() {
	var alreadyLoaded sync.Map
	ctx := context.Background()

	// Load environment and set up connections
	godotenv.Load("../../.env")

	// Set up Redis client
	rdb := sub.GetRedisClient()
	defer rdb.Close()

	// Set up BEEF storage
	redisBeefURL := os.Getenv("REDIS_BEEF")
	if redisBeefURL == "" {
		redisBeefURL = os.Getenv("REDIS")
	}
	beefStorage, err := beef.NewRedisBeefStorage(redisBeefURL, 0)
	if err != nil {
		log.Fatalf("Failed to create BEEF storage: %v", err)
	}

	// Set up chain tracker
	chaintracker := &headers_client.Client{
		Url:    os.Getenv("BLOCK_HEADERS_URL"),
		ApiKey: os.Getenv("BLOCK_HEADERS_API_KEY"),
	}

	// Get all transaction IDs from Redis queue (bsv21 sorted set)
	txids, err := rdb.ZRange(ctx, "mnee", 0, -1).Result()
	if err != nil {
		panic(err)
	}

	limiter := make(chan struct{}, 10)
	var wg sync.WaitGroup

	for _, txidStr := range txids {
		if _, loaded := alreadyLoaded.LoadOrStore(txidStr, struct{}{}); loaded {
			continue
		}
		wg.Add(1)
		limiter <- struct{}{}
		go func(txidStr string) {
			defer wg.Done()
			defer func() { <-limiter }()

			if txid, err := chainhash.NewHashFromHex(txidStr); err != nil {
				panic(err)
			} else if tx, err := beefStorage.LoadTx(ctx, txid, chaintracker); err != nil {
				panic(err)
			} else {
				for _, input := range tx.Inputs {
					inputTxidStr := input.SourceTXID.String()
					if _, loaded := alreadyLoaded.LoadOrStore(inputTxidStr, struct{}{}); loaded {
						continue
					}
					if _, err = beefStorage.LoadTx(ctx, input.SourceTXID, chaintracker); err != nil {
						panic(err)
					}
				}
			}
		}(txidStr)
	}
	wg.Wait()
}
