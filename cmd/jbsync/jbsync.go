package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/GorillaPool/go-junglebus"
	"github.com/GorillaPool/go-junglebus/models"
	"github.com/b-open-io/bsv21-overlay/lookups"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/overlay/beef"
	"github.com/b-open-io/overlay/config"
	"github.com/b-open-io/overlay/storage"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

// Package-level variables for shared services
var (
	// Configuration
	topicID   string
	fromBlock uint64 = 811302
	fromPage  uint64 = 0

	// Services
	rdb          *redis.Client
	jbClient     *junglebus.Client
	chaintracker *headers_client.Client
	eventStorage storage.EventDataStorage
	beefStorage  beef.BeefStorage
	bsv21Lookup  *lookups.Bsv21EventsLookup

	// Engine cache for token processors
	engineCache = make(map[string]*engine.Engine)

	// Metrics
	metricsDone    chan *tokenSummary
	metricsLock    sync.Mutex
	txProcessed    int
	outProcessed   int
	lastMetricTime time.Time
)

type tokenSummary struct {
	tx   int
	out  int
	time time.Duration
}

var (
	// Debug flags to control which components run
	runJungleBus     bool
	runQueueProc     bool
	runTokenProc     bool
	queueConcurrency int
	tokenConcurrency int
)

func init() {
	// Command line flags
	var envFile string
	flag.StringVar(&topicID, "topic", "", "Topic ID to subscribe to (required)")
	flag.StringVar(&envFile, "env", ".env", "Path to .env file")

	// Debug flags to enable/disable components
	flag.BoolVar(&runJungleBus, "jb", true, "Enable JungleBus subscriber")
	flag.BoolVar(&runQueueProc, "queue", true, "Enable queue processor")
	flag.BoolVar(&runTokenProc, "token", true, "Enable token processor")
	flag.IntVar(&queueConcurrency, "queue-limit", 8, "Queue processor concurrency limit")
	flag.IntVar(&tokenConcurrency, "token-limit", 16, "Token processor concurrency limit")
	flag.Parse()

	// Load environment configuration
	godotenv.Load(envFile)

	// Check topic ID
	if topicID == "" {
		topicID = os.Getenv("BSV21_TOPIC")
		if topicID == "" {
			log.Fatal("Topic ID is required. Use -topic flag or set BSV21_TOPIC environment variable")
		}
	}

	// Set up Redis connection
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	}
	rdb = redis.NewClient(opts)

	// Set up chain tracker
	chaintracker = &headers_client.Client{
		Url:    os.Getenv("BLOCK_HEADERS_URL"),
		ApiKey: os.Getenv("BLOCK_HEADERS_API_KEY"),
	}

	// Set up JungleBus connection
	jungleBusURL := os.Getenv("JUNGLEBUS")
	if jungleBusURL == "" {
		log.Fatalf("JUNGLEBUS environment variable is required")
	}
	jbClient, err = junglebus.New(junglebus.WithHTTP(jungleBusURL))
	if err != nil {
		log.Fatalf("Failed to create JungleBus client: %v", err)
	}

	// Create storage using the configuration approach
	eventStorage, err = config.CreateEventStorage("", "", "")
	if err != nil {
		log.Fatalf("Failed to create storage: %v", err)
	}

	// Initialize BSV21 lookup
	bsv21Lookup, err = lookups.NewBsv21EventsLookup(eventStorage)
	if err != nil {
		log.Fatalf("Failed to initialize BSV21 lookup: %v", err)
	}

	// Get BeefStorage from eventStorage
	beefStorage = eventStorage.GetBeefStorage()

	// Initialize metrics
	metricsDone = make(chan *tokenSummary, 1000)
	lastMetricTime = time.Now()
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS signals for graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	shutdownComplete := make(chan struct{})
	go func() {
		<-signalChan
		log.Println("Received shutdown signal, initiating graceful shutdown...")
		cancel()

		// Give processes 10 seconds to shut down gracefully
		select {
		case <-shutdownComplete:
			log.Println("Graceful shutdown completed")
		case <-time.After(10 * time.Second):
			log.Println("Shutdown timeout exceeded, forcing exit")
			os.Exit(1)
		}
	}()

	// Log initial whitelist
	const whitelistKey = "bsv21:whitelist"
	if tokens, err := rdb.SMembers(ctx, whitelistKey).Result(); err == nil {
		log.Printf("Token whitelist: %v", tokens)
	}

	// Log configuration
	log.Printf("Starting jbsync with configuration:")
	log.Printf("  JungleBus: %v", runJungleBus)
	log.Printf("  Queue Processor: %v (concurrency: %d)", runQueueProc, queueConcurrency)
	log.Printf("  Token Processor: %v (concurrency: %d)", runTokenProc, tokenConcurrency)

	var wg sync.WaitGroup

	// Always start metrics collector
	wg.Add(1)
	go func() {
		defer wg.Done()
		metricsCollector(ctx)
	}()

	// Conditionally start queue processor
	if runQueueProc {
		wg.Add(1)
		go func() {
			defer wg.Done()
			queueProcessor(ctx)
		}()
	} else {
		log.Println("Queue processor disabled")
	}

	// Conditionally start token processor
	if runTokenProc {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tokenProcessor(ctx)
		}()
	} else {
		log.Println("Token processor disabled")
	}

	// Conditionally start JungleBus subscription
	if runJungleBus {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := jungleBusSubscriber(ctx); err != nil {
				log.Printf("JungleBus subscriber error: %v", err)
			}
		}()
	} else {
		log.Println("JungleBus subscriber disabled")
	}

	// Wait for all goroutines to finish
	wg.Wait()

	// Signal that shutdown is complete
	close(shutdownComplete)

	// Close connections
	if rdb != nil {
		if err := rdb.Close(); err != nil {
			log.Printf("Error closing Redis connection: %v", err)
		}
	}
	log.Println("Shutdown complete")
}

// jungleBusSubscriber handles JungleBus subscription (Stage 1)
func jungleBusSubscriber(ctx context.Context) error {
	// Check for existing progress
	startBlock := fromBlock
	if progress, err := rdb.HGet(ctx, "progress", topicID).Int(); err == nil {
		startBlock = uint64(progress)
		log.Printf("Resuming %s from block %d", topicID, startBlock)
	}

	txcount := 0
	log.Printf("Starting JungleBus subscription for topic: %s from block %d", topicID, startBlock)

	// Create subscription
	sub, err := jbClient.SubscribeWithQueue(ctx,
		topicID,
		startBlock,
		fromPage,
		junglebus.EventHandler{
			OnTransaction: func(txn *models.TransactionResponse) {
				txcount++
				log.Printf("[TX]: %d - %d: %d %s", txn.BlockHeight, txn.BlockIndex, len(txn.Transaction), txn.Id)

				// Add to Redis queue with score based on block height and index
				if err := rdb.ZAdd(ctx, "bsv21", redis.Z{
					Member: txn.Id,
					Score:  float64(txn.BlockHeight) + float64(txn.BlockIndex)/1e9,
				}).Err(); err != nil {
					log.Printf("Failed to add transaction to queue: %v", err)
				}
			},
			OnStatus: func(status *models.ControlResponse) {
				log.Printf("[STATUS]: %d %v %d processed", status.StatusCode, status.Message, txcount)
				switch status.StatusCode {
				case 200:
					// Update progress
					if err := rdb.HSet(ctx, "progress", topicID, status.Block+1).Err(); err != nil {
						log.Printf("Failed to update progress: %v", err)
					}
					txcount = 0
				case 999:
					log.Println(status.Message)
					log.Println("Subscription completed")
					return
				}
			},
			OnError: func(err error) {
				log.Printf("[ERROR]: %v", err)
			},
		},
		&junglebus.SubscribeOptions{
			QueueSize: 10000000,
			LiteMode:  true,
		},
	)

	if err != nil {
		return err
	}

	// Wait for context cancellation
	<-ctx.Done()
	if sub != nil {
		sub.Unsubscribe()
	}
	return nil
}

// queueProcessor processes transactions from the main queue and sorts by tokenId (Stage 2)
func queueProcessor(ctx context.Context) {
	limiter := make(chan struct{}, queueConcurrency) // Concurrent BEEF downloads

	for {
		select {
		case <-ctx.Done():
			log.Println("Queue processor shutting down...")
			return
		default:
		}

		// Process batch from main queue
		start := time.Now()
		query := redis.ZRangeArgs{
			Stop:    "+inf",
			Start:   "-inf",
			Key:     "bsv21",
			ByScore: true,
			Count:   1000,
		}

		// Get transactions with their scores
		txidsWithScores, err := rdb.ZRangeArgsWithScores(ctx, query).Result()
		if err != nil {
			log.Printf("Failed to query Redis: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
				continue
			}
		}

		if len(txidsWithScores) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
				continue
			}
		}

		log.Printf("Queue processor: Processing %d txids", len(txidsWithScores))
		var wg sync.WaitGroup

		// Collect token operations grouped by tokenId
		var tokenOpsMutex sync.Mutex
		tokenQueues := make(map[string][]redis.Z)
		txidsToRemove := make([]interface{}, 0)

		for _, z := range txidsWithScores {
			wg.Add(1)
			limiter <- struct{}{}
			go func(txidStr string, score float64) {
				defer func() {
					wg.Done()
					<-limiter
				}()

				txid, err := chainhash.NewHashFromHex(txidStr)
				if err != nil {
					log.Printf("Failed to parse txid: %v", err)
					return
				}

				tx, err := beefStorage.LoadTx(ctx, txid, chaintracker)
				if err != nil {
					log.Printf("Failed to load tx %s: %v", txidStr, err)
					return
				}

				// Load inputs
				for _, input := range tx.Inputs {
					if input.SourceTransaction, err = beefStorage.LoadTx(ctx, input.SourceTXID, chaintracker); err != nil {
						log.Printf("Failed to load input tx: %v", err)
						return
					}
				}

				// Parse BSV21 tokens
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
					// Group operations by tokenId directly
					tokenOpsMutex.Lock()
					for tokenId := range tokenIds {
						key := "tok:" + tokenId
						tokenQueues[key] = append(tokenQueues[key], redis.Z{
							Score:  score,
							Member: txidStr,
						})
					}
					txidsToRemove = append(txidsToRemove, txidStr)
					tokenOpsMutex.Unlock()
				} else {
					// No BSV21 tokens found, still need to remove from queue
					tokenOpsMutex.Lock()
					txidsToRemove = append(txidsToRemove, txidStr)
					tokenOpsMutex.Unlock()
				}
			}(z.Member.(string), z.Score)
		}

		// Create a channel to signal when all goroutines are done
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		// Wait for either completion or context cancellation
		select {
		case <-done:
			// All goroutines completed normally
		case <-ctx.Done():
			// Context cancelled, but still wait briefly for goroutines to finish
			go func() {
				time.Sleep(1 * time.Second)
				log.Println("Queue processor: Some goroutines may not have completed")
			}()
			return
		}

		// Execute all token queue operations in batch
		// This ensures transactions are added in the correct order
		for key, members := range tokenQueues {
			if err := rdb.ZAdd(ctx, key, members...).Err(); err != nil {
				log.Printf("Failed to add to token queue %s: %v", key, err)
			}
		}

		// Remove processed transactions from main queue
		if len(txidsToRemove) > 0 {
			if err := rdb.ZRem(ctx, "bsv21", txidsToRemove...).Err(); err != nil {
				log.Printf("Failed to remove from main queue: %v", err)
			}
		}

		duration := time.Since(start)
		log.Printf("Queue processor: Processed %d txids in %s, %.2ftx/s", len(txidsWithScores), duration, float64(len(txidsWithScores))/duration.Seconds())
	}
}

// tokenProcessor processes token-specific queues (Stage 3)
func tokenProcessor(ctx context.Context) {
	const whitelistKey = "bsv21:whitelist"
	limiter := make(chan struct{}, tokenConcurrency) // Max concurrent token processors

	for {
		select {
		case <-ctx.Done():
			log.Println("Token processor shutting down...")
			return
		default:
		}

		// Get whitelisted tokens
		whitelistedTokens, err := rdb.SMembers(ctx, whitelistKey).Result()
		if err != nil {
			log.Printf("Failed to get whitelisted tokens: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		// Create engines for any new whitelisted tokens
		for _, tokenId := range whitelistedTokens {
			if _, exists := engineCache[tokenId]; !exists {
				// Create engine for this token
				tm := "tm_" + tokenId
				engineCache[tokenId] = &engine.Engine{
					Managers: map[string]engine.TopicManager{
						tm: topics.NewBsv21ValidatedTopicManager(
							tm,
							eventStorage,
							[]string{tokenId},
						),
					},
					LookupServices: map[string]engine.LookupService{
						"bsv21": bsv21Lookup,
					},
					Storage:      eventStorage,
					ChainTracker: chaintracker,
				}
				log.Printf("Created engine for token %s", tokenId)
			}
		}

		hasWork := false
		var wg sync.WaitGroup

		// Process each whitelisted token
		for _, tokenId := range whitelistedTokens {
			key := "tok:" + tokenId

			// Check if this token has any transactions
			exists, err := rdb.Exists(ctx, key).Result()
			if err != nil || exists == 0 {
				continue
			}

			hasWork = true
			limiter <- struct{}{}
			wg.Add(1)

			go func(tokenId string, key string) {
				defer func() {
					wg.Done()
					<-limiter
				}()

				processToken(ctx, tokenId, key)
			}(tokenId, key)
		}

		// Create a channel to signal when all goroutines are done
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		// Wait for either completion or context cancellation
		select {
		case <-done:
			// All goroutines completed normally
		case <-ctx.Done():
			log.Println("Token processor: Shutting down, some tokens may not have completed")
			return
		}

		if !hasWork {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				// Continue to next iteration
			}
		}
	}
}

// processToken processes all transactions for a specific token in order
func processToken(ctx context.Context, tokenId string, key string) {
	// Get engine from cache
	e, exists := engineCache[tokenId]
	if !exists {
		log.Printf("Error: no engine found for token %s", tokenId)
		return
	}

	tm := "tm_" + tokenId

	// Get all transactions for this token (in order)
	txids, err := rdb.ZRangeArgs(ctx, redis.ZRangeArgs{
		Stop:    "+inf",
		Start:   "-inf",
		Key:     key,
		ByScore: true,
	}).Result()
	if err != nil {
		log.Printf("Failed to query token queue %s: %v", tokenId, err)
		return
	}

	if len(txids) == 0 {
		return
	}

	log.Printf("Processing token %s: %d transactions", tokenId, len(txids))
	startTime := time.Now()

	// Process each transaction sequentially for this token
	for _, txidStr := range txids {
		select {
		case <-ctx.Done():
			return
		default:
		}

		txid, err := chainhash.NewHashFromHex(txidStr)
		if err != nil {
			log.Printf("Invalid txid %s: %v", txidStr, err)
			continue
		}

		tx, err := beefStorage.LoadTx(ctx, txid, chaintracker)
		if err != nil {
			log.Printf("Failed to load transaction %s: %v", txidStr, err)
			continue
		}

		// Build BEEF document
		beefDoc := &transaction.Beef{
			Version:      transaction.BEEF_V2,
			Transactions: map[chainhash.Hash]*transaction.BeefTx{},
		}

		// Add input transactions
		for _, input := range tx.Inputs {
			if input.SourceTransaction, err = beefStorage.LoadTx(ctx, input.SourceTXID, chaintracker); err != nil {
				log.Printf("Failed to load source transaction: %v", err)
				continue
			}
			if _, err := beefDoc.MergeTransaction(input.SourceTransaction); err != nil {
				log.Printf("Failed to merge source transaction: %v", err)
				continue
			}
		}

		// Add main transaction
		if _, err := beefDoc.MergeTransaction(tx); err != nil {
			log.Printf("Failed to merge transaction: %v", err)
			continue
		}

		// Create tagged BEEF
		taggedBeef := overlay.TaggedBEEF{
			Topics: []string{tm},
		}

		taggedBeef.Beef, err = beefDoc.AtomicBytes(txid)
		if err != nil {
			log.Printf("Failed to generate BEEF: %v", err)
			continue
		}

		// Submit to engine
		admit, err := e.Submit(ctx, taggedBeef, engine.SubmitModeHistorical, nil)
		if err != nil {
			log.Printf("Failed to submit transaction %s: %v", txidStr, err)
			continue
		}

		// Remove from queue
		if err := rdb.ZRem(ctx, key, txidStr).Err(); err != nil {
			log.Printf("Failed to remove %s from queue: %v", txidStr, err)
		}

		log.Printf("Processed %s for token %s: %d outputs", txid, tokenId, len(admit[tm].OutputsToAdmit))

		// Send metrics
		metricsDone <- &tokenSummary{
			tx:  1,
			out: len(admit[tm].OutputsToAdmit),
		}
	}

	duration := time.Since(startTime)
	log.Printf("Completed token %s: %d tx in %v (%.2ftx/s)", tokenId, len(txids), duration, float64(len(txids))/duration.Seconds())
}

// metricsCollector collects and reports processing metrics
func metricsCollector(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case summary := <-metricsDone:
			metricsLock.Lock()
			txProcessed += summary.tx
			outProcessed += summary.out
			metricsLock.Unlock()

		case <-ticker.C:
			metricsLock.Lock()
			if txProcessed > 0 {
				elapsed := time.Since(lastMetricTime)
				log.Printf("Metrics: %d tx, %d outputs in %v (%.2ftx/s)",
					txProcessed, outProcessed, elapsed,
					float64(txProcessed)/elapsed.Seconds())
				txProcessed = 0
				outProcessed = 0
				lastMetricTime = time.Now()
			}
			metricsLock.Unlock()

		case <-ctx.Done():
			log.Println("Metrics collector shutting down...")
			return
		}
	}
}
