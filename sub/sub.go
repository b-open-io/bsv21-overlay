package sub

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/GorillaPool/go-junglebus"
	"github.com/GorillaPool/go-junglebus/models"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
)

var JUNGLEBUS = "https://texas1.junglebus.gorillapool.io"
var QueueDb *sql.DB
var jb *junglebus.Client

func init() {
	var err error
	if err = godotenv.Load("../../.env"); err != nil {
		log.Panic(err)
	}

	if QueueDb, err = sql.Open("sqlite3", os.Getenv("QUEUE_DB")); err != nil {
		panic(err)
	} else if _, err = QueueDb.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		panic(err)
	} else if _, err = QueueDb.Exec("PRAGMA synchronous=NORMAL;"); err != nil {
		panic(err)
	} else if _, err = QueueDb.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		panic(err)
	} else if _, err = QueueDb.Exec("PRAGMA temp_store=MEMORY;"); err != nil {
		panic(err)
	} else if _, err = QueueDb.Exec("PRAGMA mmap_size=30000000000;"); err != nil {
		panic(err)
	} else if _, err = QueueDb.Exec(`CREATE TABLE IF NOT EXISTS queue(
		height INTEGER,
		idx BIGINT,
		txid TEXT, 
		PRIMARY KEY(height, idx)
	)`); err != nil {
		panic(err)
	}
	jb, err = junglebus.New(
		junglebus.WithHTTP(JUNGLEBUS),
	)
}

func Exec() {
	ctx := context.Background()
	insQueue, err := QueueDb.Prepare(`INSERT INTO queue(height, idx, txid) VALUES(?, ?, ?) ON CONFLICT DO NOTHING`)
	if err != nil {
		log.Panic(err)
	}
	if _, err = insQueue.Exec(883989, 368, "ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127"); err != nil {
		log.Panic(err)
	}
	var sub *junglebus.Subscription
	txcount := 0
	// lastActivity := time.Now()
	fromBlock := uint64(883989)
	fromPage := uint64(0)
	if err = QueueDb.QueryRow(`SELECT height FROM queue ORDER BY height DESC LIMIT 1`).Scan(&fromBlock); err != nil && err != sql.ErrNoRows {
		log.Panic(err)
	}

	// go func() {
	// 	ticker := time.NewTicker(1 * time.Minute)
	// 	for range ticker.C {
	// 		if time.Since(lastActivity) > 15*time.Minute {
	// 			log.Println("No activity for 15 minutes, exiting...")
	// 			os.Exit(0)
	// 		}
	// 	}
	// }()

	log.Println("Subscribing to Junglebus from block", fromBlock, fromPage)
	if sub, err = jb.SubscribeWithQueue(ctx,
		"e7ad693f63a56834ce62e6d52d67809ea5315c5fd66cbcca3cbad9b7dcda0836",
		fromBlock,
		fromPage,
		junglebus.EventHandler{
			OnTransaction: func(txn *models.TransactionResponse) {
				txcount++
				log.Printf("[TX]: %d - %d: %d %s\n", txn.BlockHeight, txn.BlockIndex, len(txn.Transaction), txn.Id)
				if _, err := insQueue.Exec(txn.BlockHeight, txn.BlockIndex, txn.Id); err != nil {
					log.Panic(err)
				}
			},
			OnStatus: func(status *models.ControlResponse) {
				log.Printf("[STATUS]: %d %v %d processed\n", status.StatusCode, status.Message, txcount)
				switch status.StatusCode {
				case 200:
					txcount = 0
				case 999:
					log.Println(status.Message)
					log.Println("Unsubscribing...")
					sub.Unsubscribe()
					os.Exit(0)
					return
				}
			},
			OnError: func(err error) {
				log.Panicf("[ERROR]: %v\n", err)
			},
		},
		&junglebus.SubscribeOptions{
			QueueSize: 10000000,
			LiteMode:  true,
		},
	); err != nil {
		log.Panic(err)
	}
	defer func() {
		sub.Unsubscribe()
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigs:
	case <-ctx.Done():
	}

}
