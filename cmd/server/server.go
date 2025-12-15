package server

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/b-open-io/bsv21-overlay/constants"
	"github.com/b-open-io/bsv21-overlay/lookups"
	"github.com/b-open-io/bsv21-overlay/peer"
	bsv21routes "github.com/b-open-io/bsv21-overlay/routes"
	bsv21topics "github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/overlay/pubsub"
	"github.com/b-open-io/overlay/queue"
	"github.com/b-open-io/overlay/routes"
	"github.com/b-open-io/overlay/storage"
	"github.com/b-open-io/overlay/sync"
	"github.com/b-open-io/overlay/topics"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-overlay-services/pkg/server"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

const (
	// GASPRestartInterval is how long to wait before restarting GASP sync
	GASPRestartInterval = 5 * time.Minute
	// TopicUpdateInterval is how often to update topic registrations
	TopicUpdateInterval = 30 * time.Second
)

// CLI-only flags (not from config file/env)
var (
	syncFlag      bool
	pprofPortFlag string
	libp2pFlag    bool
)

// Runtime state
var (
	sseSyncManager    *sync.SSESyncManager
	libp2pSyncManager *sync.LibP2PSyncManager
	libp2pSync        *pubsub.LibP2PSync
	peerBroadcaster   *pubsub.PeerBroadcaster
	e                 *engine.Engine
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
	// Load .env file for backward compatibility
	godotenv.Load(".env")

	// Define CLI-only flags (not from config file)
	Command.Flags().BoolVarP(&syncFlag, "sync", "s", false, "Start sync")
	Command.Flags().StringVar(&pprofPortFlag, "pprof", "", "pprof HTTP server port (empty to disable)")
	Command.Flags().BoolVar(&libp2pFlag, "p2p", false, "Enable LibP2P sync")
}

func runServer(cmd *cobra.Command, args []string) {
	// Load configuration
	cfg, err := Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Override config with CLI flags if provided
	if pprofPortFlag != "" {
		cfg.Server.PProfPort = pprofPortFlag
	}
	if cmd.Flags().Changed("p2p") {
		cfg.Sync.LibP2P = libp2pFlag
	}

	// Configure log level
	var slogLevel slog.Level
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn", "warning":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	slogLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slogLevel}))
	slog.SetDefault(slogLogger)

	// Create a context with cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Start pprof server if enabled
	if cfg.Server.PProfPort != "" {
		go func() {
			log.Printf("Starting pprof server on port %s", cfg.Server.PProfPort)
			if err := http.ListenAndServe(":"+cfg.Server.PProfPort, nil); err != nil {
				log.Printf("pprof server failed: %v", err)
			}
		}()
	}

	// Initialize services
	log.Println("Initializing services...")
	services, err := cfg.Initialize(ctx, slogLogger, nil)
	if err != nil {
		log.Fatalf("Failed to initialize services: %v", err)
	}

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

		// Close services
		services.Close()

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

	// Initialize BSV21 lookup service
	bsv21Lookup, err := lookups.NewBsv21EventsLookup(services.Store)
	if err != nil {
		log.Fatalf("Failed to initialize bsv21 lookup: %v", err)
	}

	// Create ActiveTopics cache for topic validation
	activeTopics := topics.NewActiveTopics()
	activeTopics.Start(ctx, services.Store.GetQueueStorage())

	// Subscribe to new block notifications
	// Note: ChainManager starts P2P subscription automatically during initialization
	blockChan := services.Chaintracks.Subscribe(ctx)

	// Start block processor to handle new blocks from P2P
	go func() {
		log.Println("Starting block processor...")

		for {
			select {
			case <-ctx.Done():
				log.Println("Block processor shutting down...")
				return
			case blockHeader := <-blockChan:
				if blockHeader == nil {
					continue
				}

				log.Printf("New block received: height=%d hash=%s", blockHeader.Height, blockHeader.Hash.String())

				// Reconcile validated merkle roots for this new block
				if err := services.Store.ReconcileValidatedMerkleRoots(ctx); err != nil {
					log.Printf("Error reconciling validated outputs: %v", err)
				}
			}
		}
	}()

	// Initialize engine
	e = &engine.Engine{
		Managers: map[string]engine.TopicManager{},
		LookupServices: map[string]engine.LookupService{
			"ls_bsv21": bsv21Lookup,
		},
		SyncConfiguration: map[string]engine.SyncConfiguration{},
		Broadcaster:       services.Broadcaster,
		HostingURL:        cfg.Hosting.URL,
		Storage:           services.Store,
		ChainTracker:      services.Chaintracks,
	}

	// Register topic managers for active and whitelisted tokens
	if err := peer.RegisterTopics(ctx, e, services.Store, activeTopics, nil); err != nil {
		log.Fatalf("Failed to register topics: %v", err)
	}

	// Build topic ID list from registered managers
	topicIds := make([]string, 0, len(e.Managers))
	for topicId := range e.Managers {
		topicIds = append(topicIds, topicId)
	}

	// Configure sync settings for all topics using storage-based peer configuration
	if err := sync.ConfigureSync(ctx, e, services.Store.GetQueueStorage(), topicIds); err != nil {
		log.Printf("Failed to configure sync: %v", err)
	}

	// Get broadcast peer mapping for transaction submission
	peerTopics, err := sync.GetBroadcastPeerTopics(ctx, services.Store.GetQueueStorage(), topicIds)
	if err != nil {
		log.Printf("Failed to get broadcast peer mapping: %v", err)
		peerTopics = make(map[string][]string) // Fallback to empty map
	}

	// Create peer broadcaster
	peerBroadcaster = pubsub.NewPeerBroadcaster(peerTopics)
	log.Printf("Configured peer broadcaster with %d peers", len(peerTopics))

	// Start GASP sync if requested
	if syncFlag {
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
					gaspEngine.Managers[topicId] = bsv21topics.NewBsv21ValidatedTopicManager(
						topicId,
						services.Store,
						[]string{tokenId},
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
				case <-time.After(GASPRestartInterval):
					log.Println("Restarting GASP sync...")
				}
			}
		}()

		// Start SSE clients for topics that have SSE enabled
		go func() {
			log.Println("Starting SSE sync...")

			// Get SSE peer mapping using the abstracted config
			ssePeerTopics, sseErr := sync.GetSSEPeerTopics(ctx, services.Store.GetQueueStorage(), topicIds)
			if sseErr != nil {
				log.Printf("Failed to get SSE peer mapping: %v", sseErr)
				return
			}

			// Register SSE sync
			var err error
			sseSyncManager, err = sync.RegisterSSESync(&sync.SSESyncConfig{
				Engine:     e,
				Storage:    services.Store,
				PeerTopics: ssePeerTopics,
				Context:    ctx,
			})
			if err != nil {
				log.Printf("Error starting SSE sync: %v", err)
			}
		}()
	}

	// Start LibP2P sync if requested
	if cfg.Sync.LibP2P {
		go func() {
			log.Println("Starting LibP2P sync...")

			// Build topic list for LibP2P sync (whitelist only)
			queueStore := services.Store.GetQueueStorage()
			whitelistTokens, libp2pErr := queueStore.SMembers(ctx, []byte(constants.KeyWhitelist))
			if libp2pErr != nil {
				log.Printf("Failed to get whitelist for LibP2P sync: %v", libp2pErr)
				return
			}

			topicsList := make([]string, 0, len(whitelistTokens))
			for _, tokenId := range whitelistTokens {
				topic := "tm_" + string(tokenId)
				topicsList = append(topicsList, topic)
			}

			// Register LibP2P sync
			libp2pSyncManager, err := sync.RegisterLibP2PSync(&sync.LibP2PSyncConfig{
				Engine:  e,
				Storage: services.Store,
				Topics:  topicsList,
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
		ticker := time.NewTicker(TopicUpdateInterval)
		defer ticker.Stop()

		log.Println("Starting periodic topic registration updates...")

		for {
			select {
			case <-ctx.Done():
				log.Println("Topic registration updater shutting down...")
				return
			case <-ticker.C:
				if err := peer.RegisterTopics(ctx, e, services.Store, activeTopics, peerTopics); err != nil {
					log.Panicf("Failed to update topic registration: %v", err)
				}
			}
		}
	}()

	// Create a new Fiber app
	app := fiber.New(fiber.Config{
		ErrorHandler: server.GetErrorHandler(), // Use overlay services error handler for proper HTTP status codes
	})
	app.Use(logger.New())

	// Setup OpenAPI documentation
	setupOpenAPIDocumentation(app)

	onesat := app.Group("/api/1sat")

	// Register common 1sat routes
	routes.RegisterRoutes(onesat, &routes.RoutesConfig{
		Storage:      services.Store,
		ChainTracker: services.Chaintracks,
		Engine:       e,
	})

	// Register SSE streaming routes with BSV21-specific catchup
	routes.RegisterSSERoutes(onesat, &routes.SSERoutesConfig{
		SSEManager: services.SSEManager,
		Catchup:    createBSV21Catchup(services.Store),
		Context:    ctx,
	})

	// Register BSV21-specific routes
	bsv21routes.RegisterBSV21Routes(onesat, &bsv21routes.BSV21RoutesConfig{
		Storage:      services.Store,
		ChainTracker: services.Chaintracks,
		ActiveTopics: activeTopics,
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
		ARCAPIKey:        cfg.Arc.APIKey,
		ARCCallbackToken: cfg.Arc.Token,
		Engine:           e,
	})

	// Start the server in a goroutine
	go func() {
		log.Printf("Starting server on port %d...", cfg.Server.Port)
		if err := app.Listen(fmt.Sprintf(":%d", cfg.Server.Port)); err != nil {
			log.Printf("Server error: %v", err)
			cancel()
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()

	// Note: ChainManager P2P subscription stops automatically via context cancellation

	// Shutdown the server
	log.Println("Shutting down HTTP server...")
	if err := app.Shutdown(); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
}

// createBSV21Catchup returns a catchup function that merges events from multiple keys
// using cursor-based iteration to maintain score order without sorting.
func createBSV21Catchup(store *storage.EventDataStorage) routes.CatchupFunc {
	return func(ctx context.Context, events []string, fromScore float64) ([]queue.ScoredMember, error) {
		// Build list of (topic, event) pairs
		type topicEvent struct {
			topic string
			event string
		}
		pairs := make([]topicEvent, 0, len(events))
		for _, event := range events {
			var topic string
			if strings.HasPrefix(event, "tm_") {
				topic = event
			} else {
				parts := strings.Split(event, ":")
				if len(parts) >= 3 {
					tokenId := parts[len(parts)-1]
					topic = "tm_" + tokenId
				} else {
					continue
				}
			}
			pairs = append(pairs, topicEvent{topic: topic, event: event})
		}

		if len(pairs) == 0 {
			return nil, nil
		}

		// Initialize cursors for each event
		type cursor struct {
			members []queue.ScoredMember
			pos     int
			pair    topicEvent
		}
		cursors := make([]*cursor, len(pairs))
		for i, pair := range pairs {
			members, err := store.LookupEventScores(ctx, pair.topic, pair.event, fromScore)
			if err != nil {
				log.Printf("Catchup lookup error for %s: %v", pair.event, err)
				members = nil
			}
			cursors[i] = &cursor{members: members, pos: 0, pair: pair}
		}

		// Merge using cursor-based iteration
		var result []queue.ScoredMember
		for {
			// Find cursor with lowest score
			var best *cursor
			var bestScore float64
			for _, c := range cursors {
				if c.pos >= len(c.members) {
					continue
				}
				score := c.members[c.pos].Score
				if best == nil || score < bestScore {
					best = c
					bestScore = score
				}
			}
			if best == nil {
				break // All cursors exhausted
			}
			result = append(result, best.members[best.pos])
			best.pos++
		}

		return result, nil
	}
}
