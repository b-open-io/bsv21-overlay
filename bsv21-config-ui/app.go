package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/b-open-io/bsv21-overlay/constants"
	overlayConfig "github.com/b-open-io/overlay/config"
	"github.com/b-open-io/overlay/storage"
	"github.com/joho/godotenv"
	"bsv21-config-ui/config"
)

// App struct
type App struct {
	ctx     context.Context
	storage *storage.EventDataStorage
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	
	// Load environment from parent directory
	godotenv.Load("../.env")
	
	// Create storage using the same configuration as server
	storage, err := overlayConfig.CreateEventStorage(
		os.Getenv("EVENTS_URL"),
		os.Getenv("BEEF_URL"),
		os.Getenv("QUEUE_URL"),
		os.Getenv("PUBSUB_URL"),
		nil, // chaintracker is optional for config UI
	)
	if err != nil {
		log.Printf("Failed to create storage: %v", err)
		return
	}
	
	a.storage = storage
	log.Printf("Connected to storage successfully")
}

// GetWhitelistedTokens returns the list of whitelisted token IDs
func (a *App) GetWhitelistedTokens() ([]string, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("Storage not initialized")
	}
	
	queueStore := a.storage.GetQueueStorage()
	members, err := queueStore.SMembers(a.ctx, constants.KeyWhitelist)
	if err != nil {
		return nil, fmt.Errorf("failed to get whitelist: %v", err)
	}
	
	return members, nil
}

// AddTokenToWhitelist adds a token ID to the whitelist
func (a *App) AddTokenToWhitelist(tokenID string) error {
	if a.storage == nil {
		return fmt.Errorf("Storage not initialized")
	}
	
	if tokenID == "" {
		return fmt.Errorf("token ID cannot be empty")
	}
	
	queueStore := a.storage.GetQueueStorage()
	err := queueStore.SAdd(a.ctx, constants.KeyWhitelist, tokenID)
	if err != nil {
		return fmt.Errorf("failed to add token to whitelist: %v", err)
	}
	
	// Topics are derived from whitelist, no separate topics set needed
	
	return nil
}

// RemoveTokenFromWhitelist removes a token ID from the whitelist
func (a *App) RemoveTokenFromWhitelist(tokenID string) error {
	if a.storage == nil {
		return fmt.Errorf("Storage not initialized")
	}
	
	queueStore := a.storage.GetQueueStorage()
	err := queueStore.SRem(a.ctx, constants.KeyWhitelist, tokenID)
	if err != nil {
		return fmt.Errorf("failed to remove token from whitelist: %v", err)
	}
	
	// Topics are derived from whitelist, no separate topics set needed
	
	return nil
}


// GetTopicPeerConfig returns the peer configuration for a specific token
func (a *App) GetTopicPeerConfig(tokenID string) (*config.TopicPeerConfig, error) {
	if a.storage == nil {
		return nil, fmt.Errorf("Storage not initialized")
	}
	
	key := constants.PeerConfigKeyPrefix + tokenID
	queueStore := a.storage.GetQueueStorage()
	peerData, err := queueStore.HGetAll(a.ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get peer config: %v", err)
	}
	
	peers := make(map[string]config.PeerSettings)
	for peerURL, settingsJSON := range peerData {
		var settings config.PeerSettings
		if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
			return nil, fmt.Errorf("failed to parse peer settings for %s: %v", peerURL, err)
		}
		peers[peerURL] = settings
	}
	
	return &config.TopicPeerConfig{
		Peers: peers,
	}, nil
}

// SetTopicPeerConfig updates the peer configuration for a specific token
func (a *App) SetTopicPeerConfig(tokenID string, topicConfig config.TopicPeerConfig) error {
	if a.storage == nil {
		return fmt.Errorf("Storage not initialized")
	}
	
	key := constants.PeerConfigKeyPrefix + tokenID
	
	queueStore := a.storage.GetQueueStorage()
	
	// Get existing config first
	existingPeers, _ := queueStore.HGetAll(a.ctx, key)
	
	// Remove existing peer configurations
	if len(existingPeers) > 0 {
		peerURLs := make([]string, 0, len(existingPeers))
		for peerURL := range existingPeers {
			peerURLs = append(peerURLs, peerURL)
		}
		err := queueStore.HDel(a.ctx, key, peerURLs...)
		if err != nil {
			return fmt.Errorf("failed to clear existing config: %v", err)
		}
	}
	
	// Set new peer configurations
	for peerURL, settings := range topicConfig.Peers {
		settingsJSON, err := json.Marshal(settings)
		if err != nil {
			return fmt.Errorf("failed to marshal settings for peer %s: %v", peerURL, err)
		}
		
		err = queueStore.HSet(a.ctx, key, peerURL, string(settingsJSON))
		if err != nil {
			return fmt.Errorf("failed to set peer config for %s: %v", peerURL, err)
		}
	}
	
	return nil
}
