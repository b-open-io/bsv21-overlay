package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/4chain-ag/go-overlay-services/pkg/core/engine"
	"github.com/4chain-ag/go-overlay-services/pkg/core/gasp/core"
	"github.com/b-open-io/bsv21-overlay/lookups"
	lookupRedis "github.com/b-open-io/bsv21-overlay/lookups/events/redis"
	storageRedis "github.com/b-open-io/bsv21-overlay/storage/redis"
	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/broadcaster"
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
	hostingUrl := fmt.Sprintf("http://morovol:%d", PORT)
	peers := []string{
		"http://morovol:3000",
		"http://morovol:3001",
	}

	storage, err := storageRedis.NewRedisStorage(os.Getenv("REDIS"))
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer storage.Close()

	eventLookup, err := lookupRedis.NewRedisEventLookup(
		os.Getenv("REDIS"),
		storage,
	)
	bsv21Lookup := &lookups.Bsv21EventsLookup{
		EventLookup: eventLookup,
	}

	e := engine.Engine{
		Managers: map[string]engine.TopicManager{
			"tm_ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0": topics.NewBsv21ValidatedTopicManager(
				"tm_ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0",
				storage,
				[]string{
					"ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0", //MNEE
				}),
		},
		LookupServices: map[string]engine.LookupService{
			"bsv21": bsv21Lookup,
		},
		SyncConfiguration: map[string]engine.SyncConfiguration{
			"tm_ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0": {
				Type:  engine.SyncConfigurationPeers,
				Peers: peers,
			},
		},
		Broadcaster: &broadcaster.Arc{
			ApiUrl: "https://arc.taal.com",
			// ApiKey:  os.Getenv("ARC_API_KEY"),
			WaitFor: broadcaster.ACCEPTED_BY_NETWORK,
			// CallbackUrl:   hostingUrl + "/arc-ingest",
			// CallbackToken: callbackToken,
		},
		HostingURL:   hostingUrl,
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

	app.Post("/submit", func(c *fiber.Ctx) error {
		topicsHeader := c.Get("x-topics", "")
		if topicsHeader == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Missing x-topics header",
			})
		}
		taggedBeef := overlay.TaggedBEEF{}
		if err := json.Unmarshal([]byte(topicsHeader), &taggedBeef.Topics); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid x-topics header",
			})
		}
		copy(taggedBeef.Beef, c.Body())
		onSteakReady := func(steak *overlay.Steak) {
			return
		}
		if steak, err := e.Submit(c.Context(), taggedBeef, engine.SubmitModeCurrent, onSteakReady); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		} else {
			return c.JSON(steak)
		}

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

	app.Post("/arc-ingest", func(c *fiber.Ctx) error {
		var status broadcaster.ArcResponse
		if err := c.BodyParser(&status); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid request",
			})
		} else if txid, err := chainhash.NewHashFromHex(status.Txid); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid txid",
			})
		} else if merklePath, err := transaction.NewMerklePathFromHex(status.MerklePath); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "Invalid merkle path",
			})
		} else if err := e.HandleNewMerkleProof(c.Context(), txid, merklePath); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		} else {
			return c.JSON(fiber.Map{
				"status": "success",
			})
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
