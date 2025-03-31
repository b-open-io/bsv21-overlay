package main

import (
	"context"
	"sync"

	"github.com/b-open-io/bsv21-overlay/sub"
	"github.com/b-open-io/bsv21-overlay/util"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	var alreadyLoaded sync.Map
	ctx := context.Background()
	rows, err := sub.QueueDb.Query(`SELECT txid FROM queue ORDER BY height, idx`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	limiter := make(chan struct{}, 10)
	var wg sync.WaitGroup
	for rows.Next() {
		var txidStr string
		if err := rows.Scan(&txidStr); err != nil {
			panic(err)
		}
		if _, loaded := alreadyLoaded.LoadOrStore(txidStr, struct{}{}); loaded {
			continue
		}
		wg.Add(1)
		limiter <- struct{}{}
		go func(txidStr string) {
			defer wg.Done()
			defer func() { <-limiter }()

			if txid, err := chainhash.NewHashFromHex(txidStr); err != nil {
				panic(err)
			} else if tx, err := util.LoadTx(ctx, txid); err != nil {
				panic(err)
			} else {
				for _, input := range tx.Inputs {
					txidStr := input.SourceTXID.String()
					if _, loaded := alreadyLoaded.LoadOrStore(txidStr, struct{}{}); loaded {
						continue
					}
					if _, err = util.LoadTx(ctx, input.SourceTXID); err != nil {
						panic(err)
					}
				}
			}
		}(txidStr)
	}
	wg.Wait()
	sub.QueueDb.Close()
}
