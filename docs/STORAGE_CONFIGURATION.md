# Storage Configuration Guide

The BSV21 overlay service supports multiple storage backends for different use cases. This guide explains how to configure and select storage implementations.

## Storage Types

### 1. BEEF Storage (Required)

BEEF (Bitcoin Extended Format) storage is required for all deployments. It stores the raw transaction data needed for SPV validation.

**Configuration:**
```bash
REDIS_BEEF=redis://localhost:6379
```

The BEEF storage is always Redis-based because it requires:
- Fast key-value lookups
- TTL support for cache expiration
- Minimal storage overhead

### 2. Event Storage (Select One)

Event storage holds the indexed BSV21 token events. You can choose between three implementations:

#### MongoDB (Recommended for Production)

Best for production deployments with large datasets.

**Pros:**
- Rich query capabilities
- Built-in aggregation pipeline
- Decimal128 type for proper uint64 handling
- Horizontal scaling support

**Configuration:**
```bash
MONGO_URL=mongodb://localhost:27017
EVENT_STORAGE=mongo
DB_NAME=bsv21  # Optional, defaults to "mnee"
```

#### Redis (High Performance)

Best for high-speed, in-memory operations.

**Pros:**
- Extremely fast queries
- Built-in pub/sub for real-time events
- Sorted sets for efficient range queries

**Cons:**
- Limited by available memory
- Less rich query capabilities

**Configuration:**
```bash
REDIS=redis://localhost:6379
EVENT_STORAGE=redis
```

Note: Can use the same Redis instance as BEEF storage or a separate one.

#### SQLite (Lightweight)

Best for single-node deployments or development.

**Pros:**
- No external dependencies
- File-based storage
- Good for small to medium datasets

**Cons:**
- Limited concurrent write performance
- No built-in horizontal scaling

**Configuration:**
```bash
SQLITE_PATH=./data/events.db
EVENT_STORAGE=sqlite
```

## Implementation in Code

To support storage selection, modify the server initialization:

```go
// cmd/server/server.go
func initializeEventStorage(ctx context.Context) (events.EventLookup, error) {
    storageType := os.Getenv("EVENT_STORAGE")
    if storageType == "" {
        storageType = "mongo" // Default
    }

    switch storageType {
    case "mongo":
        mongoURL := os.Getenv("MONGO_URL")
        if mongoURL == "" {
            mongoURL = "mongodb://localhost:27017"
        }
        dbName := os.Getenv("DB_NAME")
        if dbName == "" {
            dbName = "mnee"
        }
        return events.NewMongoEventLookup(mongoURL, dbName, store)
        
    case "redis":
        redisURL := os.Getenv("REDIS")
        if redisURL == "" {
            return nil, errors.New("REDIS environment variable required for Redis event storage")
        }
        return events.NewRedisEventLookup(redisURL, store, "bsv21")
        
    case "sqlite":
        dbPath := os.Getenv("SQLITE_PATH")
        if dbPath == "" {
            dbPath = "./events.db"
        }
        return events.NewSQLiteEventLookup(store, dbPath)
        
    default:
        return nil, fmt.Errorf("unknown event storage type: %s", storageType)
    }
}
```

## Storage Architecture

```
┌─────────────────┐     ┌─────────────────┐
│   HTTP Client   │     │ JungleBus (opt) │
└────────┬────────┘     └────────┬────────┘
         │                       │
         │ BEEF                  │ Transactions
         ▼                       ▼
┌─────────────────────────────────────────┐
│          BSV21 Overlay Service          │
├─────────────────┬───────────────────────┤
│   BEEF Storage  │    Event Storage      │
│   (Redis Only)  │  (Mongo/Redis/SQLite) │
└─────────────────┴───────────────────────┘
         │                       │
         ▼                       ▼
   Transaction Data         Event Indexes
```

## Configuration Examples

### Production Setup (High Volume)
```bash
# Separate Redis instances for optimal performance
REDIS_BEEF=redis://redis-beef:6379
MONGO_URL=mongodb://mongo-cluster:27017/bsv21?replicaSet=rs0
EVENT_STORAGE=mongo
DB_NAME=bsv21_mainnet
```

### Development Setup
```bash
# Single Redis for simplicity
REDIS_BEEF=redis://localhost:6379
SQLITE_PATH=./dev-events.db
EVENT_STORAGE=sqlite
```

### High-Speed Trading Setup
```bash
# All in-memory for maximum speed
REDIS_BEEF=redis://redis1:6379
REDIS=redis://redis2:6379
EVENT_STORAGE=redis
```

## Migration Between Storage Types

To migrate between storage implementations:

1. Export data from current storage
2. Stop the service
3. Update EVENT_STORAGE configuration
4. Import data to new storage
5. Restart the service

Example migration script:
```go
// migrate.go
func migrateEvents(from, to events.EventLookup) error {
    // Implementation depends on specific migration needs
    // Could use the Lookup interface to query and re-save events
}
```

## Performance Considerations

### MongoDB
- Create indexes on frequently queried fields
- Use connection pooling
- Consider sharding for very large datasets

### Redis
- Monitor memory usage
- Use appropriate eviction policies
- Consider Redis Cluster for scaling

### SQLite
- Enable WAL mode for better concurrency
- Use prepared statements
- Keep database file on fast storage (SSD)

## Monitoring

Monitor storage health based on implementation:

### MongoDB
```bash
# Check connection
mongo --eval "db.stats()"

# Monitor performance
mongostat
```

### Redis
```bash
# Check memory usage
redis-cli INFO memory

# Monitor commands
redis-cli MONITOR
```

### SQLite
```bash
# Check database size
ls -lh events.db

# Analyze database
sqlite3 events.db "ANALYZE;"
```