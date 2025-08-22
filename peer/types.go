package peer

// Constants for peer configuration
const (
	ConfigKeyPrefix = "peers:tm_"
)

// PeerSettings defines the configuration for a peer's capabilities
type PeerSettings struct {
	SSE       bool `json:"sse"`       // Server-Sent Events support
	GASP      bool `json:"gasp"`      // GASP protocol support
	Broadcast bool `json:"broadcast"` // Transaction broadcasting support
}