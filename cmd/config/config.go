package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/b-open-io/overlay/config"
	"github.com/joho/godotenv"
)

const (
	WhitelistKey        = "bsv21:whitelist"
	PeerConfigKeyPrefix = "peers:tm_"
)

type PeerSettings struct {
	SSE       bool `json:"sse"`
	GASP      bool `json:"gasp"`
	Broadcast bool `json:"broadcast"`
}

func main() {
	var (
		tokenID     = flag.String("token", "", "Token ID")
		peerURL     = flag.String("peer", "", "Peer URL")
		sseFlag     = flag.Bool("sse", false, "Enable SSE")
		gaspFlag    = flag.Bool("gasp", false, "Enable GASP")
		broadcastFlag = flag.Bool("broadcast", false, "Enable Broadcast")
	)
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}
	
	command := args[0]

	// Load environment
	godotenv.Load(".env")

	// Create storage using the same configuration as server
	storage, err := config.CreateEventStorage(
		os.Getenv("EVENTS_URL"), 
		os.Getenv("BEEF_URL"), 
		os.Getenv("PUBSUB_URL"),
	)
	if err != nil {
		log.Fatalf("Failed to create storage: %v", err)
	}

	ctx := context.Background()

	switch command {
	case "whitelist-add":
		if *tokenID == "" {
			log.Fatal("Token ID is required for whitelist-add")
		}
		err := storage.SAdd(ctx, WhitelistKey, *tokenID)
		if err != nil {
			log.Fatalf("Failed to add token to whitelist: %v", err)
		}
		fmt.Printf("Added token %s to whitelist\n", *tokenID)

	case "whitelist-remove":
		if *tokenID == "" {
			log.Fatal("Token ID is required for whitelist-remove")
		}
		err := storage.SRem(ctx, WhitelistKey, *tokenID)
		if err != nil {
			log.Fatalf("Failed to remove token from whitelist: %v", err)
		}
		fmt.Printf("Removed token %s from whitelist\n", *tokenID)

	case "whitelist-list":
		tokens, err := storage.SMembers(ctx, WhitelistKey)
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
		if *tokenID == "" || *peerURL == "" {
			log.Fatal("Both token ID and peer URL are required for peer-add")
		}
		key := PeerConfigKeyPrefix + *tokenID
		settings := PeerSettings{
			SSE:       *sseFlag,
			GASP:      *gaspFlag,
			Broadcast: *broadcastFlag,
		}
		settingsJSON, err := json.Marshal(settings)
		if err != nil {
			log.Fatalf("Failed to marshal settings: %v", err)
		}
		err = storage.HSet(ctx, key, *peerURL, string(settingsJSON))
		if err != nil {
			log.Fatalf("Failed to add peer: %v", err)
		}
		fmt.Printf("Added peer %s for token %s with settings: SSE=%t, GASP=%t, Broadcast=%t\n", 
			*peerURL, *tokenID, *sseFlag, *gaspFlag, *broadcastFlag)

	case "peer-remove":
		if *tokenID == "" || *peerURL == "" {
			log.Fatal("Both token ID and peer URL are required for peer-remove")
		}
		key := PeerConfigKeyPrefix + *tokenID
		err := storage.HDel(ctx, key, *peerURL)
		if err != nil {
			log.Fatalf("Failed to remove peer: %v", err)
		}
		fmt.Printf("Removed peer %s for token %s\n", *peerURL, *tokenID)

	case "peer-list":
		if *tokenID == "" {
			log.Fatal("Token ID is required for peer-list")
		}
		key := PeerConfigKeyPrefix + *tokenID
		peerData, err := storage.HGetAll(ctx, key)
		if err != nil {
			log.Fatalf("Failed to get peer config: %v", err)
		}
		if len(peerData) == 0 {
			fmt.Printf("No peers configured for token %s\n", *tokenID)
		} else {
			fmt.Printf("Peers for token %s:\n", *tokenID)
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
		if *tokenID == "" || *peerURL == "" {
			log.Fatal("Both token ID and peer URL are required for peer-get")
		}
		key := PeerConfigKeyPrefix + *tokenID
		settingsJSON, err := storage.HGet(ctx, key, *peerURL)
		if err != nil && err.Error() == "redis: nil" {
			fmt.Printf("Peer %s not found for token %s\n", *peerURL, *tokenID)
		} else if err != nil {
			log.Fatalf("Failed to get peer config: %v", err)
		} else {
			var settings PeerSettings
			if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
				log.Fatalf("Failed to parse settings: %v", err)
			}
			fmt.Printf("Peer %s for token %s: SSE=%t, GASP=%t, Broadcast=%t\n", 
				*peerURL, *tokenID, settings.SSE, settings.GASP, settings.Broadcast)
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

