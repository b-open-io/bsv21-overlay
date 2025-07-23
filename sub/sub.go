package sub

import (
	"context"
	"log"
	"os"

	"github.com/GorillaPool/go-junglebus"
	"github.com/b-open-io/overlay/subscriber"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

// GetRedisClient returns a Redis client for other commands to use
func GetRedisClient() *redis.Client {
	godotenv.Load("../../.env")
	redisURL := os.Getenv("REDIS")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	}
	return redis.NewClient(opts)
}

func Exec() {
	ctx := context.Background()

	// Load environment configuration
	godotenv.Load("../../.env")

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
		TopicID:   "9cdb5ad57e83efe18804f5910742d804f93ff5bc1a86831a347341b773d629be",
		QueueName: "mnee",
		FromBlock: 883989,
		FromPage:  0,
		QueueSize: 10000000,
		LiteMode:  true,
	}

	// Create and start the subscriber
	sub := subscriber.NewSubscriber(subConfig, redisClient, jbClient)

	// Start subscription (blocks until context cancelled or signal received)
	if err := sub.Start(ctx); err != nil {
		log.Printf("Subscriber stopped: %v", err)
	}
}
