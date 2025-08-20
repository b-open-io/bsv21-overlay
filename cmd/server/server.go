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
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/broadcaster"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"
)

var chaintracker *headers_client.Client
var PORT int
var SYNC bool

const (
	PeerConfigKeyPrefix = "peers:tm_"
)

type PeerSettings struct {
	SSE       bool `json:"sse"`
	GASP      bool `json:"gasp"`
	Broadcast bool `json:"broadcast"`
}

// loadPeerConfigFromStorage reads peer configuration from storage for a given token
func loadPeerConfigFromStorage(ctx context.Context, store storage.EventDataStorage, tokenId string) (map[string]PeerSettings, error) {
	key := PeerConfigKeyPrefix + tokenId
	peerData, err := store.HGetAll(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get peer config for token %s: %v", tokenId, err)
	}
	
	peers := make(map[string]PeerSettings)
	for peerURL, settingsJSON := range peerData {
		var settings PeerSettings
		if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
			log.Printf("Warning: failed to parse settings for peer %s (token %s): %v", peerURL, tokenId, err)
			continue
		}
		peers[peerURL] = settings
	}
	
	return peers, nil
}

// getPeersWithSetting returns peers that have a specific setting enabled for a token
func getPeersWithSetting(ctx context.Context, store storage.EventDataStorage, tokenId string, settingName string) ([]string, error) {
	peerConfig, err := loadPeerConfigFromStorage(ctx, store, tokenId)
	if err != nil {
		return nil, err
	}
	
	var enabledPeers []string
	for peerURL, settings := range peerConfig {
		switch settingName {
		case "sse":
			if settings.SSE {
				enabledPeers = append(enabledPeers, peerURL)
			}
		case "gasp":
			if settings.GASP {
				// Add /api/v1 path for GASP endpoints
				enabledPeers = append(enabledPeers, peerURL+"/api/v1")
			}
		case "broadcast":
			if settings.Broadcast {
				enabledPeers = append(enabledPeers, peerURL)
			}
		}
	}
	
	return enabledPeers, nil
}

var sseSync *pubsub.SSESync                         // Centralized SSE sync manager
var peerBroadcaster *pubsub.PeerBroadcaster         // Peer transaction broadcaster
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

	// Redis configuration is now handled by the storage factory
	
}

// broadcastMessages handles real-time event broadcasting using PubSub
func broadcastMessages(ctx context.Context, store storage.EventDataStorage) {
	pubsub := store.GetPubSub()
	if pubsub == nil {
		log.Println("No pub/sub configured, real-time broadcasting disabled")
		return
	}

	// Start the pub/sub system
	if err := pubsub.Start(ctx); err != nil {
		log.Printf("PubSub start error: %v", err)
	}
}

// startSSESync starts centralized SSE sync with configured peers
func startSSESync(ctx context.Context, store storage.EventDataStorage, tokens []string) {
	// Build peer-to-topics mapping from storage configuration
	peerTopics := make(map[string][]string)
	
	// Load SSE-enabled peers for each token from storage
	for _, tokenId := range tokens {
		topicName := "tm_" + tokenId
		
		if peers, err := getPeersWithSetting(ctx, store, tokenId, "sse"); err == nil && len(peers) > 0 {
			// Build reverse mapping: peer -> topics (only for peers with SSE enabled)
			for _, peer := range peers {
				peerTopics[peer] = append(peerTopics[peer], topicName)
			}
			log.Printf("Loaded SSE configuration for topic %s with %d peers", topicName, len(peers))
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

// setupPeerBroadcasting configures peer broadcasting from storage
func setupPeerBroadcasting(ctx context.Context, store storage.EventDataStorage, tokens []string) {
	// Build peer-to-topics mapping for broadcasting
	peerTopics := make(map[string][]string)
	
	// Load broadcast-enabled peers for each token from storage
	for _, tokenId := range tokens {
		topicName := "tm_" + tokenId
		
		if peers, err := getPeersWithSetting(ctx, store, tokenId, "broadcast"); err == nil && len(peers) > 0 {
			// Build reverse mapping: peer -> topics (only for peers with broadcast enabled)
			for _, peer := range peers {
				peerTopics[peer] = append(peerTopics[peer], topicName)
			}
			log.Printf("Loaded broadcast configuration for topic %s with %d peers", topicName, len(peers))
		}
	}
	
	// Create peer broadcaster with the configured peer-topic mapping
	if len(peerTopics) > 0 {
		peerBroadcaster = pubsub.NewPeerBroadcaster(peerTopics)
		log.Printf("Configured peer broadcaster with %d peers", len(peerTopics))
	} else {
		log.Println("No peers configured for broadcasting")
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

		// Close pub/sub (handled by storage layer)
		if store != nil {
			if pubsub := store.GetPubSub(); pubsub != nil {
				pubsub.Close()
			}
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

	// Storage layer handles all Redis/database operations

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
	tokens, err := store.SMembers(ctx, whitelistKey)
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
		
		// Configure GASP sync peers for this topic from storage
		gaspPeers, err := getPeersWithSetting(ctx, store, tokenId, "gasp")
		if err == nil && len(gaspPeers) > 0 {
			log.Printf("Loaded GASP configuration for topic %s with %d peers", topicName, len(gaspPeers))
			
			// Configure GASP sync
			e.SyncConfiguration[topicName] = engine.SyncConfiguration{
				Type:  engine.SyncConfigurationPeers,
				Peers: gaspPeers,
			}
			log.Printf("Configured GASP sync for topic %s with peers: %v", topicName, gaspPeers)
		}
	}

	// Setup peer broadcasting for transaction submission
	setupPeerBroadcasting(ctx, store, tokens)

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
			startSSESync(ctx, store, tokens)
		}()
	}

	// Start broadcasting messages in a separate goroutine
	go broadcastMessages(ctx, store)

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
	onesat.Get("/events/:topic/:event/history", func(c *fiber.Ctx) error {
		topic := c.Params("topic")
		event := c.Params("event")
		log.Printf("Received request for BSV21 event history: %s (topic: %s)", event, topic)

		// Build question and call FindOutputData for history
		question := parseEventQuery(c)
		question.Event = event
		question.Topic = topic
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
	onesat.Post("/events/:topic/history", func(c *fiber.Ctx) error {
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

		topic := c.Params("topic")
		log.Printf("Received multi-event history request for %d events (topic: %s)", len(events), topic)

		// Parse query parameters for paging
		question := parseEventQuery(c)
		question.Events = events
		question.Topic = topic
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
	onesat.Get("/events/:topic/:event/unspent", func(c *fiber.Ctx) error {
		topic := c.Params("topic")
		event := c.Params("event")
		log.Printf("Received request for unspent BSV21 events: %s (topic: %s)", event, topic)

		// Build question and call FindOutputData for unspent
		question := parseEventQuery(c)
		question.Event = event
		question.Topic = topic
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
	onesat.Post("/events/:topic/unspent", func(c *fiber.Ctx) error {
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

		topic := c.Params("topic")
		log.Printf("Received multi-event unspent request for %d events (topic: %s)", len(events), topic)

		// Parse query parameters for paging
		question := parseEventQuery(c)
		question.Events = events
		question.Topic = topic
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
		question.Topic = "tm_" + tokenId
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
		question.Topic = "tm_" + tokenId
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
			// If resuming, first send any missed events using storage
			if fromScore > 0 {
				// For each event, get recent events since the last score
				for _, event := range events {
					// For BSV21, if event starts with "tm_", it's a topic, otherwise extract tokenId
					var topic string
					if strings.HasPrefix(event, "tm_") {
						topic = event
					} else {
						// Extract tokenId from event patterns like "p2pkh:address:tokenId"
						parts := strings.Split(event, ":")
						if len(parts) >= 3 {
							tokenId := parts[len(parts)-1] // Last part is tokenId
							topic = "tm_" + tokenId
						} else {
							continue // Skip malformed events
						}
					}
					
					if recentEvents, err := store.LookupEventScores(ctx, topic, event, fromScore); err == nil {
						// Send each missed event with its actual score
						for _, eventScore := range recentEvents {
							fmt.Fprintf(w, "data: %s\n", eventScore.Member)
							fmt.Fprintf(w, "id: %.0f\n\n", eventScore.Score)
							if err := w.Flush(); err != nil {
								return // Connection closed
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

			// Register this client for each event with PubSub
			if ps := store.GetPubSub(); ps != nil {
				// Create SSE manager if needed and register client
				sseManager := pubsub.NewSSEManager(ps)
				for _, event := range events {
					sseManager.AddSSEClient(event, w)
				}

				// Start the SSE manager to listen for events
				if err := sseManager.Start(ctx); err != nil {
					fmt.Fprintf(w, "data: Error starting SSE manager: %v\n\n", err)
					w.Flush()
					return
				}

				// Cleanup function
				defer func() {
					// Stop the SSE manager and remove the client when disconnected
					sseManager.Stop()
					for _, event := range events {
						sseManager.RemoveSSEClient(event, w)
					}
					close(clientDone)
				}()
			}

			// Keep connection alive - wait for context cancellation
			<-ctx.Done()
		})

		return nil
	})

	// Add custom submit route with peer broadcasting
	app.Post("/api/v1/submit", func(c *fiber.Ctx) error {
		// Parse x-topics header
		topicsHeader := c.Get("x-topics")
		if topicsHeader == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "x-topics header is required",
			})
		}
		
		topics := strings.Split(topicsHeader, ",")
		for i, topic := range topics {
			topics[i] = strings.TrimSpace(topic)
		}
		
		// Read BEEF body
		beef := c.Body()
		if len(beef) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "BEEF transaction data is required",
			})
		}
		
		// Create TaggedBEEF
		taggedBEEF := overlay.TaggedBEEF{
			Beef:   beef,
			Topics: topics,
		}
		
		// Submit to local engine
		steak, err := e.Submit(c.Context(), taggedBEEF, engine.SubmitModeCurrent, nil)
		if err != nil {
			log.Printf("Failed to submit transaction locally: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to process transaction",
			})
		}
		
		// Build TaggedBEEF with only successfully processed topics for peer broadcasting
		if peerBroadcaster != nil && len(steak) > 0 {
			successfulTopics := make([]string, 0, len(steak))
			for topic := range steak {
				successfulTopics = append(successfulTopics, topic)
			}
			
			// Only broadcast topics that were successfully processed
			broadcastBEEF := overlay.TaggedBEEF{
				Beef:   beef,
				Topics: successfulTopics,
			}
			
			go func() {
				if err := peerBroadcaster.BroadcastTransaction(context.Background(), broadcastBEEF); err != nil {
					log.Printf("Failed to broadcast transaction to peers: %v", err)
				}
			}()
		}
		
		// Return success response (matching overlay service format)
		return c.JSON(fiber.Map{
			"description": "Overlay engine successfully processed the submitted transaction",
			"steak":       steak,
		})
	})

	// Register overlay service routes using server pattern (excluding submit route)
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
