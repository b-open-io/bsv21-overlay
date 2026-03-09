package peer

import (
	"context"
	"log"

	"github.com/b-open-io/bsv21-overlay/constants"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/overlay/queue"
	"github.com/b-open-io/overlay/storage"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
)

// RegisterTopics manages topic managers dynamically based on whitelist, blacklist, and active tokens
func RegisterTopics(ctx context.Context, eng *engine.Engine, store *storage.EventDataStorage, peerTopics map[string][]string) error {
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
	minScore := float64(1)
	activeBalances, err := queueStore.ZRange(ctx, constants.KeyActive, queue.ScoreRange{
		Min: &minScore, // Only positive balances
	})
	if err != nil {
		log.Printf("Failed to get active balances: %v", err)
		return err
	}

	// Build new managers map
	newManagers := make(map[string]engine.TopicManager)

	// Track changes for logging
	addedTopics := []string{}
	removedTopics := []string{}

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
			addedTopics = append(addedTopics, topic)
		}

		// Peer configuration is now handled by config.ConfigureSync and config.GetBroadcastPeerTopics
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
			addedTopics = append(addedTopics, topic)
			log.Printf("Created topic manager for active token: %s (balance: %.0f)", topic, scoreItem.Score)
		}
	}

	// Find removed topics
	for existingTopic := range eng.Managers {
		if _, stillExists := newManagers[existingTopic]; !stillExists {
			removedTopics = append(removedTopics, existingTopic)
		}
	}

	// Replace engine managers with new map
	eng.Managers = newManagers

	// Log only changes
	if len(addedTopics) > 0 {
		log.Printf("Added %d topic managers: %v", len(addedTopics), addedTopics)
	}
	if len(removedTopics) > 0 {
		log.Printf("Removed %d topic managers: %v", len(removedTopics), removedTopics)
	}

	return nil
}
