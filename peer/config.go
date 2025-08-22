package peer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/b-open-io/bsv21-overlay/constants"
	"github.com/b-open-io/overlay/storage"
)

// LoadConfigFromStorage reads peer configuration from storage for a given token
func LoadConfigFromStorage(ctx context.Context, store storage.EventDataStorage, tokenId string) (map[string]PeerSettings, error) {
	queueStore := store.GetQueueStorage()
	key := constants.PeerConfigKeyPrefix + tokenId
	peerData, err := queueStore.HGetAll(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get peer config for token %s: %v", tokenId, err)
	}

	peers := make(map[string]PeerSettings)
	for peerURL, settingsJSON := range peerData {
		var settings PeerSettings
		if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
			log.Printf("Warning: failed to parse settings for peer %s (token %s): %v", peerURL, tokenId, err)
			continue
		}
		peers[peerURL] = settings
	}

	return peers, nil
}

// GetPeersWithSetting returns peers that have a specific setting enabled for a token
func GetPeersWithSetting(ctx context.Context, store storage.EventDataStorage, tokenId string, settingName string) ([]string, error) {
	peerConfig, err := LoadConfigFromStorage(ctx, store, tokenId)
	if err != nil {
		return nil, err
	}

	var enabledPeers []string
	for peerURL, settings := range peerConfig {
		switch settingName {
		case "sse":
			if settings.SSE {
				enabledPeers = append(enabledPeers, peerURL)
			}
		case "gasp":
			if settings.GASP {
				// Add /api/v1 path for GASP endpoints
				enabledPeers = append(enabledPeers, peerURL+"/api/v1")
			}
		case "broadcast":
			if settings.Broadcast {
				enabledPeers = append(enabledPeers, peerURL)
			}
		}
	}

	return enabledPeers, nil
}