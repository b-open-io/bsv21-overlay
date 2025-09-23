package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/b-open-io/bsv21-overlay/constants"
	"github.com/b-open-io/overlay/config"
	"github.com/b-open-io/overlay/queue"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

type PeerSettings struct {
	SSE       bool `json:"sse"`
	GASP      bool `json:"gasp"`
	Broadcast bool `json:"broadcast"`
}

var (
	// Config command flags
	eventsURL     string
	beefURL       string
	queueURL      string
	pubsubURL     string
	tokenID       string
	peerURL       string
	sseFlag       bool
	gaspFlag      bool
	broadcastFlag bool
)

// Command exports the config command for use in the main CLI
var Command = &cobra.Command{
	Use:   "config",
	Short: "Configuration management for whitelists and peers",
	Long:  `Manage token whitelists and peer configurations for BSV-21 overlay service.`,
}

var whitelistCmd = &cobra.Command{
	Use:   "whitelist",
	Short: "Manage token whitelist",
}

var whitelistAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add token to whitelist",
	RunE:  runWhitelistAdd,
}

var whitelistRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove token from whitelist",
	RunE:  runWhitelistRemove,
}

var whitelistListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all whitelisted tokens",
	RunE:  runWhitelistList,
}

var peerCmd = &cobra.Command{
	Use:   "peer",
	Short: "Manage peer configurations",
}

var peerAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add peer for a token",
	RunE:  runPeerAdd,
}

var peerRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove peer for a token",
	RunE:  runPeerRemove,
}

var peerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all peers for a token",
	RunE:  runPeerList,
}

var peerGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get specific peer settings",
	RunE:  runPeerGet,
}

func init() {
	// Load .env file
	godotenv.Load(".env")

	// Define config command flags with env var defaults
	Command.PersistentFlags().StringVar(&eventsURL, "events", os.Getenv("EVENTS_URL"), "Event storage URL")
	Command.PersistentFlags().StringVar(&beefURL, "beef", os.Getenv("BEEF_URL"), "BEEF storage URL")
	Command.PersistentFlags().StringVar(&queueURL, "queue", os.Getenv("QUEUE_URL"), "Queue storage URL")
	Command.PersistentFlags().StringVar(&pubsubURL, "pubsub", os.Getenv("PUBSUB_URL"), "PubSub URL")
	Command.PersistentFlags().StringVar(&tokenID, "token", "", "Token ID")
	Command.PersistentFlags().StringVar(&peerURL, "peer", "", "Peer URL")
	Command.PersistentFlags().BoolVar(&sseFlag, "sse", false, "Enable SSE")
	Command.PersistentFlags().BoolVar(&gaspFlag, "gasp", false, "Enable GASP")
	Command.PersistentFlags().BoolVar(&broadcastFlag, "broadcast", false, "Enable Broadcast")

	// Build command hierarchy
	Command.AddCommand(whitelistCmd)
	whitelistCmd.AddCommand(whitelistAddCmd, whitelistRemoveCmd, whitelistListCmd)

	Command.AddCommand(peerCmd)
	peerCmd.AddCommand(peerAddCmd, peerRemoveCmd, peerListCmd, peerGetCmd)
}

func getQueueStorage() (queue.QueueStorage, error) {
	// Create storage using the same configuration as server
	// Config tool doesn't need headers client since it only manages queue storage
	store, err := config.CreateEventStorage(eventsURL, beefURL, queueURL, pubsubURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %v", err)
	}
	return store.GetQueueStorage(), nil
}

func runWhitelistAdd(cmd *cobra.Command, args []string) error {
	if tokenID == "" {
		return fmt.Errorf("token ID is required (use --token)")
	}

	queueStore, err := getQueueStorage()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := queueStore.SAdd(ctx, constants.KeyWhitelist, tokenID); err != nil {
		return fmt.Errorf("failed to add token to whitelist: %v", err)
	}

	fmt.Printf("Added token %s to whitelist\n", tokenID)
	return nil
}

func runWhitelistRemove(cmd *cobra.Command, args []string) error {
	if tokenID == "" {
		return fmt.Errorf("token ID is required (use --token)")
	}

	queueStore, err := getQueueStorage()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := queueStore.SRem(ctx, constants.KeyWhitelist, tokenID); err != nil {
		return fmt.Errorf("failed to remove token from whitelist: %v", err)
	}

	fmt.Printf("Removed token %s from whitelist\n", tokenID)
	return nil
}

func runWhitelistList(cmd *cobra.Command, args []string) error {
	queueStore, err := getQueueStorage()
	if err != nil {
		return err
	}

	ctx := context.Background()
	tokens, err := queueStore.SMembers(ctx, constants.KeyWhitelist)
	if err != nil {
		return fmt.Errorf("failed to get whitelist: %v", err)
	}

	if len(tokens) == 0 {
		fmt.Println("No tokens in whitelist")
	} else {
		fmt.Println("Whitelisted tokens:")
		for _, token := range tokens {
			fmt.Printf("  %s\n", token)
		}
	}
	return nil
}

func runPeerAdd(cmd *cobra.Command, args []string) error {
	if tokenID == "" || peerURL == "" {
		return fmt.Errorf("both token ID and peer URL are required (use --token and --peer)")
	}

	queueStore, err := getQueueStorage()
	if err != nil {
		return err
	}

	ctx := context.Background()
	key := constants.PeerConfigKeyPrefix + tokenID
	settings := PeerSettings{
		SSE:       sseFlag,
		GASP:      gaspFlag,
		Broadcast: broadcastFlag,
	}

	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %v", err)
	}

	if err := queueStore.HSet(ctx, key, peerURL, string(settingsJSON)); err != nil {
		return fmt.Errorf("failed to add peer: %v", err)
	}

	fmt.Printf("Added peer %s for token %s with settings: SSE=%t, GASP=%t, Broadcast=%t\n",
		peerURL, tokenID, sseFlag, gaspFlag, broadcastFlag)
	return nil
}

func runPeerRemove(cmd *cobra.Command, args []string) error {
	if tokenID == "" || peerURL == "" {
		return fmt.Errorf("both token ID and peer URL are required (use --token and --peer)")
	}

	queueStore, err := getQueueStorage()
	if err != nil {
		return err
	}

	ctx := context.Background()
	key := constants.PeerConfigKeyPrefix + tokenID
	if err := queueStore.HDel(ctx, key, peerURL); err != nil {
		return fmt.Errorf("failed to remove peer: %v", err)
	}

	fmt.Printf("Removed peer %s for token %s\n", peerURL, tokenID)
	return nil
}

func runPeerList(cmd *cobra.Command, args []string) error {
	if tokenID == "" {
		return fmt.Errorf("token ID is required (use --token)")
	}

	queueStore, err := getQueueStorage()
	if err != nil {
		return err
	}

	ctx := context.Background()
	key := constants.PeerConfigKeyPrefix + tokenID
	peerData, err := queueStore.HGetAll(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to get peer config: %v", err)
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
	return nil
}

func runPeerGet(cmd *cobra.Command, args []string) error {
	if tokenID == "" || peerURL == "" {
		return fmt.Errorf("both token ID and peer URL are required (use --token and --peer)")
	}

	queueStore, err := getQueueStorage()
	if err != nil {
		return err
	}

	ctx := context.Background()
	key := constants.PeerConfigKeyPrefix + tokenID
	settingsJSON, err := queueStore.HGet(ctx, key, peerURL)
	if err != nil && err.Error() == "redis: nil" {
		fmt.Printf("Peer %s not found for token %s\n", peerURL, tokenID)
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get peer config: %v", err)
	}

	var settings PeerSettings
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return fmt.Errorf("failed to parse settings: %v", err)
	}

	fmt.Printf("Peer %s for token %s: SSE=%t, GASP=%t, Broadcast=%t\n",
		peerURL, tokenID, settings.SSE, settings.GASP, settings.Broadcast)
	return nil
}