package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/b-open-io/bsv21-overlay/lookups"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/overlay/config"
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
var rdb, sub *redis.Client
var topicClients = make(map[string][]*bufio.Writer) // Map of topic to connected clients
var topicClientsMutex = &sync.Mutex{}               // Mutex to protect topicClients
var peers = []string{}
var e *engine.Engine

// Configuration from flags/env
var (
	eventsURL    string
	beefURL      string
	publisherURL string
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
	flag.StringVar(&publisherURL, "publisher", os.Getenv("PUBLISHER_URL"), "Publisher URL")
	flag.Parse()
	// Apply defaults
	if PORT == 0 {
		PORT = 3000
	}
	if beefURL == "" {
		beefURL = "./beef_storage"
	}
	
	// Set up Redis clients for pub/sub subscriptions (if publisher URL is provided)
	// TODO: This will be refactored into a PubSub interface
	if publisherURL != "" {
		if redisOpts, err := redis.ParseURL(publisherURL); err != nil {
			log.Fatalf("Failed to parse publisher URL for subscriptions: %v", err)
		} else {
			rdb = redis.NewClient(redisOpts)
			sub = redis.NewClient(redisOpts)
		}
	}
	PEERS := os.Getenv("PEERS")
	if PEERS != "" {
		peers = strings.Split(PEERS, ",")
	}
}

// broadcastMessages handles real-time event broadcasting
func broadcastMessages(ctx context.Context) {
	pubsub := sub.PSubscribe(ctx, "*")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			// Get the list of clients for the topic
			topicClientsMutex.Lock()
			clients := topicClients[msg.Channel]
			// Create a new slice to hold clients that are still connected
			var activeClients []*bufio.Writer
			for _, client := range clients {
				// Try to write to the client
				// Message format is "score:outpoint"
				parts := strings.SplitN(msg.Payload, ":", 2)
				if len(parts) == 2 {
					score := parts[0]
					outpoint := parts[1]
					_, err := fmt.Fprintf(client, "data: %s\n", outpoint)
					if err == nil {
						_, _ = fmt.Fprintf(client, "id: %s\n\n", score)
						if err := client.Flush(); err == nil {
							activeClients = append(activeClients, client)
						}
					}
				}
			}
			// Update the client list with only active clients
			topicClients[msg.Channel] = activeClients
			topicClientsMutex.Unlock()
		}
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

		// Close database connections
		if rdb != nil {
			rdb.Close()
		}
		if sub != nil {
			sub.Close()
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
	store, err = config.CreateEventStorage(eventsURL, beefURL, publisherURL)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	// Initialize BSV21 lookup service
	bsv21Lookup, err = lookups.NewBsv21EventsLookup(store)
	if err != nil {
		log.Fatalf("Failed to initialize bsv21 lookup: %v", err)
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

	// Load topic managers dynamically from whitelist (using EventDataStorage)
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
			e.SyncConfiguration[topicName] = engine.SyncConfiguration{
				Type:  engine.SyncConfigurationPeers,
				Peers: peers,
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

	// Route for all events
	onesat.Get("/events/:event", func(c *fiber.Ctx) error {
		event := c.Params("event")
		log.Printf("Received request for all BSV21 events: %s", event)

		// Build question and call storage directly
		question := parseEventQuery(c)
		results, err := store.LookupOutpoints(c.Context(), question, true) // include data
		if err != nil {
			log.Printf("Lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": err.Error(),
			})
		}

		return c.JSON(results)
	})

	// Route for unspent events only
	onesat.Get("/events/:event/unspent", func(c *fiber.Ctx) error {
		event := c.Params("event")
		log.Printf("Received request for unspent BSV21 events: %s", event)

		// Build question and call storage directly
		question := parseEventQuery(c)
		question.UnspentOnly = true
		results, err := store.LookupOutpoints(c.Context(), question, true) // include data
		if err != nil {
			log.Printf("Lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": err.Error(),
			})
		}

		return c.JSON(results)
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
	onesat.Get("/subscribe/:topics", func(c *fiber.Ctx) error {
		topics := strings.Split(c.Params("topics"), ",")
		log.Printf("Subscription request for topics: %v", topics)

		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
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

		// Create a writer for this client
		writer := bufio.NewWriter(c.Response().BodyWriter())

		// If resuming, first send any missed events
		if fromScore > 0 {
			// For each topic, query events since the last score
			for _, topic := range topics {
				question := &storage.EventQuestion{
					Event: topic,
					From:  fromScore,
					Limit: 100,
				}

				// Use storage directly to get outpoints with their actual scores
				if results, err := store.LookupOutpoints(c.Context(), question, false); err == nil {
					// Send each missed event with its actual score
					for _, result := range results {
						fmt.Fprintf(writer, "data: %s\n", result.Outpoint.String())
						fmt.Fprintf(writer, "id: %f\n\n", result.Score)
					}
				}
			}
			writer.Flush()
		}

		// Register the client for each topic
		topicClientsMutex.Lock()
		for _, topic := range topics {
			topicClients[topic] = append(topicClients[topic], writer)
		}
		topicClientsMutex.Unlock()

		// Send initial message
		fmt.Fprintf(writer, "data: Connected to topics: %s\n\n", strings.Join(topics, ", "))
		writer.Flush()

		// Keep the connection open
		<-c.Context().Done()

		// Remove the client when disconnected
		topicClientsMutex.Lock()
		for _, topic := range topics {
			clients := topicClients[topic]
			for i, client := range clients {
				if client == writer {
					topicClients[topic] = append(clients[:i], clients[i+1:]...)
					break
				}
			}
		}
		topicClientsMutex.Unlock()

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
