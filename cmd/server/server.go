package main

import (
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
	"github.com/b-open-io/overlay/routes"
	"github.com/b-open-io/overlay/storage"
	"github.com/b-open-io/overlay/sync"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-overlay-services/pkg/server"
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
var LIBP2P_SYNC bool

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

var sseSyncManager *sync.SSESyncManager       // Centralized SSE sync manager
var libp2pSyncManager *sync.LibP2PSyncManager // LibP2P-based transaction sync manager
var libp2pSync *pubsub.LibP2PSync             // LibP2P sync instance for routes
var peerBroadcaster *pubsub.PeerBroadcaster   // Peer transaction broadcaster
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
	flag.BoolVar(&LIBP2P_SYNC, "p2p", os.Getenv("LIBP2P_SYNC") == "true", "Enable LibP2P sync")
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
		if sseSyncManager != nil {
			sseSyncManager.Stop()
		}

		// Stop LibP2P sync
		if libp2pSyncManager != nil {
			libp2pSyncManager.Stop()
		}

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
	// Build peer-to-topics mapping for broadcasting
	peerTopics := make(map[string][]string)
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

			// Build peer-to-topics mapping from storage configuration
			ssePeerTopics := make(map[string][]string)
			for _, tokenId := range tokens {
				topicName := "tm_" + tokenId
				if peers, err := getPeersWithSetting(ctx, store, tokenId, "sse"); err == nil && len(peers) > 0 {
					// Build reverse mapping: peer -> topics (only for peers with SSE enabled)
					for _, peer := range peers {
						ssePeerTopics[peer] = append(ssePeerTopics[peer], topicName)
					}
					log.Printf("Loaded SSE configuration for topic %s with %d peers", topicName, len(peers))
				}
			}

			// Register SSE sync
			var err error
			sseSyncManager, err = sync.RegisterSSESync(&sync.SSESyncConfig{
				Engine:     e,
				Storage:    store,
				PeerTopics: ssePeerTopics,
				Context:    ctx,
			})
			if err != nil {
				log.Printf("Error starting SSE sync: %v", err)
			}
		}()
	}

	// Start LibP2P sync if requested
	if LIBP2P_SYNC {
		go func() {
			log.Println("Starting LibP2P sync...")

			// Build topic list for LibP2P sync
			topics := make([]string, 0, len(tokens))
			for _, tokenId := range tokens {
				topicName := "tm_" + tokenId
				topics = append(topics, topicName)
			}

			// Register LibP2P sync
			var err error
			libp2pSyncManager, err = sync.RegisterLibP2PSync(&sync.LibP2PSyncConfig{
				Engine:  e,
				Storage: store,
				Topics:  topics,
				Context: ctx,
			})
			if err != nil {
				log.Printf("Error starting LibP2P sync: %v", err)
			} else if libp2pSyncManager != nil {
				// Set global variable for submit route access
				libp2pSync = libp2pSyncManager.GetLibP2PSync()
			}
		}()
	}


	// Create a new Fiber app
	app := fiber.New(fiber.Config{
		// EnablePrintRoutes: true, // Set this to true to print routes
	})
	app.Use(logger.New())

	// Setup OpenAPI documentation
	setupOpenAPIDocumentation(app)

	onesat := app.Group("/api/1sat")

	// Register common 1sat routes
	routes.RegisterCommonRoutes(onesat, &routes.CommonRoutesConfig{
		Storage:      store,
		ChainTracker: chaintracker,
	})

	// Register SSE streaming routes
	routes.RegisterSSERoutes(onesat, &routes.SSERoutesConfig{
		Storage: store,
		Context: ctx,
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
		question := routes.ParseEventQuery(c)
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
		question := routes.ParseEventQuery(c)
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

	// Register enhanced submit route with peer broadcasting
	routes.RegisterSubmitRoutes(app, &routes.SubmitRoutesConfig{
		Engine:          e,
		LibP2PSync:      libp2pSync, // Will be set by LibP2P sync initialization if enabled
		PeerBroadcaster: peerBroadcaster,
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
