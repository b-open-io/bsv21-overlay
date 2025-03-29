package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/4chain-ag/go-overlay-services/pkg/core/engine/storage"
	"github.com/4chain-ag/go-overlay-services/pkg/core/gasp/core"
	"github.com/b-open-io/bsv21-overlay/lookups"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction/chaintracker/headers_client"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"
)

var JUNGLEBUS = "https://texas1.junglebus.gorillapool.io"
var CACHE_DIR string
var chaintracker headers_client.Client
var PORT int
var SYNC bool

func init() {
	godotenv.Load("../../.env")
	CACHE_DIR = os.Getenv("CACHE_DIR")
	chaintracker = headers_client.Client{
		Url:    os.Getenv("BLOCK_HEADERS_URL"),
		ApiKey: os.Getenv("BLOCK_HEADERS_API_KEY"),
	}
	PORT, _ = strconv.Atoi(os.Getenv("PORT"))
	flag.IntVar(&PORT, "p", PORT, "Port to listen on")
	flag.BoolVar(&SYNC, "s", false, "Start sync")
	flag.Parse()
	if PORT == 0 {
		PORT = 3000
	}
}

func main() {
	log.Println("TOPIC_DB", os.Getenv("TOPIC_DB"))
	log.Println("LOOKUP_DB", os.Getenv("LOOKUP_DB"))
	log.Println("CACHE_DIR", os.Getenv("CACHE_DIR"))
	storage, err := storage.NewSQLiteStorage(os.Getenv("TOPIC_DB"))
	if err != nil {
		panic(err)
	}
	bsv21Lookup := lookups.NewBsv21Lookup(storage, os.Getenv("LOOKUP_DB"))

	e := engine.Engine{
		Managers: map[string]engine.TopicManager{
			"bsv21": topics.NewBsv21TopicManager(
				"bsv21",
				storage,
				[]string{
					"ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0", //MNEE
				}),
		},
		LookupServices: map[string]engine.LookupService{
			"bsv21": bsv21Lookup,
		},
		SyncConfiguration: map[string]engine.SyncConfiguration{
			"bsv21": {
				Type: engine.SyncConfigurationPeers,
				Peers: []string{
					"http://morovol:3000",
					"http://morovol:3001",
				},
			},
		},
		HostingURL:   fmt.Sprintf("http://morovol:%d", PORT),
		Storage:      storage,
		ChainTracker: chaintracker,
		Verbose:      true,
		PanicOnError: true,
	}
	// Create a new Fiber app
	app := fiber.New()
	app.Use(logger.New())

	// Define a simple GET route
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("Hello, World!")
	})

	app.Post("/requestSyncResponse", func(c *fiber.Ctx) error {
		var request core.GASPInitialRequest
		topic := c.Get("x-bsv-topic", "bsv21")
		if err := c.BodyParser(&request); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid request",
			})
		} else if response, err := e.ProvideForeignSyncResponse(c.Context(), &request, topic); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		} else {
			return c.JSON(response)
		}
	})

	app.Post("/requestForeignGASPNode", func(c *fiber.Ctx) error {
		var request core.GASPNodeRequest
		if err := c.BodyParser(&request); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid request",
			})
		} else if response, err := e.ProvideForeignGASPNode(
			c.Context(),
			request.GraphID,
			&overlay.Outpoint{Txid: *request.Txid, OutputIndex: request.OutputIndex},
		); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		} else {
			return c.JSON(response)
		}
	})

	app.Post("/lookup", func(c *fiber.Ctx) error {
		var question lookup.LookupQuestion
		if err := c.BodyParser(&question); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid request",
			})
		} else if answer, err := e.Lookup(c.Context(), &question); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		} else {
			return c.JSON(answer)
		}
	})

	if SYNC {
		if err := e.StartGASPSync(context.Background()); err != nil {
			log.Fatalf("Error starting sync: %v", err)
		}
	}

	// Start the server on port 3000
	if err := app.Listen(fmt.Sprintf(":%d", PORT)); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
