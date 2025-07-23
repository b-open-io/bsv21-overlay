package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/b-open-io/bsv21-overlay/lookups"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/overlay/beef"
	"github.com/b-open-io/overlay/publish"
	"github.com/b-open-io/overlay/storage"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-overlay-services/pkg/server"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/topic"
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
				parts := strings.Split(msg.Payload, ":")
				if len(parts) >= 2 {
					_, err := fmt.Fprintf(client, "data: %s\n", parts[1])
					if err == nil {
						_, _ = fmt.Fprintf(client, "id: %s\n\n", parts[0])
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
	defer cancel()

	// Handle OS signals for graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalChan
		log.Println("Received shutdown signal, cleaning up...")
		cancel()
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
	store, err := storage.NewMongoStorage(mongoURL, "mnee", beefStorage, publisher)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	// Initialize BSV21 lookup service
	bsv21Lookup, err := lookups.NewBsv21EventsLookup(mongoURL, "mnee")
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

	// Add custom BSV21-specific routes
	app.Get("/subscribe/:topics", func(c *fiber.Ctx) error {
		topics := strings.Split(c.Params("topics"), ",")
		log.Printf("Subscription request for topics: %v", topics)

		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
		c.Set("Access-Control-Allow-Origin", "*")

		// Create a writer for this client
		writer := bufio.NewWriter(c.Response().BodyWriter())

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

	// Custom submit endpoint for backward compatibility
	app.Post("/submit", func(c *fiber.Ctx) error {
		topicsHeader := c.Get("x-topics", "")
		if topicsHeader == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Missing x-topics header",
			})
		}
		taggedBeef := overlay.TaggedBEEF{}
		if err := json.Unmarshal([]byte(topicsHeader), &taggedBeef.Topics); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid x-topics header",
			})
		}
		taggedBeef.Beef = c.Body()

		onSteakReady := func(steak *overlay.Steak) {
			for top, admit := range *steak {
				if sync, ok := e.SyncConfiguration[top]; ok {
					if sync.Type == engine.SyncConfigurationPeers && (len(admit.CoinsToRetain) > 0 || len(admit.OutputsToAdmit) > 0) {
						for _, peer := range sync.Peers {
							if _, err := (&topic.HTTPSOverlayBroadcastFacilitator{Client: http.DefaultClient}).Send(peer, &overlay.TaggedBEEF{
								Beef:   taggedBeef.Beef,
								Topics: []string{top},
							}); err != nil {
								log.Printf("Error submitting taggedBEEF to peer %s: %v", peer, err)
							}
						}
					}
				}
			}
		}

		if steak, err := e.Submit(c.Context(), taggedBeef, engine.SubmitModeCurrent, onSteakReady); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		} else {
			return c.JSON(steak)
		}
	})

	// Start the server
	log.Printf("Starting server on port %d...", PORT)
	if err := app.Listen(fmt.Sprintf(":%d", PORT)); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
