package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/b-open-io/bsv21-overlay/lookups"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/overlay/config"
	"github.com/b-open-io/overlay/pubsub"
	"github.com/b-open-io/overlay/storage"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-overlay-services/pkg/server"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/broadcaster"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

var chaintracker *headers_client.Client
var PORT int
var SYNC bool
var redisClient *redis.Client                       // Redis client for pub/sub and queue operations
var redisPubSub *pubsub.RedisPubSub                 // Redis pub/sub handler
var sseSync *pubsub.SSESync                         // Centralized SSE sync manager
var e *engine.Engine

// Configuration from flags/env
var (
	eventsURL string
	beefURL   string
	redisURL  string
)

func init() {
	godotenv.Load(".env")

	// Set up chain tracker
	chaintracker = &headers_client.Client{
		Url:    os.Getenv("HEADERS_URL"),
		ApiKey: os.Getenv("HEADERS_KEY"),
	}

	// Parse PORT from env before flags
	PORT, _ = strconv.Atoi(os.Getenv("PORT"))

	// Define command-line flags with env var defaults
	flag.IntVar(&PORT, "p", PORT, "Port to listen on")
	flag.BoolVar(&SYNC, "s", false, "Start sync")
	flag.StringVar(&eventsURL, "events", os.Getenv("EVENTS_URL"), "Event storage URL")
	flag.StringVar(&beefURL, "beef", os.Getenv("BEEF_URL"), "BEEF storage URL")
	flag.StringVar(&redisURL, "redis", os.Getenv("REDIS_URL"), "Redis URL for pub/sub and queues")
	flag.Parse()
	// Apply defaults
	if PORT == 0 {
		PORT = 3000
	}
	if beefURL == "" {
		beefURL = "./beef_storage"
	}

	// Set up Redis client (if URL is provided)
	if redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			log.Fatalf("Failed to parse Redis URL: %v", err)
		}
		redisClient = redis.NewClient(opts)

		// Test connection
		ctx := context.Background()
		if err := redisClient.Ping(ctx).Err(); err != nil {
			log.Fatalf("Failed to connect to Redis: %v", err)
		}
	}
	// Initialize Redis pub/sub if configured
	if redisURL != "" {
		var err error
		redisPubSub, err = pubsub.NewRedisPubSub(redisURL)
		if err != nil {
			log.Fatalf("Failed to initialize Redis pub/sub: %v", err)
		}
	}
}

// broadcastMessages handles real-time event broadcasting using RedisPubSub
func broadcastMessages(ctx context.Context) {
	if redisPubSub == nil {
		log.Println("No Redis pub/sub configured, real-time broadcasting disabled")
		return
	}

	// Start broadcasting Redis messages to SSE clients
	if err := redisPubSub.StartBroadcasting(ctx); err != nil {
		log.Printf("Redis broadcasting error: %v", err)
	}
}

// startSSESync starts centralized SSE sync with configured peers
func startSSESync(ctx context.Context, tokens []string) {
	// Build peer-to-topics mapping from .env TOPIC_PEERS configuration
	peerTopics := make(map[string][]string)
	
	// Parse TOPIC_PEERS JSON for topic-specific peer configuration
	if topicPeersJSON := os.Getenv("TOPIC_PEERS"); topicPeersJSON != "" {
		var topicPeerConfig map[string]struct {
			Peers []string `json:"peers"`
			SSE   bool     `json:"sse"`
			GASP  bool     `json:"gasp"`
		}
		
		if err := json.Unmarshal([]byte(topicPeersJSON), &topicPeerConfig); err != nil {
			log.Printf("Failed to parse TOPIC_PEERS JSON: %v", err)
		} else {
			// Build reverse mapping: peer -> topics (only for topics with SSE enabled)
			for topic, config := range topicPeerConfig {
				if config.SSE { // Only add topics that have SSE enabled
					for _, peer := range config.Peers {
						peer = strings.TrimSpace(peer)
						if peer != "" {
							peerTopics[peer] = append(peerTopics[peer], topic)
						}
					}
				}
			}
			log.Printf("Loaded SSE peer configuration for %d topics", len(topicPeerConfig))
		}
	}
	
	// Fallback to global PEERS for all topics if no TOPIC_PEERS configured
	if len(peerTopics) == 0 {
		if peersEnv := os.Getenv("PEERS"); peersEnv != "" {
			peers := strings.Split(peersEnv, ",")
			for _, peer := range peers {
				peer = strings.TrimSpace(peer)
				if peer != "" {
					// Add all tokens as topics for this peer
					for _, tokenId := range tokens {
						topicName := "tm_" + tokenId
						peerTopics[peer] = append(peerTopics[peer], topicName)
					}
				}
			}
			log.Printf("Using global PEERS fallback for SSE sync")
		}
	}
	
	if len(peerTopics) == 0 {
		log.Println("No peers configured for SSE sync")
		return
	}
	
	// Initialize SSE sync with engine and storage
	sseSync = pubsub.NewSSESync(e, e.Storage)
	
	// Start SSE sync
	if err := sseSync.Start(ctx, peerTopics); err != nil {
		log.Printf("Failed to start SSE sync: %v", err)
	} else {
		log.Printf("Started SSE sync with peers: %v", peerTopics)
	}
}

// stopSSESync stops the centralized SSE sync
func stopSSESync() {
	if sseSync != nil {
		sseSync.Stop()
	}
}

func main() {
	// Create a context with cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize variables for cleanup
	var store storage.EventDataStorage
	var bsv21Lookup *lookups.Bsv21EventsLookup

	// Setup cleanup function
	cleanup := func() {
		log.Println("Shutting down server...")
		cancel()

		// Stop SSE sync
		stopSSESync()

		// Close Redis pub/sub
		if redisPubSub != nil {
			redisPubSub.Close()
		}

		// Close Redis connection
		if redisClient != nil {
			redisClient.Close()
		}

		// Close storage and lookup services
		// Note: EventDataStorage interface doesn't define Close()
		// Individual implementations (Redis, SQLite) may have Close methods
		// but we can't call them through the interface
		// BSV21 lookup doesn't need closing - storage is closed separately

		log.Println("Cleanup complete")
	}
	defer cleanup()

	// Handle OS signals for graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalChan
		log.Println("Received shutdown signal, cleaning up...")
		cleanup()
		os.Exit(0)
	}()

	hostingUrl := os.Getenv("HOSTING_URL")

	// Create storage using the cleaned up configuration
	var err error
	store, err = config.CreateEventStorage(eventsURL, beefURL, redisURL)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	// Initialize BSV21 lookup service
	bsv21Lookup, err = lookups.NewBsv21EventsLookup(store)
	if err != nil {
		log.Fatalf("Failed to initialize bsv21 lookup: %v", err)
	}

	// Get Redis client from storage if we don't already have one
	if redisClient == nil {
		redisClient = store.GetRedisClient()
		if redisClient == nil {
			log.Fatal("Redis client is required for BSV21 overlay operations")
		}
	}

	// Initialize engine
	e = &engine.Engine{
		Managers: map[string]engine.TopicManager{},
		LookupServices: map[string]engine.LookupService{
			"ls_bsv21": bsv21Lookup,
		},
		SyncConfiguration: map[string]engine.SyncConfiguration{},
		Broadcaster: &broadcaster.Arc{
			ApiUrl:  "https://arc.taal.com",
			WaitFor: broadcaster.ACCEPTED_BY_NETWORK,
		},
		HostingURL:   hostingUrl,
		Storage:      store,
		ChainTracker: chaintracker,
	}

	// Load topic managers dynamically from whitelist
	const whitelistKey = "bsv21:whitelist"
	tokens, err := redisClient.SMembers(ctx, whitelistKey).Result()
	if err != nil {
		log.Fatalf("Failed to get whitelisted tokens: %v", err)
	}
	log.Printf("Loading whitelisted tokens: %v", tokens)
	for _, tokenId := range tokens {
		topicName := "tm_" + tokenId
		log.Println("Adding topic manager:", topicName)
		e.Managers[topicName] = topics.NewBsv21ValidatedTopicManager(
			topicName,
			store,
			[]string{tokenId},
		)
		
		// Configure GASP sync peers for this topic
		var gaspPeers []string
		
		// First try TOPIC_PEERS configuration
		if topicPeersJSON := os.Getenv("TOPIC_PEERS"); topicPeersJSON != "" {
			var topicPeerConfig map[string]struct {
				Peers []string `json:"peers"`
				SSE   bool     `json:"sse"`
				GASP  bool     `json:"gasp"`
			}
			
			if err := json.Unmarshal([]byte(topicPeersJSON), &topicPeerConfig); err == nil {
				if config, exists := topicPeerConfig[topicName]; exists && config.GASP {
					for _, peer := range config.Peers {
						peer = strings.TrimSpace(peer)
						if peer != "" {
							gaspPeers = append(gaspPeers, peer)
						}
					}
					log.Printf("Using TOPIC_PEERS GASP configuration for topic %s", topicName)
				}
			}
		}
		
		// Fallback to global PEERS if no topic-specific GASP config
		if len(gaspPeers) == 0 {
			if peersEnv := os.Getenv("PEERS"); peersEnv != "" {
				peers := strings.Split(peersEnv, ",")
				for _, peer := range peers {
					peer = strings.TrimSpace(peer)
					if peer != "" {
						gaspPeers = append(gaspPeers, peer)
					}
				}
				log.Printf("Using global PEERS fallback for GASP sync on topic %s", topicName)
			}
		}
		
		// Configure GASP sync if we have peers
		if len(gaspPeers) > 0 {
			e.SyncConfiguration[topicName] = engine.SyncConfiguration{
				Type:  engine.SyncConfigurationPeers,
				Peers: gaspPeers,
			}
			log.Printf("Configured GASP sync for topic %s with peers: %v", topicName, gaspPeers)
		}
	}

	// Start GASP sync if requested
	if SYNC {
		go func() {
			log.Println("Starting GASP sync...")
			if err := e.StartGASPSync(ctx); err != nil {
				log.Printf("Error starting GASP sync: %v", err)
			}
		}()
		
		// Start SSE clients for topics that have SSE enabled
		go func() {
			log.Println("Starting SSE sync...")
			startSSESync(ctx, tokens)
		}()
	}

	// Start broadcasting messages in a separate goroutine
	go broadcastMessages(ctx)

	// Create a new Fiber app
	app := fiber.New(fiber.Config{
		// EnablePrintRoutes: true, // Set this to true to print routes
	})
	app.Use(logger.New())

	// Setup OpenAPI documentation
	setupOpenAPIDocumentation(app)

	onesat := app.Group("/api/1sat")

	// Common handler for parsing event query parameters
	parseEventQuery := func(c *fiber.Ctx) *storage.EventQuestion {
		// Parse query parameters
		fromScore := 0.0
		limit := 100 // default limit

		// Parse 'from' parameter as float64
		if fromParam := c.Query("from"); fromParam != "" {
			if score, err := strconv.ParseFloat(fromParam, 64); err == nil {
				fromScore = score
			}
		}

		// Parse 'limit' parameter
		if limitParam := c.Query("limit"); limitParam != "" {
			if l, err := strconv.Atoi(limitParam); err == nil && l > 0 {
				limit = l
				if limit > 1000 {
					limit = 1000 // cap at 1000
				}
			}
		}

		return &storage.EventQuestion{
			Event: c.Params("event"),
			From:  fromScore,
			Limit: limit,
		}
	}

	// Route for event history
	onesat.Get("/events/:event/history", func(c *fiber.Ctx) error {
		event := c.Params("event")
		log.Printf("Received request for BSV21 event history: %s", event)

		// Build question and call FindOutputData for history
		question := parseEventQuery(c)
		question.Event = event
		question.UnspentOnly = false // History includes all outputs
		outputs, err := store.FindOutputData(c.Context(), question)
		if err != nil {
			log.Printf("History lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": err.Error(),
			})
		}

		return c.JSON(outputs)
	})

	// POST route for multiple events history
	onesat.Post("/events/history", func(c *fiber.Ctx) error {
		// Parse the request body - accept array of events directly
		var events []string
		if err := c.BodyParser(&events); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid request body",
			})
		}

		if len(events) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "No events provided",
			})
		}

		// Limit the number of events to prevent abuse
		if len(events) > 100 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Too many events (max 100)",
			})
		}

		log.Printf("Received multi-event history request for %d events", len(events))

		// Parse query parameters for paging
		question := parseEventQuery(c)
		question.Events = events
		question.UnspentOnly = false // History includes all outputs

		outputs, err := store.FindOutputData(c.Context(), question)
		if err != nil {
			log.Printf("History lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve event history",
			})
		}

		return c.JSON(outputs)
	})

	// Route for unspent events only
	onesat.Get("/events/:event/unspent", func(c *fiber.Ctx) error {
		event := c.Params("event")
		log.Printf("Received request for unspent BSV21 events: %s", event)

		// Build question and call FindOutputData for unspent
		question := parseEventQuery(c)
		question.Event = event
		question.UnspentOnly = true
		outputs, err := store.FindOutputData(c.Context(), question)
		if err != nil {
			log.Printf("Unspent lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": err.Error(),
			})
		}

		return c.JSON(outputs)
	})

	// POST route for multiple events unspent
	onesat.Post("/events/unspent", func(c *fiber.Ctx) error {
		// Parse the request body - accept array of events directly
		var events []string
		if err := c.BodyParser(&events); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid request body",
			})
		}

		if len(events) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "No events provided",
			})
		}

		// Limit the number of events to prevent abuse
		if len(events) > 100 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Too many events (max 100)",
			})
		}

		log.Printf("Received multi-event unspent request for %d events", len(events))

		// Parse query parameters for paging
		question := parseEventQuery(c)
		question.Events = events
		question.UnspentOnly = true

		outputs, err := store.FindOutputData(c.Context(), question)
		if err != nil {
			log.Printf("Unspent lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve unspent events",
			})
		}

		return c.JSON(outputs)
	})

	onesat.Get("/bsv21/:tokenId", func(c *fiber.Ctx) error {
		tokenIdStr := c.Params("tokenId")
		log.Printf("Received request for BSV21 token details: %s", tokenIdStr)

		// Parse the tokenId string into an outpoint
		outpoint, err := transaction.OutpointFromString(tokenIdStr)
		if err != nil {
			log.Printf("Invalid token ID format: %v", err)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid token ID format",
			})
		}

		// Use the dedicated GetToken method
		tokenData, err := bsv21Lookup.GetToken(c.Context(), outpoint)
		if err != nil {
			log.Printf("GetToken error: %v", err)

			// Determine appropriate status code based on error
			if err.Error() == "token not found" {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
					"message": "Token not found",
				})
			} else if strings.Contains(err.Error(), "not a mint transaction") {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
					"message": err.Error(),
				})
			}

			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve token details",
			})
		}

		return c.JSON(tokenData)
	})

	onesat.Get("/bsv21/:tokenId/block/:height", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		heightStr := c.Params("height")

		log.Printf("Received block request for BSV21 tokenId: %s at height: %s", tokenId, heightStr)

		// Parse height as uint32
		height64, err := strconv.ParseUint(heightStr, 10, 32)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid height parameter",
			})
		}
		height := uint32(height64)

		// Get transactions from storage for the specific token's topic at this height
		// Use the topic manager ID format: tm_<tokenId>
		topic := "tm_" + tokenId
		transactions, err := store.GetTransactionsByTopicAndHeight(c.Context(), topic, height)
		if err != nil {
			log.Printf("GetBlockData error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to get block data",
			})
		}

		// Fetch block header information from chaintracker
		blockHeader, err := chaintracker.BlockByHeight(c.Context(), height)
		if err != nil {
			log.Printf("Failed to get block header: %v", err)
			// Continue without header info rather than failing completely
			return c.JSON(fiber.Map{
				"block": fiber.Map{
					"height":            height,
					"hash":              "",
					"previousblockhash": "",
				},
				"transactions": transactions,
			})
		}

		// Build response with header and transactions
		response := fiber.Map{
			"block": fiber.Map{
				"height":            height,
				"hash":              blockHeader.Hash.String(),
				"previousblockhash": blockHeader.PreviousBlock.String(),
				"timestamp":         blockHeader.Timestamp,
			},
			"transactions": transactions,
		}

		return c.JSON(response)
	})

	onesat.Get("/bsv21/:tokenId/:lockType/:address/balance", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		lockType := c.Params("lockType")
		address := c.Params("address")

		// Build event string from lockType, address, and tokenId
		event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
		log.Printf("Received balance request for token %s, %s address %s", tokenId, lockType, address)

		// Get balance for the single event
		balance, outputs, err := bsv21Lookup.GetBalance(c.Context(), []string{event})
		if err != nil {
			log.Printf("Balance calculation error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to calculate balance",
			})
		}

		return c.JSON(fiber.Map{
			"balance":   balance, // uint64 will be serialized correctly by JSON
			"utxoCount": outputs,
		})
	})

	onesat.Get("/bsv21/:tokenId/:lockType/:address/history", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		lockType := c.Params("lockType")
		address := c.Params("address")

		// Build event string from lockType, address, and tokenId
		event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
		log.Printf("Received history request for token %s, %s address %s", tokenId, lockType, address)

		// Parse query parameters for paging
		question := parseEventQuery(c)
		question.Events = []string{event}
		question.UnspentOnly = false // History includes all outputs

		outputs, err := store.FindOutputData(c.Context(), question)
		if err != nil {
			log.Printf("History lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve output history",
			})
		}

		return c.JSON(outputs)
	})

	onesat.Get("/bsv21/:tokenId/:lockType/:address/unspent", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		lockType := c.Params("lockType")
		address := c.Params("address")

		// Build event string from lockType, address, and tokenId
		event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
		log.Printf("Received unspent request for token %s, %s address %s", tokenId, lockType, address)

		// Get UTXOs for the single event using FindOutputData (now includes full data)
		question := &storage.EventQuestion{
			Events:      []string{event},
			UnspentOnly: true,
			From:        0,
			Limit:       0, // No limit
		}

		outputs, err := store.FindOutputData(c.Context(), question)
		if err != nil {
			log.Printf("Unspent lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve unspent outputs",
			})
		}

		return c.JSON(outputs)
	})

	// POST endpoint for multiple address balance queries
	onesat.Post("/bsv21/:tokenId/:lockType/balance", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		lockType := c.Params("lockType")

		// Parse the request body - accept array of addresses directly
		var addresses []string
		if err := c.BodyParser(&addresses); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid request body",
			})
		}

		if len(addresses) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "No addresses provided",
			})
		}

		// Limit the number of addresses to prevent abuse
		if len(addresses) > 100 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Too many addresses (max 100)",
			})
		}

		log.Printf("Received multi-balance request for token %s, %s for %d addresses", tokenId, lockType, len(addresses))

		// Build event strings for all addresses
		events := make([]string, 0, len(addresses))
		for _, address := range addresses {
			event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
			events = append(events, event)
		}

		// Get total balance for all addresses combined
		balance, utxoCount, err := bsv21Lookup.GetBalance(c.Context(), events)
		if err != nil {
			log.Printf("Balance calculation error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to calculate balances",
			})
		}

		return c.JSON(fiber.Map{
			"balance":   balance,
			"utxoCount": utxoCount,
		})
	})

	// POST endpoint for multiple address history queries
	onesat.Post("/bsv21/:tokenId/:lockType/history", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		lockType := c.Params("lockType")

		// Parse the request body - accept array of addresses directly
		var addresses []string
		if err := c.BodyParser(&addresses); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid request body",
			})
		}

		if len(addresses) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "No addresses provided",
			})
		}

		// Limit the number of addresses to prevent abuse
		if len(addresses) > 100 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Too many addresses (max 100)",
			})
		}

		log.Printf("Received multi-history request for token %s, %s for %d addresses", tokenId, lockType, len(addresses))

		// Build event strings for all addresses
		events := make([]string, 0, len(addresses))
		for _, address := range addresses {
			event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
			events = append(events, event)
		}

		// Parse query parameters for paging
		question := parseEventQuery(c)
		question.Events = events
		question.UnspentOnly = false // History includes all outputs

		outputs, err := store.FindOutputData(c.Context(), question)
		if err != nil {
			log.Printf("History lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve output history",
			})
		}

		return c.JSON(outputs)
	})

	// POST endpoint for multiple address unspent queries
	onesat.Post("/bsv21/:tokenId/:lockType/unspent", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		lockType := c.Params("lockType")

		// Parse the request body - accept array of addresses directly
		var addresses []string
		if err := c.BodyParser(&addresses); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid request body",
			})
		}

		if len(addresses) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "No addresses provided",
			})
		}

		// Limit the number of addresses to prevent abuse
		if len(addresses) > 100 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Too many addresses (max 100)",
			})
		}

		log.Printf("Received multi-unspent request for token %s, %s for %d addresses", tokenId, lockType, len(addresses))

		// Build event strings for all addresses
		events := make([]string, 0, len(addresses))
		for _, address := range addresses {
			event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
			events = append(events, event)
		}

		// Get UTXOs for all addresses combined using FindOutputData
		question := &storage.EventQuestion{
			Events:      events,
			UnspentOnly: true,
			From:        0,
			Limit:       0, // No limit
		}

		outputs, err := store.FindOutputData(c.Context(), question)
		if err != nil {
			log.Printf("Unspent lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve unspent outputs",
			})
		}

		return c.JSON(outputs)
	})

	onesat.Get("/block/tip", func(c *fiber.Ctx) error {
		// Get current block tip from chaintracker
		tip, err := chaintracker.GetChaintip(c.Context())
		if err != nil {
			log.Printf("Failed to get block tip: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to get current block tip",
			})
		}

		// Return header with height and properly formatted hash strings
		return c.JSON(fiber.Map{
			"height":            tip.Height,
			"hash":              tip.Header.Hash.String(),
			"version":           tip.Header.Version,
			"prevBlockHash":     tip.Header.PreviousBlock.String(),
			"merkleRoot":        tip.Header.MerkleRoot.String(),
			"creationTimestamp": tip.Header.Timestamp,
			"difficultyTarget":  tip.Header.Bits,
			"nonce":             tip.Header.Nonce,
		})
	})

	onesat.Get("/block/:height", func(c *fiber.Ctx) error {
		heightStr := c.Params("height")

		// Parse height as uint32
		height64, err := strconv.ParseUint(heightStr, 10, 32)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid height parameter",
			})
		}
		height := uint32(height64)

		// Get block header by height
		blockHeader, err := chaintracker.BlockByHeight(c.Context(), height)
		if err != nil {
			log.Printf("Failed to get block header for height %d: %v", height, err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to get block header",
			})
		}

		// Return header with height and properly formatted hash strings
		return c.JSON(fiber.Map{
			"height":            height,
			"hash":              blockHeader.Hash.String(),
			"version":           blockHeader.Version,
			"prevBlockHash":     blockHeader.PreviousBlock.String(),
			"merkleRoot":        blockHeader.MerkleRoot.String(),
			"creationTimestamp": blockHeader.Timestamp,
			"difficultyTarget":  blockHeader.Bits,
			"nonce":             blockHeader.Nonce,
		})
	})

	// Add custom BSV21-specific routes
	onesat.Get("/subscribe/:events", func(c *fiber.Ctx) error {
		events := strings.Split(c.Params("events"), ",")
		log.Printf("Subscription request for events: %v", events)

		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
		c.Set("Transfer-Encoding", "chunked")
		c.Set("X-Accel-Buffering", "no")
		c.Set("Access-Control-Allow-Origin", "*")

		// Check for Last-Event-ID header for resumption
		lastEventID := c.Get("Last-Event-ID")
		var fromScore float64
		if lastEventID != "" {
			if score, err := strconv.ParseFloat(lastEventID, 64); err == nil {
				fromScore = score
				log.Printf("Resuming from score: %f", fromScore)
			}
		}

		// Create a channel for this client to signal when to close
		clientDone := make(chan struct{})

		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			// If resuming, first send any missed events using buffered events
			if fromScore > 0 {
				pubsub := store.GetPubSub()
				if pubsub != nil {
					// For each event, get buffered events since the last score
					for _, event := range events {
						if recentEvents, err := pubsub.GetRecentEvents(ctx, event, fromScore); err == nil {
							// Send each missed event with its actual score
							for _, eventData := range recentEvents {
								fmt.Fprintf(w, "data: %s\n", eventData.Outpoint)
								fmt.Fprintf(w, "id: %.0f\n\n", eventData.Score)
								if err := w.Flush(); err != nil {
									return // Connection closed
								}
							}
						}
					}
				}
			}

			// Send initial connection message
			fmt.Fprintf(w, "data: Connected to events: %s\n\n", strings.Join(events, ", "))
			if err := w.Flush(); err != nil {
				return // Connection closed
			}

			// Register this client for each event with RedisPubSub
			if redisPubSub != nil {
				for _, event := range events {
					redisPubSub.AddSSEClient(event, w)
				}

				// Cleanup function
				defer func() {
					// Remove the client when disconnected
					if redisPubSub != nil {
						for _, event := range events {
							redisPubSub.RemoveSSEClient(event, w)
						}
					}
					close(clientDone)
				}()
			}

			// Keep connection alive - wait for context cancellation
			<-ctx.Done()
		})

		return nil
	})

	// Register overlay service routes using server pattern
	server.RegisterRoutes(app, &server.RegisterRoutesConfig{
		ARCAPIKey:        os.Getenv("ARC_API_KEY"),
		ARCCallbackToken: os.Getenv("ARC_CALLBACK_TOKEN"),
		Engine:           e,
	})

	// Start the server in a goroutine
	go func() {
		log.Printf("Starting server on port %d...", PORT)
		if err := app.Listen(fmt.Sprintf(":%d", PORT)); err != nil {
			log.Printf("Server error: %v", err)
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()

	// Shutdown the server
	log.Println("Shutting down HTTP server...")
	if err := app.Shutdown(); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
}
