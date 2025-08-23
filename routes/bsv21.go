package routes

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/b-open-io/bsv21-overlay/lookups"
	"github.com/b-open-io/overlay/routes"
	"github.com/b-open-io/overlay/storage"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/gofiber/fiber/v2"
)

// BSV21RoutesConfig holds the configuration for BSV21-specific routes
type BSV21RoutesConfig struct {
	Storage      storage.EventDataStorage
	ChainTracker *headers_client.Client
	Engine       *engine.Engine
	BSV21Lookup  *lookups.Bsv21EventsLookup
}

// RegisterBSV21Routes registers BSV21-specific API routes
func RegisterBSV21Routes(group fiber.Router, config *BSV21RoutesConfig) {
	if config == nil || config.Storage == nil || config.ChainTracker == nil || config.Engine == nil || config.BSV21Lookup == nil {
		log.Fatal("RegisterBSV21Routes: config, storage, chaintracker, engine, and bsv21lookup are required")
	}

	store := config.Storage
	chaintracker := config.ChainTracker
	eng := config.Engine
	bsv21Lookup := config.BSV21Lookup

	// Token details endpoint
	group.Get("/bsv21/:tokenId", func(c *fiber.Ctx) error {
		tokenIdStr := c.Params("tokenId")
		log.Printf("Received request for BSV21 token details: %s", tokenIdStr)

		// Validate tokenId is active
		topic := "tm_" + tokenIdStr
		if _, exists := eng.Managers[topic]; !exists {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"message": "Topic not available",
			})
		}

		// Parse the tokenId string into an outpoint
		outpoint, err := transaction.OutpointFromString(tokenIdStr)
		if err != nil {
			log.Printf("Invalid token ID format: %v", err)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid token ID format",
			})
		}

		// Use the dedicated GetToken method
		tokenData, err := bsv21Lookup.GetToken(c.Context(), outpoint)
		if err != nil {
			log.Printf("GetToken error: %v", err)

			// Determine appropriate status code based on error
			if err.Error() == "token not found" {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
					"message": "Token not found",
				})
			} else if strings.Contains(err.Error(), "not a mint transaction") {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
					"message": err.Error(),
				})
			}

			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve token details",
			})
		}

		return c.JSON(tokenData)
	})

	// Block data endpoint
	group.Get("/bsv21/:tokenId/block/:height", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		heightStr := c.Params("height")

		log.Printf("Received block request for BSV21 tokenId: %s at height: %s", tokenId, heightStr)

		// Validate tokenId is active
		topic := "tm_" + tokenId
		if _, exists := eng.Managers[topic]; !exists {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"message": "Topic not available",
			})
		}

		// Parse height as uint32
		height64, err := strconv.ParseUint(heightStr, 10, 32)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid height parameter",
			})
		}
		height := uint32(height64)

		// Get transactions from storage for the specific token's topic at this height
		transactions, err := store.GetTransactionsByTopicAndHeight(c.Context(), topic, height)
		if err != nil {
			log.Printf("GetBlockData error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to get block data",
			})
		}

		// Fetch block header information from chaintracker
		blockHeader, err := chaintracker.BlockByHeight(c.Context(), height)
		if err != nil {
			log.Printf("Failed to get block header: %v", err)
			// Continue without header info rather than failing completely
			return c.JSON(fiber.Map{
				"block": fiber.Map{
					"height":            height,
					"hash":              "",
					"previousblockhash": "",
				},
				"transactions": transactions,
			})
		}

		// Build response with header and transactions
		response := fiber.Map{
			"block": fiber.Map{
				"height":            height,
				"hash":              blockHeader.Hash.String(),
				"previousblockhash": blockHeader.PreviousBlock.String(),
				"timestamp":         blockHeader.Timestamp,
			},
			"transactions": transactions,
		}

		return c.JSON(response)
	})

	// Transaction details endpoint
	group.Get("/bsv21/:tokenId/tx/:txid", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		txidStr := c.Params("txid")

		log.Printf("Received transaction request for BSV21 tokenId: %s, txid: %s", tokenId, txidStr)

		// Validate tokenId is active
		topic := "tm_" + tokenId
		if _, exists := eng.Managers[topic]; !exists {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"message": "Topic not available",
			})
		}

		// Parse the txid string into a hash
		txid, err := chainhash.NewHashFromHex(txidStr)
		if err != nil {
			log.Printf("Invalid transaction ID format: %v", err)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid transaction ID format",
			})
		}

		// Check if BEEF should be included (optional query parameter)
		includeBeef := c.Query("beef") == "true"

		// Get single transaction from storage with optional BEEF
		transaction, err := store.GetTransactionByTopic(c.Context(), topic, txid, includeBeef)
		if err != nil {
			log.Printf("GetTransactionByTopic error: %v", err)

			// Determine appropriate status code based on error
			if err.Error() == "transaction not found" {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
					"message": "Transaction not found",
				})
			}

			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve transaction details",
			})
		}

		return c.JSON(transaction)
	})

	// Single address balance endpoint
	group.Get("/bsv21/:tokenId/:lockType/:address/balance", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		lockType := c.Params("lockType")
		address := c.Params("address")

		// Validate tokenId is active
		topic := "tm_" + tokenId
		if _, exists := eng.Managers[topic]; !exists {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"message": "Topic not available",
			})
		}

		// Build event string from lockType, address, and tokenId
		event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
		log.Printf("Received balance request for token %s, %s address %s", tokenId, lockType, address)

		// Get balance for the single event
		balance, outputs, err := bsv21Lookup.GetBalance(c.Context(), []string{event})
		if err != nil {
			log.Printf("Balance calculation error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to calculate balance",
			})
		}

		return c.JSON(fiber.Map{
			"balance":   balance, // uint64 will be serialized correctly by JSON
			"utxoCount": outputs,
		})
	})

	// Single address history endpoint
	group.Get("/bsv21/:tokenId/:lockType/:address/history", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		lockType := c.Params("lockType")
		address := c.Params("address")

		// Validate tokenId is active
		topic := "tm_" + tokenId
		if _, exists := eng.Managers[topic]; !exists {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"message": "Topic not available",
			})
		}

		// Build event string from lockType, address, and tokenId
		event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
		log.Printf("Received history request for token %s, %s address %s", tokenId, lockType, address)

		// Parse query parameters for paging
		question := routes.ParseEventQuery(c)
		question.Events = []string{event}
		question.Topic = topic
		question.UnspentOnly = false // History includes all outputs

		outputs, err := store.FindOutputData(c.Context(), question)
		if err != nil {
			log.Printf("History lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve output history",
			})
		}

		return c.JSON(outputs)
	})

	// Single address unspent endpoint
	group.Get("/bsv21/:tokenId/:lockType/:address/unspent", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		lockType := c.Params("lockType")
		address := c.Params("address")

		// Validate tokenId is active
		topic := "tm_" + tokenId
		if _, exists := eng.Managers[topic]; !exists {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"message": "Topic not available",
			})
		}

		// Build event string from lockType, address, and tokenId
		event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
		log.Printf("Received unspent request for token %s, %s address %s", tokenId, lockType, address)

		// Get UTXOs for the single event using FindOutputData (now includes full data)
		question := &storage.EventQuestion{
			Events:      []string{event},
			UnspentOnly: true,
			From:        0,
			Limit:       0, // No limit
		}

		outputs, err := store.FindOutputData(c.Context(), question)
		if err != nil {
			log.Printf("Unspent lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve unspent outputs",
			})
		}

		return c.JSON(outputs)
	})

	// Multi-address balance endpoint
	group.Post("/bsv21/:tokenId/:lockType/balance", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		lockType := c.Params("lockType")

		// Validate tokenId is active
		topic := "tm_" + tokenId
		if _, exists := eng.Managers[topic]; !exists {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"message": "Topic not available",
			})
		}

		// Parse the request body - accept array of addresses directly
		var addresses []string
		if err := c.BodyParser(&addresses); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid request body",
			})
		}

		if len(addresses) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "No addresses provided",
			})
		}

		// Limit the number of addresses to prevent abuse
		if len(addresses) > 100 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Too many addresses (max 100)",
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
		balance, utxoCount, err := bsv21Lookup.GetBalance(c.Context(), events)
		if err != nil {
			log.Printf("Balance calculation error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to calculate balances",
			})
		}

		return c.JSON(fiber.Map{
			"balance":   balance,
			"utxoCount": utxoCount,
		})
	})

	// Multi-address history endpoint
	group.Post("/bsv21/:tokenId/:lockType/history", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		lockType := c.Params("lockType")

		// Validate tokenId is active
		topic := "tm_" + tokenId
		if _, exists := eng.Managers[topic]; !exists {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"message": "Topic not available",
			})
		}

		// Parse the request body - accept array of addresses directly
		var addresses []string
		if err := c.BodyParser(&addresses); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid request body",
			})
		}

		if len(addresses) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "No addresses provided",
			})
		}

		// Limit the number of addresses to prevent abuse
		if len(addresses) > 100 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Too many addresses (max 100)",
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
		question := routes.ParseEventQuery(c)
		question.Events = events
		question.Topic = topic
		question.UnspentOnly = false // History includes all outputs

		outputs, err := store.FindOutputData(c.Context(), question)
		if err != nil {
			log.Printf("History lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve output history",
			})
		}

		return c.JSON(outputs)
	})

	// Multi-address unspent endpoint
	group.Post("/bsv21/:tokenId/:lockType/unspent", func(c *fiber.Ctx) error {
		tokenId := c.Params("tokenId")
		lockType := c.Params("lockType")

		// Validate tokenId is active
		topic := "tm_" + tokenId
		if _, exists := eng.Managers[topic]; !exists {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"message": "Topic not available",
			})
		}

		// Parse the request body - accept array of addresses directly
		var addresses []string
		if err := c.BodyParser(&addresses); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Invalid request body",
			})
		}

		if len(addresses) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "No addresses provided",
			})
		}

		// Limit the number of addresses to prevent abuse
		if len(addresses) > 100 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"message": "Too many addresses (max 100)",
			})
		}

		log.Printf("Received multi-unspent request for token %s, %s for %d addresses", tokenId, lockType, len(addresses))

		// Build event strings for all addresses
		events := make([]string, 0, len(addresses))
		for _, address := range addresses {
			event := fmt.Sprintf("%s:%s:%s", lockType, address, tokenId)
			events = append(events, event)
		}

		// Get UTXOs for all addresses combined using FindOutputData
		question := &storage.EventQuestion{
			Events:      events,
			UnspentOnly: true,
			From:        0,
			Limit:       0, // No limit
		}

		outputs, err := store.FindOutputData(c.Context(), question)
		if err != nil {
			log.Printf("Unspent lookup error: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"message": "Failed to retrieve unspent outputs",
			})
		}

		return c.JSON(outputs)
	})
}
