package config

// Redis configuration schema for BSV21 overlay
const (
	// Token whitelist management
	WhitelistKey = "bsv21:whitelist"   // SET: Token IDs that are whitelisted for processing

	// Peer config for topics: "peers:tm_{tokenId}" - HASH
	// Field = peer URL, Value = JSON config {"sse": bool, "gasp": bool, "broadcast": bool}
	PeerConfigKeyPrefix = "peers:tm_"
)

// PeerSettings represents the settings for a specific peer (stored as JSON value in Redis HASH)
type PeerSettings struct {
	SSE       bool `json:"sse"`       // Enable SSE for this peer
	GASP      bool `json:"gasp"`      // Enable GASP for this peer
	Broadcast bool `json:"broadcast"` // Enable broadcast for this peer
}

// TopicPeerConfig represents the complete peer configuration for a topic
type TopicPeerConfig struct {
	Peers map[string]PeerSettings `json:"peers"` // Map of peer URL -> settings
}

// ConfigManager handles Redis-based configuration
type ConfigManager struct {
	redisClient interface{} // Will be replaced with actual Redis client
}