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
  "description": "Overlay engine successfully processed the submitted transaction",
  "steak": {
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
	"fmt"
	"net/http"

	"github.com/bsv-blockchain/go-sdk/transaction"
)

func submitTransaction(tx *transaction.Transaction, tokenId string) error {
	// Serialize to BEEF format
	beef, err := tx.BEEF()
	if err != nil {
		return fmt.Errorf("failed to serialize BEEF: %w", err)
	}

	// Create request
	req, err := http.NewRequest("POST", "https://overlay.example.com/api/v1/submit", bytes.NewReader(beef))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("x-topics", "tm_"+tokenId)

	// Send request
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

## Building a Transaction with BEEF

For the `tx.BEEF()` call to succeed, each input must have its `SourceTransaction` populated with the parent transaction (which itself must have either a `MerklePath` or its own `SourceTransaction` chain back to confirmed ancestors).

```go
// Each input needs its source transaction
for _, input := range tx.Inputs {
	input.SourceTransaction = parentTx // Must have MerklePath or its own ancestors
}

// Now BEEF() can build the complete ancestry
beef, err := tx.BEEF()
```
