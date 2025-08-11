package main

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"
	
	// Import generated docs
	_ "github.com/b-open-io/bsv21-overlay/docs"
)

// @title BSV21 Overlay API
// @version 1.0
// @description API for BSV21 token transactions, providing real-time event streaming, lookups, and SPV validation
// @termsOfService http://swagger.io/terms/

// @tag.name 1sat-bsv21
// @tag.description BSV21 token-specific operations under the 1sat namespace
// @tag.name 1sat-blocks
// @tag.description Blockchain block operations under the 1sat namespace
// @tag.name 1sat-events
// @tag.description Event querying and streaming under the 1sat namespace
// @tag.name overlay
// @tag.description Core overlay service operations
// @tag.name webhooks
// @tag.description Webhook endpoints for external integrations

// @contact.name API Support
// @contact.url http://www.swagger.io/support
// @contact.email support@swagger.io

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @BasePath /

// @schemes http https

// BSV21EventResponse represents an event response
// @Description BSV21 event data
type BSV21EventResponse struct {
	Outpoint string                 `json:"outpoint" example:"ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127.0"`
	Score    float64                `json:"score" example:"902578.000001234"`
	Data     map[string]interface{} `json:"data,omitempty"`
}

// BSV21BalanceResponse represents a balance response
// @Description BSV21 token balance for an address
type BSV21BalanceResponse struct {
	Balance   uint64 `json:"balance" example:"1000000"`
	UtxoCount int    `json:"utxoCount" example:"5"`
}

// BlockHeaderResponse represents a block header
// @Description Bitcoin block header information
type BlockHeaderResponse struct {
	Height            uint32 `json:"height" example:"902578"`
	Hash              string `json:"hash" example:"000000000000000000034b7f5e7e5e5a5b5c5d5e5f5g5h5i5j5k5l5m5n5o5p"`
	Version           int32  `json:"version" example:"536870912"`
	PrevBlockHash     string `json:"prevBlockHash" example:"000000000000000000012a3b4c5d6e7f8g9h0i1j2k3l4m5n6o7p8q9r0s1t2u3"`
	MerkleRoot        string `json:"merkleRoot" example:"1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"`
	CreationTimestamp uint32 `json:"creationTimestamp" example:"1699564800"`
	DifficultyTarget  uint32 `json:"difficultyTarget" example:"403014710"`
	Nonce             uint32 `json:"nonce" example:"1234567890"`
}

// BSV21BlockDataResponse represents block data with transactions
// @Description BSV21 transactions for a specific block
type BSV21BlockDataResponse struct {
	Block        BlockHeaderResponse      `json:"block"`
	Transactions []map[string]interface{} `json:"transactions"`
}

// BSV21TokenDetailsResponse represents token mint details
// @Description BSV21 token mint transaction details
type BSV21TokenDetailsResponse struct {
	ID   string `json:"id" example:"ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0"`
	TxID string `json:"txid" example:"ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127"`
	Vout uint32 `json:"vout" example:"0"`
	Op   string `json:"op" example:"deploy+mint"`
	Amt  string `json:"amt" example:"2100000000000000"`
	Sym  string `json:"sym,omitempty" example:"SHUA"`
	Dec  uint8  `json:"dec,omitempty" example:"0"`
	Icon string `json:"icon,omitempty" example:"https://example.com/token-icon.png"`
}

// ErrorResponse represents an error response
// @Description Error response returned by the API
type ErrorResponse struct {
	Message string `json:"message" example:"Invalid token ID format"`
}

// setupSwagger configures Swagger UI
func setupSwagger(app *fiber.App) {
	// Swagger UI route
	app.Get("/swagger/*", swagger.HandlerDefault)
}

// Routes documentation

// GetOverlayEvents godoc
// @Summary Get all overlay events
// @Description Retrieve all overlay events (both spent and unspent) for a specific event pattern
// @Tags 1sat-events
// @Accept json
// @Produce json
// @Param event path string true "Event pattern: id:tokenId, sym:SYMBOL, p2pkh:address:tokenId, cos:address:tokenId, ltm:tokenId, pow20:tokenId, list:address"
// @Param from query number false "Starting score (for pagination)"
// @Param limit query integer false "Maximum number of results (default 100, max 1000)"
// @Success 200 {array} BSV21EventResponse
// @Failure 500 {object} ErrorResponse
// @Router /1sat/events/{event} [get]
func GetOverlayEventsDocs() {}

// GetUnspentOverlayEvents godoc
// @Summary Get unspent overlay events
// @Description Retrieve only unspent overlay events for a specific event pattern
// @Tags 1sat-events
// @Accept json
// @Produce json
// @Param event path string true "Event pattern: id:tokenId, sym:SYMBOL, p2pkh:address:tokenId, cos:address:tokenId, ltm:tokenId, pow20:tokenId, list:address"
// @Param from query number false "Starting score (for pagination)"
// @Param limit query integer false "Maximum number of results (default 100, max 1000)"
// @Success 200 {array} BSV21EventResponse
// @Failure 500 {object} ErrorResponse
// @Router /1sat/events/{event}/unspent [get]
func GetUnspentOverlayEventsDocs() {}

// GetBSV21TokenDetails godoc
// @Summary Get BSV21 token details
// @Description Get the mint transaction details for a specific BSV21 token
// @Tags 1sat-bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID (ordinal format: txid_vout)"
// @Success 200 {object} BSV21TokenDetailsResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /1sat/bsv21/{tokenId} [get]
func GetBSV21TokenDetailsDocs() {}

// GetBSV21Balance godoc
// @Summary Get BSV21 token balance
// @Description Get the balance of a BSV21 token for a specific address
// @Tags 1sat-bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID (ordinal format: txid_vout)"
// @Param lockType path string true "Lock type (p2pkh, cos, ltm, pow20, etc.)"
// @Param address path string true "Bitcoin address"
// @Success 200 {object} BSV21BalanceResponse
// @Failure 500 {object} ErrorResponse
// @Router /1sat/bsv21/{tokenId}/{lockType}/{address}/balance [get]
func GetBSV21BalanceDocs() {}

// GetBlockTip godoc
// @Summary Get current block tip
// @Description Get the current blockchain tip with full header information
// @Tags 1sat-blocks
// @Accept json
// @Produce json
// @Success 200 {object} BlockHeaderResponse
// @Failure 500 {object} ErrorResponse
// @Router /1sat/block/tip [get]
func GetBlockTipDocs() {}

// GetBlockByHeight godoc
// @Summary Get block by height
// @Description Get block header information for a specific height
// @Tags 1sat-blocks
// @Accept json
// @Produce json
// @Param height path integer true "Block height"
// @Success 200 {object} BlockHeaderResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /1sat/block/{height} [get]
func GetBlockByHeightDocs() {}

// GetBSV21BlockData godoc
// @Summary Get BSV21 transactions for a block
// @Description Get all BSV21 transactions for a specific token at a given block height
// @Tags 1sat-bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID (ordinal format: txid_vout)"
// @Param height path integer true "Block height"
// @Success 200 {object} BSV21BlockDataResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /1sat/bsv21/{tokenId}/block/{height} [get]
func GetBSV21BlockDataDocs() {}

// SubscribeToTopics godoc
// @Summary Subscribe to BSV21 events
// @Description Server-Sent Events (SSE) stream for real-time BSV21 events
// @Tags 1sat-events
// @Produce text/event-stream
// @Param topics path string true "Comma-separated list of topics to subscribe to"
// @Param Last-Event-ID header string false "Resume from this event ID (score)"
// @Success 200 {string} string "SSE stream"
// @Router /1sat/subscribe/{topics} [get]
func SubscribeToTopicsDocs() {}

// SubmitTransaction godoc
// @Summary Submit tagged BEEF transaction
// @Description Submit a tagged BEEF transaction for processing by the overlay
// @Tags overlay
// @Accept application/octet-stream
// @Param x-topics header string true "Comma-separated list of topics"
// @Param beef body string true "BEEF transaction data (binary)"
// @Success 200 {object} map[string]string
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /submit [post]
func SubmitTransactionDocs() {}

// LookupEvents godoc
// @Summary Query overlay events
// @Description Query overlay events with complex filters via POST
// @Tags overlay
// @Accept json
// @Produce json
// @Param query body object true "Lookup query with event filters"
// @Success 200 {array} BSV21EventResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /lookup [post]
func LookupEventsDocs() {}

// ARCIngest godoc
// @Summary ARC webhook for transaction status
// @Description Webhook endpoint for ARC transaction status updates
// @Tags webhooks
// @Accept json
// @Param status body object true "Transaction status update from ARC"
// @Success 200 {object} map[string]string
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /arc-ingest [post]
func ARCIngestDocs() {}