# BSV21-Overlay Modularization Plan

## Goal
Refactor `cmd/server/server.go` from a ~500 line monolith into a modular architecture where sync services (GASP, SSE, LibP2P) are reusable libraries that other systems can pull in.

## Current State

The server handles 7 concerns in one file:

| Concern | Lines | Description |
|---------|-------|-------------|
| Configuration & Logging | 77-105 | Load config, set log level |
| Service Initialization | 121-125 | Create storage, chaintracks, broadcaster via `config.Initialize()` |
| Block Processing | 169-195 | Subscribe to new blocks, reconcile merkle roots |
| Engine & Topic Management | 198-235 | Initialize engine, register topic managers, configure sync |
| GASP/SSE/LibP2P Sync | 238-346 | Three sync mechanisms in goroutines |
| Periodic Topic Updates | 349-366 | Background ticker to refresh topic registrations |
| HTTP Server & Routes | 368-433 | Fiber setup, route registration, shutdown |

### What's Already Modular
- `config/config.go` - Shared library config (embeddable)
- `config/services.go` - Service initialization returning `Services` struct
- `routes/` package - Route handlers
- `topics/` package - Topic managers
- `lookups/` package - Lookup services
- `peer/topics.go` - `RegisterTopics` function

---

## Proposed New Structure

```
bsv21-overlay/
├── service/
│   ├── interface.go           # NEW: BSV21Service interface
│   ├── embedded/
│   │   └── embedded.go        # NEW: In-process implementation
│   ├── topics/
│   │   └── registration.go    # NEW: Topic registration service
│   └── gasp/
│       └── factory.go         # NEW: BSV21 topic manager factory
└── cmd/server/
    └── server.go              # SLIM: ~150 lines, just wiring

overlay/  (separate repo)
└── sync/
    ├── gasp.go                # NEW: GASPSyncManager (reusable by any overlay)
    └── merkle.go              # NEW: MerkleReconciler (reusable by any overlay)
```

---

## Implementation Phases

### Phase 1: Create Service Interface
**File:** `service/interface.go` (~40 lines)

```go
type BSV21Service interface {
    GetEngine() *engine.Engine
    GetStorage() *storage.EventDataStorage
    GetActiveTopics() *topics.ActiveTopics
    GetPeerBroadcaster() *pubsub.PeerBroadcaster
    GetLibP2PSync() *pubsub.LibP2PSync
    Start(ctx context.Context) error
    Stop()
}
```

**Purpose:** Define the contract that can be satisfied by:
- Embedded implementation (in-process)
- REST client (future, for remote access)

---

### Phase 2: Create GASPSyncManager in overlay repo
**File:** `../overlay/sync/gasp.go` (~120 lines)

Extract GASP sync logic (server.go:238-286) into reusable manager:

```go
type TopicManagerFactory func(topicId string, store *storage.EventDataStorage) engine.TopicManager

type GASPSyncConfig struct {
    Engine          *engine.Engine
    Storage         *storage.EventDataStorage
    TopicManagerFn  TopicManagerFactory  // Allows BSV21-specific or other topic managers
    RestartInterval time.Duration
    Context         context.Context
}

type GASPSyncManager struct { ... }

func RegisterGASPSync(config *GASPSyncConfig) (*GASPSyncManager, error)
func (m *GASPSyncManager) Start() error
func (m *GASPSyncManager) Stop()
```

**Key design:** The `TopicManagerFactory` allows each overlay (BSV21, ordinals, etc.) to provide its own topic manager implementation while sharing the GASP sync logic.

---

### Phase 3: Create Merkle Root Reconciliation Service in overlay repo
**File:** `../overlay/sync/merkle.go` (~80 lines)

Extract merkle root reconciliation (server.go:169-195) to overlay repo (generic, not BSV21-specific):

```go
type MerkleReconcilerConfig struct {
    Storage     *storage.EventDataStorage
    Chaintracks chaintracks.Chaintracks
    Context     context.Context
    Logger      *slog.Logger
}

type MerkleReconciler struct { ... }

func NewMerkleReconciler(config *MerkleReconcilerConfig) (*MerkleReconciler, error)
func (mr *MerkleReconciler) Start() error
func (mr *MerkleReconciler) Stop()
```

**Responsibilities:**
- Subscribe to chaintracks block notifications (as trigger)
- Reconcile validated merkle roots when new blocks arrive
- Generic logic reusable by any overlay

---

### Phase 4: Create Topic Registration Service
**File:** `service/topics/registration.go` (~100 lines)

Extract periodic topic registration (server.go:349-366):

```go
type TopicRegistrationConfig struct {
    Engine         *engine.Engine
    Storage        *storage.EventDataStorage
    ActiveTopics   *topics.ActiveTopics
    PeerTopics     map[string][]string
    UpdateInterval time.Duration
    Context        context.Context
}

type TopicRegistrationManager struct { ... }

func NewTopicRegistrationManager(config *TopicRegistrationConfig) (*TopicRegistrationManager, error)
func (m *TopicRegistrationManager) Start() error
func (m *TopicRegistrationManager) Stop()
func (m *TopicRegistrationManager) UpdatePeerTopics(peerTopics map[string][]string)
```

---

### Phase 5: Create BSV21 Topic Manager Factory
**File:** `service/gasp/factory.go` (~30 lines)

BSV21-specific factory for GASP sync:

```go
func NewBSV21TopicManagerFactory() sync.TopicManagerFactory {
    return func(topicId string, store *storage.EventDataStorage) engine.TopicManager {
        tokenId := strings.TrimPrefix(topicId, "tm_")
        return bsv21topics.NewBsv21ValidatedTopicManager(topicId, store, []string{tokenId})
    }
}
```

---

### Phase 6: Create Embedded Implementation
**File:** `service/embedded/embedded.go` (~250 lines)

Orchestrates all services following the arcade pattern:

```go
type Config struct {
    Services     *config.Services
    HostingURL   string
    EnableGASP   bool
    EnableSSE    bool
    EnableLibP2P bool
    Logger       *slog.Logger
}

type Embedded struct {
    services           *config.Services
    engine             *engine.Engine
    activeTopics       *topics.ActiveTopics
    bsv21Lookup        *lookups.Bsv21EventsLookup
    peerBroadcaster    *pubsub.PeerBroadcaster

    // Sync managers
    merkleReconciler   *sync.MerkleReconciler
    gaspSyncManager    *sync.GASPSyncManager
    sseSyncManager     *sync.SSESyncManager
    libp2pSyncManager  *sync.LibP2PSyncManager
    topicRegistration  *servicetopics.TopicRegistrationManager

    logger *slog.Logger
    ctx    context.Context
    cancel context.CancelFunc
}

func New(ctx context.Context, cfg Config) (*Embedded, error)
func (e *Embedded) Start(ctx context.Context) error
func (e *Embedded) Stop()
// ... interface method implementations
```

---

### Phase 7: Refactor server.go
**File:** `cmd/server/server.go` (reduce from ~500 to ~150 lines)

Pure wiring/orchestration:

```go
func runServer(cmd *cobra.Command, args []string) {
    // 1. Load config
    cfg, _ := Load()

    // 2. Apply CLI overrides
    // 3. Configure logging
    // 4. Create context
    // 5. Start pprof if enabled

    // 6. Initialize base services
    services, _ := cfg.Initialize(ctx, logger, nil)
    defer services.Close()

    // 7. Create embedded BSV21 service
    bsv21Service, _ := embedded.New(ctx, embedded.Config{
        Services:     services,
        HostingURL:   cfg.Hosting.URL,
        EnableGASP:   syncFlag,
        EnableSSE:    syncFlag,
        EnableLibP2P: cfg.Sync.LibP2P,
    })

    // 8. Start service
    bsv21Service.Start(ctx)
    defer bsv21Service.Stop()

    // 9. Setup signal handling
    // 10. Create Fiber app with routes
    // 11. Start HTTP server
}
```

---

## Key Benefits

### 1. GASP Sync as a Library
After refactoring, other systems can pull in GASP sync:

```go
import "github.com/b-open-io/overlay/sync"

gaspManager, _ := sync.RegisterGASPSync(&sync.GASPSyncConfig{
    Engine:         myEngine,
    Storage:        myStorage,
    TopicManagerFn: myFactory,  // Custom topic managers
    Context:        ctx,
})
gaspManager.Start()
defer gaspManager.Stop()
```

### 2. Embeddable Service
1sat-indexer or other consumers can embed the full BSV21 service:

```go
import "github.com/b-open-io/bsv21-overlay/service/embedded"

bsv21, _ := embedded.New(ctx, embedded.Config{
    Services:   myServices,
    EnableGASP: true,
})
bsv21.Start(ctx)

// Access internals
engine := bsv21.GetEngine()
storage := bsv21.GetStorage()
```

### 3. Testability
Each service can be tested in isolation with mock dependencies.

---

## Compatibility

- **Routes unchanged:** All existing route registration continues to work
- **Config pattern preserved:** `config.Services` and `Initialize()` unchanged
- **1sat-indexer compatible:** Routes accept same types as before
- **Follows existing patterns:** GASPSyncManager mirrors SSESyncManager/LibP2PSyncManager

---

## Open Questions

1. **Should `createBSV21Catchup` move to routes package?** It's currently in server.go but is route-specific logic.

2. **Should we add a REST client implementation of BSV21Service?** This would allow remote access to a BSV21 overlay, similar to how arcade has embedded vs client.

3. **Should TopicRegistrationManager be in overlay repo instead of bsv21-overlay?** It's fairly generic logic.

4. **Naming:** Is `BSV21Service` the right name, or should it be more specific like `BSV21OverlayService`?

---

## Files to Create/Modify

| File | Action | Lines |
|------|--------|-------|
| `service/interface.go` | Create | ~40 |
| `../overlay/sync/gasp.go` | Create | ~120 |
| `../overlay/sync/merkle.go` | Create | ~80 |
| `service/topics/registration.go` | Create | ~100 |
| `service/gasp/factory.go` | Create | ~30 |
| `service/embedded/embedded.go` | Create | ~250 |
| `cmd/server/server.go` | Refactor | ~150 (down from ~500) |

**Total new code:** ~620 lines across 7 new files (2 in overlay repo, 5 in bsv21-overlay)
**Net change:** Server.go reduced by ~350 lines, moved to reusable services
