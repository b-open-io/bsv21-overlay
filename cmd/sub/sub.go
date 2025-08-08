package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/GorillaPool/go-junglebus"
	"github.com/b-open-io/overlay/subscriber"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

func main() {
	// Command line flags
	var (
		topicID string
		envFile string
	)

	flag.StringVar(&topicID, "topic", "", "Topic ID to subscribe to (required)")
	flag.StringVar(&envFile, "env", "../../.env", "Path to .env file")
	flag.Parse()

	// Load environment configuration
	godotenv.Load(envFile)

	// Check environment variable if flag not provided
	if topicID == "" {
		topicID = os.Getenv("BSV21_TOPIC")
		if topicID == "" {
			log.Fatal("Topic ID is required. Use -topic flag or set BSV21_TOPIC environment variable")
		}
	}

	// Fixed configuration values
	const (
		fromBlock = 811302
		fromPage  = 0
		queueName = "bsv21"
		queueSize = 10000000
		liteMode  = true
	)

	// Set up Redis connection
	redisURL := os.Getenv("REDIS")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	}
	redisClient := redis.NewClient(opts)
	defer redisClient.Close()

	// Set up JungleBus connection
	jungleBusURL := os.Getenv("JUNGLEBUS")
	if jungleBusURL == "" {
		log.Fatalf("JUNGLEBUS environment variable is required")
	}
	jbClient, err := junglebus.New(junglebus.WithHTTP(jungleBusURL))
	if err != nil {
		log.Fatalf("Failed to create JungleBus client: %v", err)
	}

	// Configure the subscriber
	subConfig := &subscriber.SubscriberConfig{
		TopicID:   topicID,
		QueueName: queueName,
		FromBlock: fromBlock,
		FromPage:  fromPage,
		QueueSize: queueSize,
		LiteMode:  liteMode,
	}

	log.Printf("Starting subscriber for topic: %s", topicID)
	log.Printf("Queue: %s, FromBlock: %d, FromPage: %d", queueName, fromBlock, fromPage)

	// Create and start the subscriber
	sub := subscriber.NewSubscriber(subConfig, redisClient, jbClient)

	// Set up context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal, stopping subscriber...")
		cancel()
	}()

	// Start subscription (blocks until context cancelled or signal received)
	if err := sub.Start(ctx); err != nil {
		log.Printf("Subscriber stopped: %v", err)
	}
}
