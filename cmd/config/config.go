package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/b-open-io/bsv21-overlay/constants"
	"github.com/b-open-io/overlay/config"
	"github.com/joho/godotenv"
)

// Note: Storage key constants are now defined in constants/keys.go

type PeerSettings struct {
	SSE       bool `json:"sse"`
	GASP      bool `json:"gasp"`
	Broadcast bool `json:"broadcast"`
}

// Package-level variables for configuration
var (
	eventsURL string
	beefURL   string
	queueURL  string
	pubsubURL string
	tokenID   string
	peerURL   string
	sseFlag   bool
	gaspFlag  bool
	broadcastFlag bool
)

func init() {
	// Load .env file first
	godotenv.Load(".env")
	
	// Define all flags with environment variable defaults
	flag.StringVar(&eventsURL, "events", os.Getenv("EVENTS_URL"), "Event storage URL")
	flag.StringVar(&beefURL, "beef", os.Getenv("BEEF_URL"), "BEEF storage URL")
	flag.StringVar(&queueURL, "queue", os.Getenv("QUEUE_URL"), "Queue storage URL")
	flag.StringVar(&pubsubURL, "pubsub", os.Getenv("PUBSUB_URL"), "PubSub URL")
	flag.StringVar(&tokenID, "token", "", "Token ID")
	flag.StringVar(&peerURL, "peer", "", "Peer URL")
	flag.BoolVar(&sseFlag, "sse", false, "Enable SSE")
	flag.BoolVar(&gaspFlag, "gasp", false, "Enable GASP")
	flag.BoolVar(&broadcastFlag, "broadcast", false, "Enable Broadcast")
}

func main() {
	// Check if we have at least one argument for the command
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	
	command := os.Args[1]
	
	// Parse flags from the remaining arguments (after the command)
	flag.CommandLine.Parse(os.Args[2:])

	// Create storage using the same configuration as server.go
	// Config tool doesn't need headers client since it only manages queue storage
	storage, err := config.CreateEventStorage(eventsURL, beefURL, queueURL, pubsubURL, nil)
	if err != nil {
		log.Fatalf("Failed to create storage: %v", err)
	}
	
	queueStore := storage.GetQueueStorage()

	ctx := context.Background()

	switch command {
	case "whitelist-add":
		if tokenID == "" {
			log.Fatal("Token ID is required for whitelist-add")
		}
		err := queueStore.SAdd(ctx, constants.KeyWhitelist, tokenID)
		if err != nil {
			log.Fatalf("Failed to add token to whitelist: %v", err)
		}
		fmt.Printf("Added token %s to whitelist\n", tokenID)

	case "whitelist-remove":
		if tokenID == "" {
			log.Fatal("Token ID is required for whitelist-remove")
		}
		err := queueStore.SRem(ctx, constants.KeyWhitelist, tokenID)
		if err != nil {
			log.Fatalf("Failed to remove token from whitelist: %v", err)
		}
		fmt.Printf("Removed token %s from whitelist\n", tokenID)

	case "whitelist-list":
		tokens, err := queueStore.SMembers(ctx, constants.KeyWhitelist)
		if err != nil {
			log.Fatalf("Failed to get whitelist: %v", err)
		}
		if len(tokens) == 0 {
			fmt.Println("No tokens in whitelist")
		} else {
			fmt.Println("Whitelisted tokens:")
			for _, token := range tokens {
				fmt.Printf("  %s\n", token)
			}
		}

	case "peer-add":
		if tokenID == "" || peerURL == "" {
			log.Fatal("Both token ID and peer URL are required for peer-add")
		}
		key := constants.PeerConfigKeyPrefix + tokenID
		settings := PeerSettings{
			SSE:       sseFlag,
			GASP:      gaspFlag,
			Broadcast: broadcastFlag,
		}
		settingsJSON, err := json.Marshal(settings)
		if err != nil {
			log.Fatalf("Failed to marshal settings: %v", err)
		}
		err = queueStore.HSet(ctx, key, peerURL, string(settingsJSON))
		if err != nil {
			log.Fatalf("Failed to add peer: %v", err)
		}
		fmt.Printf("Added peer %s for token %s with settings: SSE=%t, GASP=%t, Broadcast=%t\n", 
			peerURL, tokenID, sseFlag, gaspFlag, broadcastFlag)

	case "peer-remove":
		if tokenID == "" || peerURL == "" {
			log.Fatal("Both token ID and peer URL are required for peer-remove")
		}
		key := constants.PeerConfigKeyPrefix + tokenID
		err := queueStore.HDel(ctx, key, peerURL)
		if err != nil {
			log.Fatalf("Failed to remove peer: %v", err)
		}
		fmt.Printf("Removed peer %s for token %s\n", peerURL, tokenID)

	case "peer-list":
		if tokenID == "" {
			log.Fatal("Token ID is required for peer-list")
		}
		key := constants.PeerConfigKeyPrefix + tokenID
		peerData, err := queueStore.HGetAll(ctx, key)
		if err != nil {
			log.Fatalf("Failed to get peer config: %v", err)
		}
		if len(peerData) == 0 {
			fmt.Printf("No peers configured for token %s\n", tokenID)
		} else {
			fmt.Printf("Peers for token %s:\n", tokenID)
			for peerURL, settingsJSON := range peerData {
				var settings PeerSettings
				if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
					log.Printf("Warning: failed to parse settings for peer %s: %v", peerURL, err)
					continue
				}
				fmt.Printf("  %s: SSE=%t, GASP=%t, Broadcast=%t\n", 
					peerURL, settings.SSE, settings.GASP, settings.Broadcast)
			}
		}

	case "peer-get":
		if tokenID == "" || peerURL == "" {
			log.Fatal("Both token ID and peer URL are required for peer-get")
		}
		key := constants.PeerConfigKeyPrefix + tokenID
		settingsJSON, err := queueStore.HGet(ctx, key, peerURL)
		if err != nil && err.Error() == "redis: nil" {
			fmt.Printf("Peer %s not found for token %s\n", peerURL, tokenID)
		} else if err != nil {
			log.Fatalf("Failed to get peer config: %v", err)
		} else {
			var settings PeerSettings
			if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
				log.Fatalf("Failed to parse settings: %v", err)
			}
			fmt.Printf("Peer %s for token %s: SSE=%t, GASP=%t, Broadcast=%t\n", 
				peerURL, tokenID, settings.SSE, settings.GASP, settings.Broadcast)
		}

	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("BSV21 Configuration CLI")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  config <command> [options]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  whitelist-add     Add token to whitelist")
	fmt.Println("  whitelist-remove  Remove token from whitelist")
	fmt.Println("  whitelist-list    List all whitelisted tokens")
	fmt.Println("  peer-add          Add peer for a token")
	fmt.Println("  peer-remove       Remove peer for a token")
	fmt.Println("  peer-list         List all peers for a token")
	fmt.Println("  peer-get          Get specific peer settings")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -token <id>       Token ID")
	fmt.Println("  -peer <url>       Peer URL")
	fmt.Println("  -sse              Enable SSE for peer")
	fmt.Println("  -gasp             Enable GASP for peer")
	fmt.Println("  -broadcast        Enable Broadcast for peer")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  # Add token to whitelist")
	fmt.Println("  config whitelist-add -token ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0")
	fmt.Println()
	fmt.Println("  # Add peer with SSE and GASP enabled")
	fmt.Println("  config peer-add -token ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0 -peer https://bsv21.shruggr.cloud -sse -gasp")
	fmt.Println()
	fmt.Println("  # List all peers for a token")
	fmt.Println("  config peer-list -token ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0")
}

