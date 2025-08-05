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
	"sync"
	"syscall"

	"github.com/b-open-io/bsv21-overlay/lookups"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/overlay/beef"
	"github.com/b-open-io/overlay/lookup/events"
	"github.com/b-open-io/overlay/publish"
	"github.com/b-open-io/overlay/storage"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-overlay-services/pkg/server"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
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

func init() {
	godotenv.Load("../../.env")
	chaintracker = &headers_client.Client{
		Url:    os.Getenv("BLOCK_HEADERS_URL"),
		ApiKey: os.Getenv("BLOCK_HEADERS_API_KEY"),
	}
	PORT, _ = strconv.Atoi(os.Getenv("PORT"))
	flag.IntVar(&PORT, "p", PORT, "Port to listen on")
	flag.BoolVar(&SYNC, "s", false, "Start sync")
	flag.Parse()
	if PORT == 0 {
		PORT = 3000
	}
	if redisOpts, err := redis.ParseURL(os.Getenv("REDIS")); err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	} else {
		rdb = redis.NewClient(redisOpts)
		sub = redis.NewClient(redisOpts)
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
	var store *storage.MongoStorage
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
		if bsv21Lookup != nil {
			bsv21Lookup.Close()
		}

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

	// Initialize BEEF storage
	redisBeefURL := os.Getenv("REDIS_BEEF")
	if redisBeefURL == "" {
		redisBeefURL = os.Getenv("REDIS")
	}
	beefStorage, err := beef.NewRedisBeefStorage(redisBeefURL, 0)
	if err != nil {
		log.Fatalf("Failed to create BEEF storage: %v", err)
	}

	// Initialize publisher
	publisher, err := publish.NewRedisPublish(os.Getenv("REDIS"))
	if err != nil {
		log.Fatalf("Failed to create publisher: %v", err)
	}

	// Initialize storage
	mongoURL := os.Getenv("MONGO_URL")
	if mongoURL == "" {
		mongoURL = "mongodb://localhost:27017"
	}
	store, err = storage.NewMongoStorage(mongoURL, "mnee", beefStorage, publisher)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	// Initialize BSV21 lookup service
	bsv21Lookup, err = lookups.NewBsv21EventsLookup(mongoURL, "mnee", store)
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

	// Load topic managers dynamically from Redis
	if tms, err := rdb.SMembers(ctx, "topics").Result(); err != nil {
		log.Fatalf("Failed to get topics from Redis: %v", err)
	} else {
		for _, top := range tms {
			log.Println("Adding topic manager:", top)
			tokenId := top[3:] // Remove "tm_" prefix
			e.Managers[top] = topics.NewBsv21ValidatedTopicManager(
				top,
				store,
				[]string{tokenId},
			)
			e.SyncConfiguration[top] = engine.SyncConfiguration{
				Type:  engine.SyncConfigurationPeers,
				Peers: peers,
			}
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
	app := fiber.New()
	app.Use(logger.New())

	// Register overlay service routes using server pattern
	server.RegisterRoutesWithErrorHandler(app, &server.RegisterRoutesConfig{
		ARCAPIKey:        os.Getenv("ARC_API_KEY"),
		ARCCallbackToken: os.Getenv("ARC_CALLBACK_TOKEN"),
		Engine:           e,
	})

	onesat := app.Group("/1sat")
	onesat.Get("/events/:event", func(c *fiber.Ctx) error {
		event := c.Params("event")
		log.Printf("Received request for BSV21 event: %s", event)

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

		// Create the lookup question
		question := &events.Question{
			Event: event,
			From:  fromScore,
			Limit: limit,
		}

		// Marshal question to JSON
		queryBytes, err := json.Marshal(question)
		if err != nil {
			log.Printf("Failed to create query: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to create query",
			})
		}

		// Create lookup question wrapper
		lookupQuestion := &lookup.LookupQuestion{
			Service: "ls_bsv21",
			Query:   queryBytes,
		}

		// Call the lookup service
		answer, err := bsv21Lookup.Lookup(c.Context(), lookupQuestion)
		if err != nil {
			log.Printf("Lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		// Return the answer
		return c.JSON(answer)
	})

	onesat.Get("/bsv21/:event/balance", func(c *fiber.Ctx) error {
		event := c.Params("event")
		log.Printf("Received balance request for BSV21 event: %s", event)

		// Sum the values for the specified event (unspent only)
		balance, outputs, err := bsv21Lookup.ValueSumUint64(c.Context(), event, events.SpentStatusUnspent)
		if err != nil {
			log.Printf("Balance calculation error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "Failed to calculate balance",
			})
		}

		return c.JSON(fiber.Map{
			"event":   event,
			"balance": balance, // uint64 will be serialized correctly by JSON
			"outputs": outputs,
		})
	})

	// onesat.Get("/bsv21/:event/outputs", func(c *fiber.Ctx) error {
	// 	event := c.Params("event")

	// 	// Get the outputs for the specified event
	// 	outputs, err := bsv21Lookup(c.Context(), event)
	// 	if err != nil {
	// 		log.Printf("Outputs retrieval error: %v", err)
	// 		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
	// 			"error": "Failed to retrieve outputs",
	// 		})
	// 	}

	// 	return c.JSON(fiber.Map{
	// 		"event":   event,
	// 		"outputs": outputs,
	// 	})
	// })

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
				question := &events.Question{
					Event: topic,
					From:  fromScore,
					Limit: 100,
				}

				// Use LookupOutpoints to get outpoints with their actual scores
				if outpoints, scores, err := bsv21Lookup.LookupOutpoints(c.Context(), question); err == nil {
					// Send each missed event with its actual score
					for i, outpoint := range outpoints {
						fmt.Fprintf(writer, "data: %s\n", outpoint.String())
						fmt.Fprintf(writer, "id: %f\n\n", scores[i])
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
