package peer

import (
	"context"
	"log"
	"strings"

	bsv21topics "github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/overlay/storage"
	"github.com/b-open-io/overlay/topics"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
)

// RegisterTopics manages topic managers dynamically based on ActiveTopics cache.
// The activeTopics parameter provides the cached list of active topics (from whitelist/blacklist/active storage).
// This function syncs Engine.Managers with that list, creating new managers as needed.
func RegisterTopics(ctx context.Context, eng *engine.Engine, store *storage.EventDataStorage, activeTopics *topics.ActiveTopics, peerTopics map[string][]string) error {
	// Ensure ActiveTopics refresher is started (idempotent)
	activeTopics.Start(ctx, store.GetQueueStorage())

	// Get current active topics from the cache
	activeList := activeTopics.GetAll()

	// Build new managers map
	newManagers := make(map[string]engine.TopicManager)

	// Track changes for logging
	addedTopics := []string{}
	removedTopics := []string{}

	// Process all active topics
	for _, topic := range activeList {
		// Extract tokenId from topic (remove "tm_" prefix)
		tokenId := strings.TrimPrefix(topic, "tm_")

		// Reuse existing manager if available
		if existingManager, exists := eng.Managers[topic]; exists {
			newManagers[topic] = existingManager
		} else {
			// Create new topic manager
			newManagers[topic] = bsv21topics.NewBsv21ValidatedTopicManager(
				topic,
				store,
				[]string{tokenId},
			)
			addedTopics = append(addedTopics, topic)
			log.Printf("Created topic manager for: %s", topic)
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
