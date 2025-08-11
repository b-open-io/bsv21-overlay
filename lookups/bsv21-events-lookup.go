package lookups

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/b-open-io/overlay/storage"
	"github.com/bitcoin-sv/go-templates/template/bsv21"
	"github.com/bitcoin-sv/go-templates/template/bsv21/ltm"
	"github.com/bitcoin-sv/go-templates/template/bsv21/pow20"
	"github.com/bitcoin-sv/go-templates/template/cosign"
	"github.com/bitcoin-sv/go-templates/template/ordlock"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/overlay"
	"github.com/bsv-blockchain/go-sdk/overlay/lookup"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
)

type Bsv21EventsLookup struct {
	storage   storage.EventDataStorage
	mintCache sync.Map // Cache of mint tokens by tokenId (stores *bsv21.Bsv21)
}

func NewBsv21EventsLookup(eventStorage storage.EventDataStorage) (*Bsv21EventsLookup, error) {
	return &Bsv21EventsLookup{
		storage: eventStorage,
	}, nil
}

func (l *Bsv21EventsLookup) OutputAdmittedByTopic(ctx context.Context, payload *engine.OutputAdmittedByTopic) error {
	blockHeight := uint32(time.Now().Unix())
	var blockIdx uint64
	_, tx, txid, err := transaction.ParseBeef(payload.AtomicBEEF)
	if err != nil {
		return err
	}
	if tx.MerklePath != nil {
		blockHeight = tx.MerklePath.BlockHeight
		for _, pe := range tx.MerklePath.Path[0] {
			if pe.Hash.Equal(*txid) {
				blockIdx = pe.Offset
				break
			}
		}
	}
	
	// Decode the BSV21 data using the go-templates parser
	if b := bsv21.Decode(payload.LockingScript); b != nil {
		events := make([]string, 0, 5)
		
		// For mint operations, set the ID to the ordinal string
		if b.Op == string(bsv21.OpMint) {
			b.Id = payload.Outpoint.OrdinalString()
			if b.Symbol != nil {
				events = append(events, fmt.Sprintf("sym:%s", *b.Symbol))
			}
		} else if b.Op == string(bsv21.OpTransfer) {
			// For transfers, get the mint token data (uses cache internally)
			tokenOutpoint, err := transaction.OutpointFromString(b.Id)
			if err == nil {
				mintData, err := l.GetToken(ctx, tokenOutpoint)
				if err == nil {
					// Copy mint token fields to the transfer
					if sym, ok := mintData["sym"].(string); ok {
						b.Symbol = &sym
					}
					if dec, ok := mintData["dec"].(uint8); ok {
						b.Decimals = &dec
					} else if dec, ok := mintData["dec"].(float64); ok {
						decUint8 := uint8(dec)
						b.Decimals = &decUint8
					}
					if icon, ok := mintData["icon"].(string); ok {
						b.Icon = &icon
					}
				}
			}
		}
		events = append(events, fmt.Sprintf("id:%s", b.Id))
		
		// Extract the address from the suffix script
		var address string
		suffix := script.NewFromBytes(b.Insc.ScriptSuffix)
		if p := p2pkh.Decode(suffix, true); p != nil {
			address = p.AddressString
			events = append(events, fmt.Sprintf("p2pkh:%s:%s", address, b.Id))
		} else if c := cosign.Decode(suffix); c != nil {
			address = c.Address
			events = append(events, fmt.Sprintf("cos:%s:%s", address, b.Id))
		} else if ltm := ltm.Decode(suffix); ltm != nil {
			events = append(events, fmt.Sprintf("ltm:%s", b.Id))
		} else if pow20 := pow20.Decode(suffix); pow20 != nil {
			events = append(events, fmt.Sprintf("pow20:%s", b.Id))
		} else if ordLock := ordlock.Decode(suffix); ordLock != nil {
			if ordLock.Seller != nil {
				address = ordLock.Seller.AddressString
				events = append(events, fmt.Sprintf("list:%s:%s", address, b.Id))
			}
			events = append(events, fmt.Sprintf("list:%s", b.Id))
		}
		
		// Create a clean data structure that matches the go-templates structure
		// This structure will be stored directly without any mapping on retrieval
		// Store amount as string to avoid BSON type conversion issues
		// and maintain consistency across all storage implementations
		
		bsv21Data := map[string]interface{}{
			"id":  b.Id,
			"op":  b.Op,
			"amt": strconv.FormatUint(b.Amt, 10), // Store as string
		}
		
		// Add optional fields only if they exist
		if b.Symbol != nil {
			bsv21Data["sym"] = *b.Symbol
		}
		if b.Decimals != nil {
			bsv21Data["dec"] = *b.Decimals
		}
		if b.Icon != nil {
			bsv21Data["icon"] = *b.Icon
		}
		if address != "" {
			bsv21Data["address"] = address
		}
		
		// Store the BSV21 data under the "bsv21" key
		dataToStore := map[string]interface{}{
			"bsv21": bsv21Data,
		}
		
		// Save all events with the data using the storage layer
		if err := l.storage.SaveEvents(ctx, payload.Outpoint, events, blockHeight, blockIdx, dataToStore); err != nil {
			return err
		}
	}
	return nil
}

func (l *Bsv21EventsLookup) GetDocumentation() string {
	return "BSV21 lookup"
}

func (l *Bsv21EventsLookup) GetMetaData() *overlay.MetaData {
	return &overlay.MetaData{
		Name: "BSV21",
	}
}

// OutputSpent is called when a previously-admitted UTXO is spent
// No BSV21-specific action needed - storage handles spend tracking
func (l *Bsv21EventsLookup) OutputSpent(ctx context.Context, payload *engine.OutputSpent) error {
	return nil
}

// OutputNoLongerRetainedInHistory is called when historical retention is no longer required
// No BSV21-specific action needed
func (l *Bsv21EventsLookup) OutputNoLongerRetainedInHistory(ctx context.Context, outpoint *transaction.Outpoint, topic string) error {
	return nil
}

// OutputEvicted permanently removes the UTXO from all indices
// No BSV21-specific action needed - storage handles eviction
func (l *Bsv21EventsLookup) OutputEvicted(ctx context.Context, outpoint *transaction.Outpoint) error {
	return nil
}

// OutputBlockHeightUpdated is called when a transaction's block height is updated
// No BSV21-specific action needed - storage handles block height updates
func (l *Bsv21EventsLookup) OutputBlockHeightUpdated(ctx context.Context, txid *chainhash.Hash, blockHeight uint32, blockIndex uint64) error {
	return nil
}

// Lookup handles generic lookup queries through the LookupService interface
func (l *Bsv21EventsLookup) Lookup(ctx context.Context, question *lookup.LookupQuestion) (*lookup.LookupAnswer, error) {
	// For now, return an empty answer
	// In the future, this could parse the query JSON and route to appropriate methods
	return &lookup.LookupAnswer{
		Type: lookup.AnswerTypeFormula,
	}, nil
}


// GetBalance calculates the total balance of BSV21 tokens for given event patterns
// Balance is always calculated for unspent outputs only
func (l *Bsv21EventsLookup) GetBalance(ctx context.Context, events []string) (uint64, int, error) {
	// Use the storage interface to query for outpoints with the given events
	question := &storage.EventQuestion{
		Events:      events,
		UnspentOnly: true, // Balance is always for unspent outputs only
		From:        0,
		Limit:       0, // No limit - get all results
	}
	
	results, err := l.storage.LookupOutpoints(ctx, question, true) // include data
	if err != nil {
		return 0, 0, err
	}
	
	var totalBalance uint64
	count := 0
	
	// Sum up amounts from BSV21 data
	for _, result := range results {
		if result.Data == nil {
			continue
		}
		
		// Access the data structure we control
		dataMap, ok := result.Data.(map[string]interface{})
		if !ok {
			continue
		}
		
		// Extract BSV21 data
		bsv21DataRaw, ok := dataMap["bsv21"]
		if !ok {
			continue
		}
		
		bsv21Data, ok := bsv21DataRaw.(map[string]interface{})
		if !ok {
			continue
		}
		
		// Extract amount - we know it's stored as a string
		amtStr, ok := bsv21Data["amt"].(string)
		if !ok {
			continue
		}
		
		amt, err := strconv.ParseUint(amtStr, 10, 64)
		if err != nil {
			continue
		}
		
		totalBalance += amt
		count++
	}
	
	return totalBalance, count, nil
}

// GetToken returns the mint transaction details for a specific BSV21 token (with caching)
func (l *Bsv21EventsLookup) GetToken(ctx context.Context, outpoint *transaction.Outpoint) (map[string]interface{}, error) {
	tokenId := outpoint.OrdinalString()
	
	// Check cache first - now caching the response map directly
	if cached, ok := l.mintCache.Load(tokenId); ok {
		return cached.(map[string]interface{}), nil
	}
	
	// Not in cache, get the data for this specific outpoint
	data, err := l.storage.GetOutputData(ctx, outpoint)
	if err != nil {
		return nil, err
	}
	
	if data == nil {
		return nil, fmt.Errorf("token data not found")
	}
	
	// Access the data structure we control
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid data format")
	}
	
	// Extract BSV21 data
	bsv21DataRaw, ok := dataMap["bsv21"]
	if !ok {
		return nil, fmt.Errorf("token data not found")
	}
	
	bsv21Data, ok := bsv21DataRaw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid token data format")
	}
	
	// Verify this is a mint transaction
	op, ok := bsv21Data["op"].(string)
	if !ok || op != "deploy+mint" {
		return nil, fmt.Errorf("outpoint exists but is not a mint transaction (op=%s)", op)
	}
	
	// Build response with all token details
	response := map[string]interface{}{
		"id":   tokenId,
		"txid": outpoint.Txid.String(),
		"vout": outpoint.Index,
		"op":   op,
	}
	
	// Add amount - already stored as string in bsv21Data
	if amtStr, ok := bsv21Data["amt"].(string); ok {
		response["amt"] = amtStr
	}
	
	// Add optional fields if present
	if sym, ok := bsv21Data["sym"].(string); ok {
		response["sym"] = sym
	}
	
	if dec, ok := bsv21Data["dec"].(float64); ok {
		response["dec"] = uint8(dec)
	}
	
	if icon, ok := bsv21Data["icon"].(string); ok {
		response["icon"] = icon
	}
	
	// Cache the response directly
	l.mintCache.Store(tokenId, response)
	
	return response, nil
}
