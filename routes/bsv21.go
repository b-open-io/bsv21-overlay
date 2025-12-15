package routes

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/b-open-io/bsv21-overlay/lookups"
	"github.com/b-open-io/overlay/queue"
	"github.com/b-open-io/overlay/routes"
	"github.com/b-open-io/overlay/storage"
	"github.com/b-open-io/overlay/topics"
	"github.com/bsv-blockchain/go-chaintracks/chaintracks"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/gofiber/fiber/v2"
)

// BSV21RoutesConfig holds the configuration for BSV21-specific routes
type BSV21RoutesConfig struct {
	Storage      *storage.EventDataStorage
	ChainTracker chaintracks.Chaintracks
	ActiveTopics *topics.ActiveTopics
	BSV21Lookup  *lookups.Bsv21EventsLookup
}

// BSV21Controller handles BSV21 API requests
type BSV21Controller struct {
	storage      *storage.EventDataStorage
	chaintracker chaintracks.Chaintracks
	activeTopics *topics.ActiveTopics
	bsv21Lookup  *lookups.Bsv21EventsLookup
}

// TokenResponse represents BSV21 token details
// @Description BSV21 token metadata response
type TokenResponse struct {
	ID     string `json:"id" example:"abc123def456_0"`
	Txid   string `json:"txid" example:"abc123def456789..."`
	Vout   uint32 `json:"vout" example:"0"`
	Op     string `json:"op" example:"deploy+mint"`
	Amt    string `json:"amt" example:"1000000"`
	Symbol string `json:"sym,omitempty" example:"TEST"`
	Dec    uint8  `json:"dec,omitempty" example:"8"`
	Icon   string `json:"icon,omitempty" example:"b://..."`
}

// BlockResponse represents block data for a token
// @Description Block data containing token transactions
type BlockResponse struct {
	Block        BlockInfo                   `json:"block"`
	Transactions []*storage.TransactionData `json:"transactions"`
}

// BlockInfo represents block header information
// @Description Block header metadata
type BlockInfo struct {
	Height            uint32 `json:"height" example:"850000"`
	Hash              string `json:"hash" example:"00000000000000000001..."`
	PreviousBlockHash string `json:"previousblockhash" example:"00000000000000000002..."`
	Timestamp         uint32 `json:"timestamp,omitempty" example:"1699999999"`
}

// BalanceResponse represents token balance information
// @Description Token balance for an address
type BalanceResponse struct {
	Balance   uint64 `json:"balance" example:"1000000"`
	UtxoCount int    `json:"utxoCount" example:"5"`
}

// ErrorResponse represents an error response
// @Description Error response with message
type ErrorResponse struct {
	Message string `json:"message" example:"Error description"`
}

// RegisterBSV21Routes registers BSV21-specific API routes
func RegisterBSV21Routes(group fiber.Router, config *BSV21RoutesConfig) {
	if config == nil || config.Storage == nil || config.ChainTracker == nil || config.ActiveTopics == nil || config.BSV21Lookup == nil {
		log.Fatal("RegisterBSV21Routes: config, storage, chaintracker, activeTopics, and bsv21lookup are required")
	}

	// Ensure ActiveTopics refresher is started (idempotent)
	config.ActiveTopics.Start(context.Background(), config.Storage.GetQueueStorage())

	ctrl := &BSV21Controller{
		storage:      config.Storage,
		chaintracker: config.ChainTracker,
		activeTopics: config.ActiveTopics,
		bsv21Lookup:  config.BSV21Lookup,
	}

	// Token details endpoint
	group.Get("/:tokenId", ctrl.GetToken)

	// Block data endpoint
	group.Get("/:tokenId/block/:height", ctrl.GetBlockData)

	// Transaction details endpoint
	group.Get("/:tokenId/tx/:txid", ctrl.GetTransaction)

	// Single address balance endpoint
	group.Get("/:tokenId/:lockType/:address/balance", ctrl.GetAddressBalance)

	// Single address history endpoint
	group.Get("/:tokenId/:lockType/:address/history", ctrl.GetAddressHistory)

	// Single address unspent endpoint
	group.Get("/:tokenId/:lockType/:address/unspent", ctrl.GetAddressUnspent)

	// Multi-address balance endpoint
	group.Post("/:tokenId/:lockType/balance", ctrl.GetMultiAddressBalance)

	// Multi-address history endpoint
	group.Post("/:tokenId/:lockType/history", ctrl.GetMultiAddressHistory)

	// Multi-address unspent endpoint
	group.Post("/:tokenId/:lockType/unspent", ctrl.GetMultiAddressUnspent)
}

// GetToken retrieves BSV21 token details
// @Summary Get BSV21 token details
// @Description Retrieve metadata and details for a BSV21 token by its ID
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Success 200 {object} TokenResponse
// @Failure 400 {object} ErrorResponse "Invalid token ID format"
// @Failure 404 {object} ErrorResponse "Token not found"
// @Failure 503 {object} ErrorResponse "Topic not available"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /bsv21/{tokenId} [get]
func (ctrl *BSV21Controller) GetToken(c *fiber.Ctx) error {
	tokenIdStr := c.Params("tokenId")
	log.Printf("Received request for BSV21 token details: %s", tokenIdStr)

	// Validate tokenId is active
	topic := "tm_" + tokenIdStr
	if !ctrl.activeTopics.IsActive(topic) {
		return c.Status(fiber.StatusServiceUnavailable).JSON(ErrorResponse{
			Message: "Topic not available",
		})
	}

	// Parse the tokenId string into an outpoint
	outpoint, err := transaction.OutpointFromString(tokenIdStr)
	if err != nil {
		log.Printf("Invalid token ID format: %v", err)
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid token ID format",
		})
	}

	// Use the dedicated GetToken method
	tokenData, err := ctrl.bsv21Lookup.GetToken(c.Context(), outpoint)
	if err != nil {
		log.Printf("GetToken error: %v", err)

		// Determine appropriate status code based on error
		if err.Error() == "token not found" {
			return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{
				Message: "Token not found",
			})
		} else if strings.Contains(err.Error(), "not a mint transaction") {
			return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
				Message: err.Error(),
			})
		}

		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve token details",
		})
	}

	return c.JSON(tokenData)
}

// GetBlockData retrieves block data for a token at a specific height
// @Summary Get block data for a token
// @Description Retrieve transactions for a BSV21 token at a specific block height
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Param height path int true "Block height"
// @Success 200 {object} BlockResponse
// @Failure 400 {object} ErrorResponse "Invalid height parameter"
// @Failure 503 {object} ErrorResponse "Topic not available"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /bsv21/{tokenId}/block/{height} [get]
func (ctrl *BSV21Controller) GetBlockData(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	heightStr := c.Params("height")

	// Validate tokenId is active
	topic := "tm_" + tokenId
	if !ctrl.activeTopics.IsActive(topic) {
		return c.Status(fiber.StatusServiceUnavailable).JSON(ErrorResponse{
			Message: "Topic not available",
		})
	}

	// Parse height as uint32
	height64, err := strconv.ParseUint(heightStr, 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid height parameter",
		})
	}
	height := uint32(height64)

	// Get transactions from storage for the specific token's topic at this height
	transactions, err := ctrl.storage.GetTransactionsByTopicAndHeight(c.Context(), topic, height)
	if err != nil {
		log.Printf("GetBlockData error: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to get block data",
		})
	}

	// Fetch block header information from chaintracker
	blockHeader, err := ctrl.chaintracker.GetHeaderByHeight(c.UserContext(), height)
	if err != nil {
		log.Printf("Failed to get block header: %v", err)
		// Continue without header info rather than failing completely
		return c.JSON(BlockResponse{
			Block: BlockInfo{
				Height:            height,
				Hash:              "",
				PreviousBlockHash: "",
			},
			Transactions: transactions,
		})
	}

	// Build response with header and transactions
	return c.JSON(BlockResponse{
		Block: BlockInfo{
			Height:            blockHeader.Height,
			Hash:              blockHeader.Hash.String(),
			PreviousBlockHash: blockHeader.Header.PrevHash.String(),
			Timestamp:         blockHeader.Header.Timestamp,
		},
		Transactions: transactions,
	})
}

// GetTransaction retrieves transaction details for a token
// @Summary Get transaction details
// @Description Retrieve details for a specific transaction within a BSV21 token's topic
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Param txid path string true "Transaction ID (hex)"
// @Param beef query bool false "Include BEEF data in response"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} ErrorResponse "Invalid transaction ID format"
// @Failure 404 {object} ErrorResponse "Transaction not found"
// @Failure 503 {object} ErrorResponse "Topic not available"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /bsv21/{tokenId}/tx/{txid} [get]
func (ctrl *BSV21Controller) GetTransaction(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	txidStr := c.Params("txid")

	log.Printf("Received transaction request for BSV21 tokenId: %s, txid: %s", tokenId, txidStr)

	// Validate tokenId is active
	topic := "tm_" + tokenId
	if !ctrl.activeTopics.IsActive(topic) {
		return c.Status(fiber.StatusServiceUnavailable).JSON(ErrorResponse{
			Message: "Topic not available",
		})
	}

	// Parse the txid string into a hash
	txid, err := chainhash.NewHashFromHex(txidStr)
	if err != nil {
		log.Printf("Invalid transaction ID format: %v", err)
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid transaction ID format",
		})
	}

	// Check if BEEF should be included (optional query parameter)
	includeBeef := c.Query("beef") == "true"

	// Get single transaction from storage with optional BEEF
	transaction, err := ctrl.storage.GetTransactionByTopic(c.Context(), topic, txid, includeBeef)
	if err != nil {
		log.Printf("GetTransactionByTopic error: %v", err)

		// Determine appropriate status code based on error
		if err.Error() == "transaction not found" {
			return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{
				Message: "Transaction not found",
			})
		}

		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve transaction details",
		})
	}

	return c.JSON(transaction)
}

// GetAddressBalance retrieves token balance for a single address
// @Summary Get address token balance
// @Description Get the BSV21 token balance for a specific address
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Param lockType path string true "Lock type (p2pkh, cos, list, etc.)"
// @Param address path string true "Address or identifier"
// @Success 200 {object} BalanceResponse
// @Failure 503 {object} ErrorResponse "Topic not available"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /bsv21/{tokenId}/{lockType}/{address}/balance [get]
func (ctrl *BSV21Controller) GetAddressBalance(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	lockType := c.Params("lockType")
	address := c.Params("address")

	// Validate tokenId is active
	topic := "tm_" + tokenId
	if !ctrl.activeTopics.IsActive(topic) {
		return c.Status(fiber.StatusServiceUnavailable).JSON(ErrorResponse{
			Message: "Topic not available",
		})
	}

	// Build event string from lockType, address, and tokenId
	event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
	log.Printf("Received balance request for token %s, %s address %s", tokenId, lockType, address)

	// Get balance for the single event
	balance, outputs, err := ctrl.bsv21Lookup.GetBalance(c.Context(), []string{event}, topic)
	if err != nil {
		log.Printf("Balance calculation error: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to calculate balance",
		})
	}

	return c.JSON(BalanceResponse{
		Balance:   balance,
		UtxoCount: outputs,
	})
}

// GetAddressHistory retrieves transaction history for a single address
// @Summary Get address transaction history
// @Description Get all BSV21 token transactions (including spent) for a specific address
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Param lockType path string true "Lock type (p2pkh, cos, list, etc.)"
// @Param address path string true "Address or identifier"
// @Param from query number false "Starting score for pagination"
// @Param limit query int false "Maximum number of results"
// @Param rev query bool false "Reverse order"
// @Success 200 {array} storage.IndexedOutput
// @Failure 503 {object} ErrorResponse "Topic not available"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /bsv21/{tokenId}/{lockType}/{address}/history [get]
func (ctrl *BSV21Controller) GetAddressHistory(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	lockType := c.Params("lockType")
	address := c.Params("address")

	// Validate tokenId is active
	topic := "tm_" + tokenId
	if !ctrl.activeTopics.IsActive(topic) {
		return c.Status(fiber.StatusServiceUnavailable).JSON(ErrorResponse{
			Message: "Topic not available",
		})
	}

	// Build event string from lockType, address, and tokenId
	event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
	log.Printf("Received history request for token %s, %s address %s", tokenId, lockType, address)

	// Parse query parameters for paging
	cfg := routes.ParseSearchConfig(c)
	cfg.Keys = [][]byte{[]byte(event)}

	// Get outputs including spent (history includes all)
	topicStore := ctrl.storage.GetStore(topic)
	outputs, err := topicStore.SearchOutputs(c.Context(), cfg, nil, true) // includeSpent=true
	if err != nil {
		log.Printf("History lookup error: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve output history",
		})
	}

	return c.JSON(outputs)
}

// GetAddressUnspent retrieves unspent outputs for a single address
// @Summary Get address unspent outputs
// @Description Get unspent BSV21 token outputs for a specific address
// @Tags bsv21
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Param lockType path string true "Lock type (p2pkh, cos, list, etc.)"
// @Param address path string true "Address or identifier"
// @Success 200 {array} storage.IndexedOutput
// @Failure 503 {object} ErrorResponse "Topic not available"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /bsv21/{tokenId}/{lockType}/{address}/unspent [get]
func (ctrl *BSV21Controller) GetAddressUnspent(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	lockType := c.Params("lockType")
	address := c.Params("address")

	// Validate tokenId is active
	topic := "tm_" + tokenId
	if !ctrl.activeTopics.IsActive(topic) {
		return c.Status(fiber.StatusServiceUnavailable).JSON(ErrorResponse{
			Message: "Topic not available",
		})
	}

	// Build event string from lockType, address, and tokenId
	event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
	log.Printf("Received unspent request for token %s, %s address %s", tokenId, lockType, address)

	// Get unspent outputs only
	cfg := &queue.SearchCfg{
		Keys:  [][]byte{[]byte(event)},
		Limit: 0, // No limit
	}

	topicStore := ctrl.storage.GetStore(topic)
	outputs, err := topicStore.SearchOutputs(c.Context(), cfg, nil, false) // includeSpent=false
	if err != nil {
		log.Printf("Unspent lookup error: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve unspent outputs",
		})
	}

	return c.JSON(outputs)
}

// GetMultiAddressBalance retrieves token balance for multiple addresses
// @Summary Get multi-address token balance
// @Description Get the combined BSV21 token balance for multiple addresses
// @Tags bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Param lockType path string true "Lock type (p2pkh, cos, list, etc.)"
// @Param addresses body []string true "Array of addresses (max 100)"
// @Success 200 {object} BalanceResponse
// @Failure 400 {object} ErrorResponse "Invalid request body or too many addresses"
// @Failure 503 {object} ErrorResponse "Topic not available"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /bsv21/{tokenId}/{lockType}/balance [post]
func (ctrl *BSV21Controller) GetMultiAddressBalance(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	lockType := c.Params("lockType")

	// Validate tokenId is active
	topic := "tm_" + tokenId
	if !ctrl.activeTopics.IsActive(topic) {
		return c.Status(fiber.StatusServiceUnavailable).JSON(ErrorResponse{
			Message: "Topic not available",
		})
	}

	// Parse the request body - accept array of addresses directly
	var addresses []string
	if err := c.BodyParser(&addresses); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid request body",
		})
	}

	if len(addresses) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "No addresses provided",
		})
	}

	// Limit the number of addresses to prevent abuse
	if len(addresses) > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Too many addresses (max 100)",
		})
	}

	log.Printf("Received multi-balance request for token %s, %s for %d addresses", tokenId, lockType, len(addresses))

	// Build event strings for all addresses
	events := make([]string, 0, len(addresses))
	for _, address := range addresses {
		event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
		events = append(events, event)
	}

	// Get total balance for all addresses combined
	balance, utxoCount, err := ctrl.bsv21Lookup.GetBalance(c.Context(), events, topic)
	if err != nil {
		log.Printf("Balance calculation error: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to calculate balances",
		})
	}

	return c.JSON(BalanceResponse{
		Balance:   balance,
		UtxoCount: utxoCount,
	})
}

// GetMultiAddressHistory retrieves transaction history for multiple addresses
// @Summary Get multi-address transaction history
// @Description Get all BSV21 token transactions (including spent) for multiple addresses
// @Tags bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Param lockType path string true "Lock type (p2pkh, cos, list, etc.)"
// @Param addresses body []string true "Array of addresses (max 100)"
// @Param from query number false "Starting score for pagination"
// @Param limit query int false "Maximum number of results"
// @Param rev query bool false "Reverse order"
// @Success 200 {array} storage.IndexedOutput
// @Failure 400 {object} ErrorResponse "Invalid request body or too many addresses"
// @Failure 503 {object} ErrorResponse "Topic not available"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /bsv21/{tokenId}/{lockType}/history [post]
func (ctrl *BSV21Controller) GetMultiAddressHistory(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	lockType := c.Params("lockType")

	// Validate tokenId is active
	topic := "tm_" + tokenId
	if !ctrl.activeTopics.IsActive(topic) {
		return c.Status(fiber.StatusServiceUnavailable).JSON(ErrorResponse{
			Message: "Topic not available",
		})
	}

	// Parse the request body - accept array of addresses directly
	var addresses []string
	if err := c.BodyParser(&addresses); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid request body",
		})
	}

	if len(addresses) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "No addresses provided",
		})
	}

	// Limit the number of addresses to prevent abuse
	if len(addresses) > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Too many addresses (max 100)",
		})
	}

	log.Printf("Received multi-history request for token %s, %s for %d addresses", tokenId, lockType, len(addresses))

	// Build event strings for all addresses
	events := make([]string, 0, len(addresses))
	for _, address := range addresses {
		event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
		events = append(events, event)
	}

	// Parse query parameters for paging
	cfg := routes.ParseSearchConfig(c)
	cfg.Keys = make([][]byte, len(events))
	for i, e := range events {
		cfg.Keys[i] = []byte(e)
	}

	// Get outputs including spent (history includes all)
	topicStore := ctrl.storage.GetStore(topic)
	outputs, err := topicStore.SearchOutputs(c.Context(), cfg, nil, true) // includeSpent=true
	if err != nil {
		log.Printf("History lookup error: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve output history",
		})
	}

	return c.JSON(outputs)
}

// GetMultiAddressUnspent retrieves unspent outputs for multiple addresses
// @Summary Get multi-address unspent outputs
// @Description Get unspent BSV21 token outputs for multiple addresses
// @Tags bsv21
// @Accept json
// @Produce json
// @Param tokenId path string true "Token ID (outpoint format: txid_vout)"
// @Param lockType path string true "Lock type (p2pkh, cos, list, etc.)"
// @Param addresses body []string true "Array of addresses (max 100)"
// @Success 200 {array} storage.IndexedOutput
// @Failure 400 {object} ErrorResponse "Invalid request body or too many addresses"
// @Failure 503 {object} ErrorResponse "Topic not available"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /bsv21/{tokenId}/{lockType}/unspent [post]
func (ctrl *BSV21Controller) GetMultiAddressUnspent(c *fiber.Ctx) error {
	tokenId := c.Params("tokenId")
	lockType := c.Params("lockType")

	// Validate tokenId is active
	topic := "tm_" + tokenId
	if !ctrl.activeTopics.IsActive(topic) {
		return c.Status(fiber.StatusServiceUnavailable).JSON(ErrorResponse{
			Message: "Topic not available",
		})
	}

	// Parse the request body - accept array of addresses directly
	var addresses []string
	if err := c.BodyParser(&addresses); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Invalid request body",
		})
	}

	if len(addresses) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "No addresses provided",
		})
	}

	// Limit the number of addresses to prevent abuse
	if len(addresses) > 100 {
		return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
			Message: "Too many addresses (max 100)",
		})
	}

	log.Printf("Received multi-unspent request for token %s, %s for %d addresses", tokenId, lockType, len(addresses))

	// Build event strings for all addresses
	events := make([]string, 0, len(addresses))
	for _, address := range addresses {
		event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
		events = append(events, event)
	}

	// Get unspent outputs only
	keys := make([][]byte, len(events))
	for i, e := range events {
		keys[i] = []byte(e)
	}
	cfg := &queue.SearchCfg{
		Keys:  keys,
		Limit: 0, // No limit
	}

	topicStore := ctrl.storage.GetStore(topic)
	outputs, err := topicStore.SearchOutputs(c.Context(), cfg, nil, false) // includeSpent=false
	if err != nil {
		log.Printf("Unspent lookup error: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{
			Message: "Failed to retrieve unspent outputs",
		})
	}

	return c.JSON(outputs)
}
