package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/b-open-io/bsv21-overlay/constants"
	"github.com/b-open-io/bsv21-overlay/lookups"
	"github.com/b-open-io/bsv21-overlay/peer"
	bsv21routes "github.com/b-open-io/bsv21-overlay/routes"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/overlay/config"
	"github.com/b-open-io/overlay/headers"
	"github.com/b-open-io/overlay/headers/processor"
	headersRoutes "github.com/b-open-io/overlay/headers/routes"
	"github.com/b-open-io/overlay/pubsub"
	"github.com/b-open-io/overlay/routes"
	"github.com/b-open-io/overlay/storage"
	"github.com/b-open-io/overlay/sync"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-overlay-services/pkg/server"
	"github.com/bsv-blockchain/go-sdk/transaction/broadcaster"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

// Global variables
var (
	chaintracker      *headers.Client
	broadcast         *broadcaster.Arc
	PORT              int
	SYNC              bool
	LIBP2P_SYNC       bool
	sseSyncManager    *sync.SSESyncManager
	libp2pSyncManager *sync.LibP2PSyncManager
	libp2pSync        *pubsub.LibP2PSync
	peerBroadcaster   *pubsub.PeerBroadcaster
	e                 *engine.Engine
)

// Configuration from flags/env
var (
	eventsURL    string
	beefURL      string
	queueURL     string
	pubsubURL    string
	arcURL       string
	arcAPIKey    string
	arcToken     string
	hostingURL   string
	headersURL   string
	headersKey   string
	webhookToken string
)

// Command exports the server command for use in the main CLI
var Command = &cobra.Command{
	Use:   "server",
	Short: "Start the BSV-21 overlay API server",
	Long: `Start the HTTP API server providing lookup, streaming endpoints,
and integrated transaction processing for BSV-21 tokens.`,
	Run: runServer,
}

func init() {
	// Load .env file
	godotenv.Load(".env")

	// Parse PORT from env before flags
	PORT, _ = strconv.Atoi(os.Getenv("PORT"))

	// Define server command flags with env var defaults
	Command.Flags().IntVarP(&PORT, "port", "p", PORT, "Port to listen on")
	Command.Flags().BoolVarP(&SYNC, "sync", "s", false, "Start sync")
	Command.Flags().BoolVar(&LIBP2P_SYNC, "p2p", os.Getenv("LIBP2P_SYNC") == "true", "Enable LibP2P sync")
	Command.Flags().StringVar(&eventsURL, "events", os.Getenv("EVENTS_URL"), "Event storage URL")
	Command.Flags().StringVar(&beefURL, "beef", os.Getenv("BEEF_URL"), "BEEF storage URL")
	Command.Flags().StringVar(&queueURL, "queue", os.Getenv("QUEUE_URL"), "Queue storage URL")
	Command.Flags().StringVar(&pubsubURL, "pubsub", os.Getenv("PUBSUB_URL"), "PubSub URL")
	Command.Flags().StringVar(&arcURL, "arc-url", os.Getenv("ARC_URL"), "Arc broadcaster URL")
	Command.Flags().StringVar(&arcAPIKey, "arc-key", os.Getenv("ARC_API_KEY"), "Arc API key")
	Command.Flags().StringVar(&arcToken, "arc-token", os.Getenv("ARC_CALLBACK_TOKEN"), "Arc callback token")
	Command.Flags().StringVar(&hostingURL, "hosting", os.Getenv("HOSTING_URL"), "Hosting URL")
	Command.Flags().StringVar(&headersURL, "headers", os.Getenv("HEADERS_URL"), "Block headers service URL")
	Command.Flags().StringVar(&headersKey, "headers-key", os.Getenv("HEADERS_KEY"), "Block headers API key")
	Command.Flags().StringVar(&webhookToken, "webhook-token", os.Getenv("WEBHOOK_TOKEN"), "Webhook authentication token")
}

func runServer(cmd *cobra.Command, args []string) {
	// Apply defaults
	if PORT == 0 {
		PORT = 3000
	}
	if arcURL == "" {
		arcURL = "https://arc.gorillapool.io/v1"
	}
	if headersURL == "" {
		headersURL = "https://mainnet.headers.gorillapool.io"
	}

	// Set up chain tracker
	chaintracker = headers.NewClient(headers.ClientParams{
		Url:    headersURL,
		ApiKey: headersKey,
	})

	broadcast = &broadcaster.Arc{
		ApiUrl:        arcURL,
		ApiKey:        arcAPIKey,
		CallbackToken: &arcToken,
		WaitFor:       broadcaster.ACCEPTED_BY_NETWORK,
	}
	if hostingURL != "" {
		url := fmt.Sprintf("%s/api/v1/arc-ingest", hostingURL)
		broadcast.CallbackUrl = &url
	}

	// Create a context with cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize variables for cleanup
	var store *storage.EventDataStorage
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

	// Create storage using the cleaned up configuration with chaintracker for validation
	var err error
	store, err = config.CreateEventStorage(eventsURL, beefURL, queueURL, pubsubURL, chaintracker)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	// Initialize BSV21 lookup service
	bsv21Lookup, err = lookups.NewBsv21EventsLookup(store)
	if err != nil {
		log.Fatalf("Failed to initialize bsv21 lookup: %v", err)
	}

	// Start headers processor for merkle root reconciliation with 1 minute polling
	go processor.Start(ctx, chaintracker, store, 1*time.Minute)

	// Initialize engine
	e = &engine.Engine{
		Managers: map[string]engine.TopicManager{},
		LookupServices: map[string]engine.LookupService{
			"ls_bsv21": bsv21Lookup,
		},
		SyncConfiguration: map[string]engine.SyncConfiguration{},
		Broadcaster:       broadcast,
		HostingURL:        hostingURL,
		Storage:           store,
		ChainTracker:      chaintracker,
	}

	// Register topic managers for active and whitelisted tokens
	if err := peer.RegisterTopics(ctx, e, store, nil); err != nil {
		log.Fatalf("Failed to register topics: %v", err)
	}

	// Build topic ID list from registered managers
	topicIds := make([]string, 0, len(e.Managers))
	for topicId := range e.Managers {
		topicIds = append(topicIds, topicId)
	}

	// Configure sync settings for all topics using storage-based peer configuration
	if err := config.ConfigureSync(ctx, e, store.GetQueueStorage(), topicIds); err != nil {
		log.Printf("Failed to configure sync: %v", err)
	}

	// Get broadcast peer mapping for transaction submission
	peerTopics, err := config.GetBroadcastPeerTopics(ctx, store.GetQueueStorage(), topicIds)
	if err != nil {
		log.Printf("Failed to get broadcast peer mapping: %v", err)
		peerTopics = make(map[string][]string) // Fallback to empty map
	}

	// Create peer broadcaster
	peerBroadcaster = pubsub.NewPeerBroadcaster(peerTopics)
	log.Printf("Configured peer broadcaster with %d peers", len(peerTopics))

	// Start GASP sync if requested
	if SYNC {
		go func() {
			for {
				log.Println("Starting GASP sync...")

				// Create separate engine for GASP sync with SyncModeFull topic managers
				gaspEngine := &engine.Engine{
					Managers:          map[string]engine.TopicManager{},
					LookupServices:    e.LookupServices,    // Share lookup services
					SyncConfiguration: e.SyncConfiguration, // Share sync configuration
					Broadcaster:       e.Broadcaster,       // Share broadcaster
					HostingURL:        e.HostingURL,        // Share hosting URL
					Storage:           e.Storage,           // Share storage
					ChainTracker:      e.ChainTracker,      // Share chain tracker
				}

				// Create SyncModeFull topic managers for the same topics as main engine
				for topicId := range e.Managers {
					// Extract tokenId from topic (remove "tm_" prefix)
					tokenId := topicId[3:]
					gaspEngine.Managers[topicId] = topics.NewBsv21ValidatedTopicManager(
						topicId,
						store,
						[]string{tokenId},
						topics.SyncModeFull,
					)
				}

				log.Printf("Created GASP engine with %d SyncModeFull topic managers", len(gaspEngine.Managers))

				// First sync any invalidated outputs (e.g., from reorgs)
				for topicId := range gaspEngine.Managers {
					if err := gaspEngine.SyncInvalidatedOutputs(ctx, topicId); err != nil {
						log.Printf("Error syncing invalidated outputs for topic %s: %v", topicId, err)
					}
				}

				// Then perform regular GASP sync
				if err := gaspEngine.StartGASPSync(ctx); err != nil {
					log.Printf("Error starting GASP sync: %v", err)
				}
				select {
				case <-ctx.Done():
					log.Println("GASP sync shutting down...")
					return
				case <-time.After(10 * time.Minute):
					log.Println("Restarting GASP sync...")
				}
			}
		}()

		// Start SSE clients for topics that have SSE enabled
		go func() {
			log.Println("Starting SSE sync...")

			// Get SSE peer mapping using the abstracted config
			ssePeerTopics, sseErr := config.GetSSEPeerTopics(ctx, store.GetQueueStorage(), topicIds)
			if sseErr != nil {
				log.Printf("Failed to get SSE peer mapping: %v", sseErr)
				return
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

			// Build topic list for LibP2P sync (whitelist only)
			queueStore := store.GetQueueStorage()
			whitelistTokens, libp2pErr := queueStore.SMembers(ctx, constants.KeyWhitelist)
			if libp2pErr != nil {
				log.Printf("Failed to get whitelist for LibP2P sync: %v", libp2pErr)
				return
			}

			topics := make([]string, 0, len(whitelistTokens))
			for _, tokenId := range whitelistTokens {
				topic := "tm_" + tokenId
				topics = append(topics, topic)
			}

			// Register LibP2P sync
			libp2pSyncManager, err := sync.RegisterLibP2PSync(&sync.LibP2PSyncConfig{
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

	// Start periodic topic registration updates
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		log.Println("Starting periodic topic registration updates...")

		for {
			select {
			case <-ctx.Done():
				log.Println("Topic registration updater shutting down...")
				return
			case <-ticker.C:
				if err := peer.RegisterTopics(ctx, e, store, peerTopics); err != nil {
					log.Panicf("Failed to update topic registration: %v", err)
				}
			}
		}
	}()

	// Create a new Fiber app
	app := fiber.New(fiber.Config{
		// EnablePrintRoutes: true, // Set this to true to print routes
		ErrorHandler: server.GetErrorHandler(), // Use overlay services error handler for proper HTTP status codes
	})
	app.Use(logger.New())

	// Setup OpenAPI documentation
	setupOpenAPIDocumentation(app)

	onesat := app.Group("/api/1sat")

	// Register common 1sat routes
	routes.RegisterCommonRoutes(onesat, &routes.CommonRoutesConfig{
		Storage:      store,
		ChainTracker: chaintracker,
		Engine:       e,
	})

	// Register SSE streaming routes
	routes.RegisterSSERoutes(onesat, &routes.SSERoutesConfig{
		Storage: store,
		Context: ctx,
	})

	// Register BSV21-specific routes
	bsv21routes.RegisterBSV21Routes(onesat, &bsv21routes.BSV21RoutesConfig{
		Storage:      store,
		ChainTracker: chaintracker,
		Engine:       e,
		BSV21Lookup:  bsv21Lookup,
	})

	// Register enhanced submit route with peer broadcasting
	routes.RegisterSubmitRoutes(app, &routes.SubmitRoutesConfig{
		Engine:          e,
		LibP2PSync:      libp2pSync, // Will be set by LibP2P sync initialization if enabled
		PeerBroadcaster: peerBroadcaster,
	})

	// Register overlay service routes using server pattern (excluding submit route)
	server.RegisterRoutes(app, &server.RegisterRoutesConfig{
		ARCAPIKey:        arcAPIKey,
		ARCCallbackToken: arcToken,
		Engine:           e,
	})

	// Register headers webhook route and webhook with block headers service if hosting URL is configured
	var webhookURL string
	if hostingURL != "" {
		headersRoutes.RegisterWebhookRoutes(onesat, headersRoutes.WebhookConfig{
			HeadersClient: chaintracker,
			EventStorage:  store,
			WebhookToken:  webhookToken, // Can be empty string if not configured
		})

		// Register webhook with block headers service
		webhookURL = fmt.Sprintf("%s/api/1sat/headers/webhook", hostingURL)
		if _, err := chaintracker.RegisterWebhook(ctx, webhookURL, webhookToken); err != nil {
			log.Printf("Failed to register webhook with block headers service: %v", err)
		} else {
			log.Printf("Registered webhook with block headers service: %s", webhookURL)
		}
	}

	// Start the server in a goroutine
	go func() {
		log.Printf("Starting server on port %d...", PORT)
		if err := app.Listen(fmt.Sprintf(":%d", PORT)); err != nil {
			log.Printf("Server error: %v", err)
			cancel()
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()

	// Unregister webhook if it was registered
	if webhookURL != "" {
		log.Printf("Unregistering webhook: %s", webhookURL)
		if err := chaintracker.UnregisterWebhook(context.Background(), webhookURL); err != nil {
			log.Printf("Failed to unregister webhook: %v", err)
		}
	}

	// Shutdown the server
	log.Println("Shutting down HTTP server...")
	if err := app.Shutdown(); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
}
