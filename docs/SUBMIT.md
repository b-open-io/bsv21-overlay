# Submitting Transactions

Submit BSV21 token transactions to the overlay service.

## Endpoint

```
POST /api/v1/submit
```

## Headers

| Header | Required | Description |
|--------|----------|-------------|
| `x-topics` | Yes | Comma-separated list of topics. Use `tm_<tokenId>` format. |
| `Content-Type` | Yes | `application/octet-stream` |

## Request Body

Binary BEEF (BRC-62/BRC-96) encoded transaction data.

## Topic Format

Topics use the format `tm_<tokenId>` where `tokenId` is the token's outpoint string:

```
tm_ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0
```

## Response

```json
{
  "STEAK": {
    "tm_ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0": {
      "outputsToAdmit": [0, 1],
      "coinsToRetain": [],
      "coinsRemoved": [0],
      "ancillaryTxIDs": []
    }
  }
}
```

## Go Example

```go
package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

// ResolveTxBeef recursively loads source transactions to build a complete BEEF.
// If the transaction has a MerklePath (is mined), it's already provable.
// Otherwise, each input's SourceTransaction must be populated.
func ResolveTxBeef(ctx context.Context, txid string) (*transaction.Transaction, error) {
	tx, err := LoadTx(ctx, txid) // Your function to load a transaction
	if err != nil {
		return nil, err
	}

	if tx.MerklePath != nil {
		return tx, nil // Already has proof, no ancestors needed
	}

	for _, input := range tx.Inputs {
		if input.SourceTransaction == nil {
			input.SourceTransaction, err = ResolveTxBeef(ctx, input.SourceTXID.String())
			if err != nil {
				return nil, err
			}
		}
	}
	return tx, nil
}

// SubmitTransaction serializes a transaction to BEEF and submits it to the overlay.
func SubmitTransaction(ctx context.Context, tx *transaction.Transaction, tokenId string) error {
	beef, err := tx.BEEF()
	if err != nil {
		return fmt.Errorf("failed to serialize BEEF: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://overlay.example.com/api/v1/submit", bytes.NewReader(beef))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("x-topics", "tm_"+tokenId)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("submit failed: %s", resp.Status)
	}

	return nil
}
```
