package config

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/b-open-io/overlay/beef"
	"github.com/b-open-io/overlay/pubsub"
	"github.com/b-open-io/overlay/queue"
	"github.com/b-open-io/overlay/storage"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/bsv-blockchain/go-sdk/transaction/broadcaster"
)

// Services holds initialized application services.
type Services struct {
	// Core storage
	Store *storage.EventDataStorage

	// Chaintracks for block header validation
	Chaintracks chaintracks.Chaintracks

	// Arc broadcaster for transaction submission
	Broadcaster *broadcaster.Arc

	// SSE manager for streaming
	SSEManager *pubsub.SSEManager

	// Configuration
	Config *Config

	// Logger
	Logger *slog.Logger
}

// Initialize creates and returns all application services.
// If chaintracker is provided, it will be used instead of creating a new one.
func (c *Config) Initialize(ctx context.Context, logger *slog.Logger, chaintracker chaintracks.Chaintracks) (*Services, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// Initialize Chaintracks if not provided
	if chaintracker == nil {
		logger.Info("Initializing Chaintracks")
		var err error
		chaintracker, err = c.Chaintracks.Initialize(ctx, "bsv21", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize chaintracks: %w", err)
		}
	} else {
		logger.Info("Using provided Chaintracks instance")
	}

	// Create BEEF storage
	logger.Info("Initializing BEEF storage", slog.String("url", c.Beef.URL))
	beefStore, err := beef.NewStorage(c.Beef.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create beef storage: %w", err)
	}

	// Initialize queue storage
	logger.Info("Initializing queue storage", slog.String("url", c.Queue.URL))
	queueURL := c.Queue.URL
	if queueURL == "" {
		queueURL = "badger:///tmp/queue"
	}
	queueStorage, err := queue.NewBadgerQueueStorage(queueURL, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize queue storage: %w", err)
	}

	// Initialize pubsub
	logger.Info("Initializing pubsub", slog.String("url", c.PubSub.URL))
	pubsubURL := c.PubSub.URL
	if pubsubURL == "" {
		pubsubURL = "channels://"
	}
	ps, err := pubsub.CreatePubSub(pubsubURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize pubsub: %w", err)
	}

	// Create event storage
	logger.Info("Initializing event storage")
	store := storage.CreateEventDataStorage(queueStorage, ps, beefStore)

	// Initialize Arc broadcaster
	logger.Info("Initializing Arc broadcaster", slog.String("url", c.Arc.URL))
	arcBroadcaster := &broadcaster.Arc{
		ApiUrl:        c.Arc.URL,
		ApiKey:        c.Arc.APIKey,
		CallbackToken: &c.Arc.Token,
		WaitFor:       broadcaster.ACCEPTED_BY_NETWORK,
	}
	if c.Hosting.URL != "" {
		callbackURL := fmt.Sprintf("%s/api/v1/arc-ingest", c.Hosting.URL)
		arcBroadcaster.CallbackUrl = &callbackURL
	}

	// Create SSE manager
	logger.Info("Initializing SSE manager")
	sseManager := pubsub.NewSSEManager(ctx, store.GetPubSub())

	return &Services{
		Store:       store,
		Chaintracks: chaintracker,
		Broadcaster: arcBroadcaster,
		SSEManager:  sseManager,
		Config:      c,
		Logger:      logger,
	}, nil
}

// Close gracefully shuts down all services.
func (s *Services) Close() {
	if s.Store != nil {
		if ps := s.Store.GetPubSub(); ps != nil {
			ps.Close()
		}
	}
}
