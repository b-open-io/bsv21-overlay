# Implementation Notes for Storage Configuration

This document outlines the changes needed to support configurable storage backends in the BSV21 overlay service.

## Current State

The service currently has hard-coded dependencies on MongoDB:

1. **Server initialization** (cmd/server/server.go):
   - Line 167: `storage.NewMongoStorage()` - hard-coded MongoDB storage
   - Line 173: `lookups.NewBsv21EventsLookup()` - internally uses MongoDB
   - Line 157: Publisher requires Redis (for pub/sub)

2. **BSV21 Lookup** (lookups/bsv21-events-lookup.go):
   - Embeds `events.MongoEventLookup`
   - Constructor calls `events.NewMongoEventLookup()`

3. **Process service** (cmd/process/process.go):
   - Similar hard-coded MongoDB initialization

## Required Changes

### 1. Abstract the BSV21 Lookup

Create a generic BSV21 lookup that can use any event storage backend:

```go
// lookups/bsv21-events-lookup.go
type Bsv21EventsLookup struct {
    events.EventLookup // Interface instead of concrete type
}

func NewBsv21EventsLookup(eventLookup events.EventLookup) (*Bsv21EventsLookup, error) {
    return &Bsv21EventsLookup{
        EventLookup: eventLookup,
    }, nil
}
```

### 2. Storage Factory Function

Create a factory to initialize the correct storage based on configuration:

```go
// internal/config/storage.go
func InitializeEventLookup(ctx context.Context, storage engine.Storage) (events.EventLookup, error) {
    storageType := os.Getenv("EVENT_STORAGE")
    if storageType == "" {
        storageType = "mongo" // Default for backward compatibility
    }

    switch storageType {
    case "mongo":
        mongoURL := os.Getenv("MONGO_URL")
        if mongoURL == "" {
            mongoURL = "mongodb://localhost:27017"
        }
        dbName := os.Getenv("DB_NAME")
        if dbName == "" {
            dbName = "mnee" // Keep default for compatibility
        }
        return events.NewMongoEventLookup(mongoURL, dbName, storage)

    case "redis":
        redisURL := os.Getenv("REDIS")
        if redisURL == "" {
            return nil, errors.New("REDIS required for Redis event storage")
        }
        topic := os.Getenv("TOPIC_NAME")
        if topic == "" {
            topic = "bsv21"
        }
        return events.NewRedisEventLookup(redisURL, storage, topic)

    case "sqlite":
        dbPath := os.Getenv("SQLITE_PATH")
        if dbPath == "" {
            dbPath = "./events.db"
        }
        return events.NewSQLiteEventLookup(storage, dbPath)

    default:
        return nil, fmt.Errorf("unknown EVENT_STORAGE type: %s", storageType)
    }
}
```

### 3. Update Server Initialization

```go
// cmd/server/server.go
// After initializing storage...

// Initialize event lookup based on configuration
eventLookup, err := config.InitializeEventLookup(ctx, store)
if err != nil {
    log.Fatalf("Failed to initialize event lookup: %v", err)
}

// Initialize BSV21 lookup service with the event lookup
bsv21Lookup, err = lookups.NewBsv21EventsLookup(eventLookup)
if err != nil {
    log.Fatalf("Failed to initialize bsv21 lookup: %v", err)
}
```

### 4. Configuration for Hard-coded Values

Add environment variables for all hard-coded values:

```go
// Database/Queue names
DB_NAME=bsv21           // Default: "mnee"
QUEUE_NAME=bsv21_queue  // Default: "mnee"

// Service identifiers  
LOOKUP_SERVICE_ID=ls_bsv21  // Default: "ls_bsv21"
TOPICS_SET_NAME=topics      // Default: "topics"

// ARC configuration
ARC_API_URL=https://arc.taal.com  // Default shown
ARC_WAIT_FOR=ACCEPTED_BY_NETWORK  // Default shown

// Processing limits
QUEUE_CONCURRENCY=16      // Default: 16
QUEUE_BATCH_SIZE=1000    // Default: 1000
API_MAX_LIMIT=1000       // Default: 1000
API_DEFAULT_LIMIT=100    // Default: 100
```

### 5. Publisher Configuration

The publisher is currently hard-coded to Redis. For non-Redis event storage, we might need:

```go
func InitializePublisher() (publish.Publisher, error) {
    // For Redis event storage, use Redis publisher
    if os.Getenv("EVENT_STORAGE") == "redis" || os.Getenv("REDIS") != "" {
        return publish.NewRedisPublish(os.Getenv("REDIS"))
    }
    
    // For other storage types, could use a no-op publisher
    // or implement MongoDB/SQLite publishers
    return publish.NewNoOpPublish(), nil
}
```

## Migration Path

1. **Phase 1**: Add configuration loading without changing behavior
2. **Phase 2**: Implement storage factory and update initialization
3. **Phase 3**: Test with different storage backends
4. **Phase 4**: Update documentation and provide migration guides

## Testing Strategy

1. Create integration tests for each storage backend
2. Ensure consistent behavior across all implementations
3. Performance benchmarks for each storage type
4. Migration tests between storage types

## Backward Compatibility

- All new environment variables should have defaults matching current values
- Existing deployments should work without any configuration changes
- Clear documentation on new options without requiring immediate adoption