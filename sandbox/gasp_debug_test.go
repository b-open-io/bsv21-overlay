package sandbox

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/b-open-io/bsv21-overlay/topics"
	"github.com/b-open-io/overlay/config"
	"github.com/bsv-blockchain/go-overlay-services/pkg/core/engine"
	"github.com/bsv-blockchain/go-sdk/chainhash"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/joho/godotenv"
)

// TestGASPValidationFlow tests the core GASP validation logic for a specific failing transaction
func TestGASPValidationFlow(t *testing.T) {
	// Load environment variables like server.go does
	godotenv.Load("../.env")
	
	// Use same configuration variables as server.go
	eventsURL := os.Getenv("EVENTS_URL")
	beefURL := os.Getenv("BEEF_URL") 
	queueURL := os.Getenv("QUEUE_URL")
	pubsubURL := os.Getenv("PUBSUB_URL")
	
	// Use one of the failing transaction outpoints from the logs
	failingTxidStr := "1361d419375130fd763c259d9ec75b4e2fc93a5af7973371e78c2225b5b5649d"
	vout := uint32(0)
	
	txid, err := chainhash.NewHashFromHex(failingTxidStr)
	if err != nil {
		t.Fatalf("Failed to parse txid: %v", err)
	}
	
	outpoint := &transaction.Outpoint{
		Txid:  *txid,
		Index: vout,
	}
	
	t.Logf("Testing GASP validation for outpoint: %s", outpoint.String())
	
	// Create storage using same config as server.go
	store, err := config.CreateEventStorage(eventsURL, beefURL, queueURL, pubsubURL)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	
	ctx := context.Background()
	
	// Create topic manager with SyncModeFull (like GASP sync uses)
	topicId := "tm_ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0"
	tokenId := "ae59f3b898ec61acbdb6cc7a245fabeded0c094bf046f35206a3aec60ef88127_0"
	
	topicManager := topics.NewBsv21ValidatedTopicManager(
		topicId,
		store,
		[]string{tokenId},
		topics.SyncModeFull,
	)
	
	// Create a simple remote to fetch the node from the peer
	remote := &engine.OverlayGASPRemote{
		EndpointUrl: "https://bsv21.1sat.app/api/v1",
		Topic:       topicId,
		HttpClient:  http.DefaultClient,
	}
	
	t.Logf("Requesting node from remote...")
	
	// Step 1: Retrieve the remote node 
	node, err := remote.RequestNode(ctx, outpoint, outpoint, true)
	if err != nil {
		t.Fatalf("Failed to request node: %v", err)
	}
	
	t.Logf("Retrieved node: GraphID=%s, OutputIndex=%d", node.GraphID.String(), node.OutputIndex)
	t.Logf("Node has AncillaryBeef: %d bytes", len(node.AncillaryBeef))
	
	// Step 2: Parse the AncillaryBeef first to get all dependency transactions
	t.Logf("Parsing AncillaryBeef to get dependencies...")
	
	if len(node.AncillaryBeef) == 0 {
		t.Fatalf("No AncillaryBeef provided - this explains why validation fails")
	}
	
	// Parse the AncillaryBeef to get dependency transactions
	ancillaryBeef, _, _, err := transaction.ParseBeef(node.AncillaryBeef)
	if err != nil {
		t.Fatalf("Failed to parse AncillaryBeef: %v", err)
	}
	
	t.Logf("AncillaryBeef contains %d dependency transactions", len(ancillaryBeef.Transactions))
	for i, beefTx := range ancillaryBeef.Transactions {
		// Convert BeefTx to Transaction to access methods
		depTx := beefTx.Transaction
		t.Logf("Dependency %d: %s (%d inputs, %d outputs)", i, depTx.TxID().String(), len(depTx.Inputs), len(depTx.Outputs))
	}
	
	// Parse our main transaction and construct BEEF properly (like getBEEFForNode)
	tx, err := transaction.NewTransactionFromHex(node.RawTx)
	if err != nil {
		t.Fatalf("Failed to parse main transaction: %v", err)
	}
	
	// Set proof if available
	if node.Proof != nil {
		if tx.MerklePath, err = transaction.NewMerklePathFromHex(*node.Proof); err != nil {
			t.Fatalf("Failed to parse proof: %v", err)
		}
		t.Logf("Transaction has merkle proof")
	} else {
		t.Logf("Transaction has no proof - will need dependencies")
	}
	
	// Create BEEF from transaction (following getBEEFForNode pattern)
	mainBeef, err := transaction.NewBeefFromTransaction(tx)
	if err != nil {
		t.Fatalf("Failed to create BEEF from transaction: %v", err)
	}
	
	// Merge AncillaryBeef if present (like getBEEFForNode does)
	if len(node.AncillaryBeef) > 0 {
		if err := mainBeef.MergeBeefBytes(node.AncillaryBeef); err != nil {
			t.Fatalf("Failed to merge AncillaryBeef: %v", err)
		}
		t.Logf("Successfully merged %d bytes of AncillaryBeef", len(node.AncillaryBeef))
	}
	
	// Get atomic BEEF bytes (like getBEEFForNode does)
	fullBeefBytes, err := mainBeef.AtomicBytes(tx.TxID())
	if err != nil {
		t.Fatalf("Failed to get atomic BEEF bytes: %v", err)
	}
	
	t.Logf("Transaction ID: %s", tx.TxID().String())
	t.Logf("Transaction inputs: %d", len(tx.Inputs))
	t.Logf("Transaction outputs: %d", len(tx.Outputs))
	t.Logf("Full BEEF size: %d bytes", len(fullBeefBytes))
	
	// Step 3: Create actual engine with topic manager (like GASP sync does)
	gaspEngine := &engine.Engine{
		Managers: map[string]engine.TopicManager{
			topicId: topicManager,
		},
		Storage: store,
	}
	
	// Create GASP storage using the actual engine
	gaspStorage := engine.NewOverlayGASPStorage(topicId, gaspEngine, nil)
	
	t.Logf("Following actual GASP processIncomingNode flow...")
	
	// Step 4: Test the topic manager validation directly with the full BEEF
	t.Logf("Testing topic manager validation with merged BEEF...")
	
	// Extract previous coins from the merged BEEF for validation
	_, previousCoins, _, err := transaction.ParseBeef(fullBeefBytes)
	if err != nil {
		t.Fatalf("Failed to parse merged BEEF: %v", err)
	}
	
	if previousCoins != nil {
		t.Logf("Previous coins available: %d", len(previousCoins.Inputs))
	} else {
		t.Logf("Previous coins: nil")
	}
	
	// Now test validation with proper previous coins  
	admit, err := topicManager.IdentifyAdmissibleOutputs(ctx, fullBeefBytes, nil)
	if err != nil {
		t.Logf("IdentifyAdmissibleOutputs failed: %v", err)
	} else {
		t.Logf("Admissible outputs: %+v", admit)
		
		// Check if any outputs were admitted
		if len(admit.OutputsToAdmit) > 0 {
			t.Logf("SUCCESS: %d outputs would be admitted: %v", len(admit.OutputsToAdmit), admit.OutputsToAdmit)
		} else {
			t.Logf("ISSUE: No outputs admitted - validation failed")
		}
		
		if len(admit.CoinsToRetain) > 0 {
			t.Logf("Coins to retain: %v", admit.CoinsToRetain)
		}
		if len(admit.CoinsRemoved) > 0 {
			t.Logf("Coins removed: %v", admit.CoinsRemoved)
		}
	}
	
	// Step 5: Test GASP storage flow with detailed dependency tracking
	t.Logf("=== GASP Storage Flow Testing ===")
	
	t.Logf("Step 5a: Testing AppendToGraph for root node...")
	err = gaspStorage.AppendToGraph(ctx, node, nil)
	if err != nil {
		t.Logf("AppendToGraph failed: %v", err)
	} else {
		t.Logf("AppendToGraph succeeded for root node")
	}
	
	t.Logf("Step 5b: Testing FindNeededInputs...")
	neededInputs, err := gaspStorage.FindNeededInputs(ctx, node)
	if err != nil {
		t.Logf("FindNeededInputs failed: %v", err)
	} else if neededInputs != nil {
		t.Logf("Found %d missing dependencies:", len(neededInputs.RequestedInputs))
		for outpointStr, data := range neededInputs.RequestedInputs {
			t.Logf("  - Missing: %s (metadata: %v)", outpointStr, data.Metadata)
		}
		
		// Step 5c: Test recursive dependency resolution
		t.Logf("Step 5c: Testing recursive dependency resolution...")
		for outpointStr, data := range neededInputs.RequestedInputs {
			t.Logf("Fetching dependency: %s", outpointStr)
			
			if outpoint, err := transaction.OutpointFromString(outpointStr); err != nil {
				t.Logf("Failed to parse outpoint %s: %v", outpointStr, err)
			} else if depNode, err := remote.RequestNode(ctx, node.GraphID, outpoint, data.Metadata); err != nil {
				t.Logf("Failed to fetch dependency %s: %v", outpointStr, err)
			} else {
				t.Logf("Successfully fetched dependency %s", outpointStr)
				t.Logf("  - Dependency GraphID: %s", depNode.GraphID.String())
				t.Logf("  - Dependency OutputIndex: %d", depNode.OutputIndex)
				t.Logf("  - Dependency has AncillaryBeef: %d bytes", len(depNode.AncillaryBeef))
				t.Logf("  - Dependency RawTx (first 100 chars): %s...", depNode.RawTx[:min(100, len(depNode.RawTx))])
				
				// Verify what transaction ID the RawTx actually represents
				if verifyTx, err := transaction.NewTransactionFromHex(depNode.RawTx); err != nil {
					t.Logf("  - Failed to parse returned RawTx: %v", err)
				} else {
					t.Logf("  - Returned RawTx actually represents transaction: %s", verifyTx.TxID().String())
					if verifyTx.TxID().String() == "af18d62ae847dfc8f50a5e0b453830deac249b9a88d78b0818ccd502f09a5068" {
						t.Logf("  - ✅ SUCCESS: RawTx contains the correct dependency transaction!")
					} else {
						t.Logf("  - ❌ ERROR: RawTx contains wrong transaction!")
					}
				}
				
				// Test AppendToGraph for dependency
				spentBy := &transaction.Outpoint{Txid: *txid, Index: node.OutputIndex}
				t.Logf("Calling AppendToGraph for dependency with spentBy: %s", spentBy.String())
				if err := gaspStorage.AppendToGraph(ctx, depNode, spentBy); err != nil {
					t.Logf("AppendToGraph failed for dependency %s: %v", outpointStr, err)
				} else {
					t.Logf("AppendToGraph succeeded for dependency %s", outpointStr)
				}
				
				// Check if this dependency has its own missing inputs
				t.Logf("Checking if dependency %s has its own missing inputs...", outpointStr)
				
				// Parse dependency transaction to understand what we're testing
				depTx, err := transaction.NewTransactionFromHex(depNode.RawTx)
				if err != nil {
					t.Logf("Failed to parse dependency transaction: %v", err)
				} else {
					t.Logf("Dependency transaction %s:", depTx.TxID().String())
					t.Logf("  - Has proof: %v", depNode.Proof != nil)
					t.Logf("  - Has AncillaryBeef: %d bytes", len(depNode.AncillaryBeef))
					t.Logf("  - Input count: %d", len(depTx.Inputs))
					for i, input := range depTx.Inputs {
						inputOutpoint := &transaction.Outpoint{
							Txid:  *input.SourceTXID,
							Index: input.SourceTxOutIndex,
						}
						t.Logf("    Input %d: %s", i, inputOutpoint.String())
					}
					
					// Test what the topic manager says it needs for this dependency
					t.Logf("Testing what topic manager needs for dependency validation...")
					if depNode.Proof != nil {
						if depTx.MerklePath, err = transaction.NewMerklePathFromHex(*depNode.Proof); err != nil {
							t.Logf("Failed to parse dependency proof: %v", err)
						}
					}
					
					depBeef, err := transaction.NewBeefFromTransaction(depTx)
					if err != nil {
						t.Logf("Failed to create BEEF for dependency: %v", err)
					} else {
						if len(depNode.AncillaryBeef) > 0 {
							if err := depBeef.MergeBeefBytes(depNode.AncillaryBeef); err != nil {
								t.Logf("Failed to merge dependency AncillaryBeef: %v", err)
							}
						}
						
						if depBeefBytes, err := depBeef.AtomicBytes(depTx.TxID()); err != nil {
							t.Logf("Failed to get dependency BEEF bytes: %v", err)
						} else if tmNeeds, err := topicManager.IdentifyNeededInputs(ctx, depBeefBytes); err != nil {
							t.Logf("Topic manager IdentifyNeededInputs failed for dependency: %v", err)
						} else {
							t.Logf("Topic manager says dependency needs %d inputs:", len(tmNeeds))
							for _, needsOutpoint := range tmNeeds {
								t.Logf("  - Topic manager needs: %s", needsOutpoint.String())
							}
						}
					}
				}
				
				// Now test GASP's FindNeededInputs 
				depNeededInputs, err := gaspStorage.FindNeededInputs(ctx, depNode)
				if err != nil {
					t.Logf("GASP FindNeededInputs failed for dependency %s: %v", outpointStr, err)
				} else if depNeededInputs != nil {
					t.Logf("GASP FindNeededInputs says dependency %s has %d missing inputs:", outpointStr, len(depNeededInputs.RequestedInputs))
					for depOutpointStr := range depNeededInputs.RequestedInputs {
						t.Logf("  - GASP says dependency needs: %s", depOutpointStr)
					}
				} else {
					t.Logf("GASP FindNeededInputs: dependency %s has no missing inputs", outpointStr)
				}
			}
		}
	} else {
		t.Logf("No missing inputs found")
	}
	
	// Step 5d: Test validation directly
	t.Logf("Step 5d: Testing ValidateGraphAnchor...")
	err = gaspStorage.ValidateGraphAnchor(ctx, node.GraphID)
	if err != nil {
		t.Logf("ValidateGraphAnchor failed: %v", err)
	} else {
		t.Logf("ValidateGraphAnchor succeeded")
	}
}