package constants

// Storage key constants for BSV21 overlay service
// These keys are used with the EventDataStorage interface (Redis-like operations)
const (
	// Token and balance management
	KeyActive    = "bsv21:active"      // Sorted set: tokenId -> current_balance
	KeyBlacklist = "bsv21:blacklist"   // Set: blacklisted tokenIds to skip
	KeyWhitelist = "bsv21:whitelist"   // Set: Token IDs that are whitelisted for processing

	// Peer configuration keys
	PeerConfigKeyPrefix = "peers:tm_"  // Hash key prefix for peer configs: "peers:tm_{tokenId}"
)