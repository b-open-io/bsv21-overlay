package peer

import (
	"context"
	"log"

	"github.com/b-open-io/bsv21-overlay/constants"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/overlay/storage"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
)

// RegisterTopics manages topic managers and peer configurations dynamically
func RegisterTopics(ctx context.Context, eng *engine.Engine, store storage.EventDataStorage, peerTopics map[string][]string) error {
	queueStore := store.GetQueueStorage()

	// Load blacklist once for fast O(1) lookups
	blacklist := make(map[string]struct{})
	if blacklisted, err := queueStore.SMembers(ctx, constants.KeyBlacklist); err == nil {
		for _, token := range blacklisted {
			blacklist[token] = struct{}{}
		}
	}

	// Get whitelist tokens
	whitelistTokens, err := queueStore.SMembers(ctx, constants.KeyWhitelist)
	if err != nil {
		log.Printf("Failed to get whitelist: %v", err)
		return err
	}

	// Get active balance tokens
	activeBalances, err := queueStore.ZRangeByScore(ctx, constants.KeyActive, 1, 1e9, 0, 0) // Only positive balances
	if err != nil {
		log.Printf("Failed to get active balances: %v", err)
		return err
	}

	// Build new managers map
	newManagers := make(map[string]engine.TopicManager)

	// Clear and rebuild peer topics map
	for k := range peerTopics {
		delete(peerTopics, k)
	}

	whitelistCount := 0
	activeCount := 0

	// Process whitelist tokens (with full peer configuration)
	for _, tokenId := range whitelistTokens {
		if _, isBlacklisted := blacklist[tokenId]; isBlacklisted {
			continue // Skip blacklisted tokens
		}

		topic := "tm_" + tokenId

		// Reuse existing manager if available
		if existingManager, exists := eng.Managers[topic]; exists {
			newManagers[topic] = existingManager
		} else {
			// Create new topic manager
			newManagers[topic] = topics.NewBsv21ValidatedTopicManager(
				topic,
				store,
				[]string{tokenId},
			)
		}

		// Configure GASP sync peers for this topic
		if gaspPeers, err := GetPeersWithSetting(ctx, store, tokenId, "gasp"); err == nil && len(gaspPeers) > 0 {
			eng.SyncConfiguration[topic] = engine.SyncConfiguration{
				Type:  engine.SyncConfigurationPeers,
				Peers: gaspPeers,
			}
		}

		// Configure broadcast peers for this topic
		if broadcastPeers, err := GetPeersWithSetting(ctx, store, tokenId, "broadcast"); err == nil && len(broadcastPeers) > 0 {
			for _, peer := range broadcastPeers {
				peerTopics[peer] = append(peerTopics[peer], topic)
			}
		}

		whitelistCount++
	}

	// Process active balance tokens (basic topic managers only)
	for _, scoreItem := range activeBalances {
		tokenId := scoreItem.Member

		if _, isBlacklisted := blacklist[tokenId]; isBlacklisted {
			continue // Skip blacklisted tokens
		}

		// Skip if already processed in whitelist
		topic := "tm_" + tokenId
		if _, exists := newManagers[topic]; exists {
			continue
		}

		// Reuse existing manager if available
		if existingManager, exists := eng.Managers[topic]; exists {
			newManagers[topic] = existingManager
		} else {
			// Create new basic topic manager (no peer config)
			newManagers[topic] = topics.NewBsv21ValidatedTopicManager(
				topic,
				store,
				[]string{tokenId},
			)
			log.Printf("Created topic manager for active token: %s (balance: %.0f)", topic, scoreItem.Score)
		}

		activeCount++
	}

	// Replace engine managers with new map
	eng.Managers = newManagers

	log.Printf("Registered %d topic managers (%d whitelist + %d active, %d blacklisted), %d broadcast peers",
		len(newManagers), whitelistCount, activeCount, len(blacklist), len(peerTopics))

	return nil
}