# BSV21 Overlay Service

A high-performance overlay service for BSV21 tokens on the Bitcoin SV blockchain. The service provides HTTP APIs for submitting transactions, querying token events, calculating balances, and streaming real-time updates.

## Overview

The BSV21 overlay service is a complete solution for working with BSV21 tokens:

- **Submit** BSV21 transactions via HTTP API
- **Query** token events, balances, and ownership
- **Stream** real-time updates via Server-Sent Events
- **Validate** all transactions with SPV proofs

## Features

- Full BSV21 protocol support with uint64 precision for token amounts
- Event-based indexing for efficient queries
- Support for multiple locking script types:
  - P2PKH (pay-to-public-key-hash)
  - Multisig with cosigners
  - Lock-to-mint (LTM) tokens
  - Proof-of-work (POW20) tokens
  - OrdLock marketplace listings
- Multiple storage backend support (MongoDB, Redis, SQLite)
- Real-time event streaming via Server-Sent Events (SSE)
- SPV validation of all transactions
- Horizontal scaling support with peer synchronization

## Quick Start

### Prerequisites

- Go 1.21 or higher
- Redis 7.0+ (or Redis Stack)
- MongoDB 7.0+ (or compatible)

### Installation

```bash
# Clone the repository
git clone https://github.com/your-org/bsv21-overlay.git
cd bsv21-overlay

# Build the server
go build -o server.run cmd/server/server.go

# Or use the build script
./build.sh
```

### Configuration

The service uses connection strings that auto-detect the storage type:

```bash
# Event Storage (Required - auto-detects type from URL)
export EVENTS_URL=mongodb://user:pass@localhost:27017/bsv21?authSource=admin  # MongoDB
# OR
export EVENTS_URL=redis://localhost:6379           # Redis
# OR
export EVENTS_URL=./overlay.db                     # SQLite (local file)

# BEEF Storage (Optional - defaults to ./beef_storage/)
# Single storage backend:
export BEEF_URL=redis://localhost:6379            # Redis (recommended for production)
# OR
export BEEF_URL=mongodb://user:pass@localhost:27017/beef?authSource=admin    # MongoDB
# OR
export BEEF_URL=./beef.db                         # SQLite
# OR leave unset to use ./beef_storage/ directory     # Filesystem (default)

# Hierarchical storage (stack multiple backends):
export BEEF_URL='["lru://1gb", "redis://localhost:6379", "junglebus://"]'  # JSON array
# OR
export BEEF_URL="lru://100mb,redis://localhost:6379,junglebus://"         # Comma-separated

# Publisher Configuration (Optional - needed for real-time events)
export PUBLISHER_URL=redis://localhost:6379           # Publisher for pub/sub (optional)

# Service Configuration
export PORT=3000
export HOSTING_URL=http://localhost:3000

# Block Headers Service (for SPV validation)
export HEADERS_URL=https://api.whatsonchain.com/v1/bsv/main/block
export HEADERS_KEY=your_api_key

# Optional: Peer sync for horizontal scaling
export PEERS=http://peer1:3000,http://peer2:3000

# Optional: ARC Integration
export ARC_API_KEY=your_arc_api_key
export ARC_CALLBACK_TOKEN=your_callback_token
```

#### Common Configuration Examples

**Development (minimal setup):**
```bash
export PUBLISHER_URL=redis://localhost:6379
# EVENTS_URL defaults to ./overlay.db
# BEEF_URL defaults to ./beef_storage/
```

**Production (high performance):**
```bash
export EVENTS_URL=mongodb://user:pass@localhost:27017/bsv21?authSource=admin
export BEEF_URL=redis://localhost:6379
export PUBLISHER_URL=redis://localhost:6379
```

**All Redis:**
```bash
export EVENTS_URL=redis://localhost:6379
export BEEF_URL=redis://localhost:6379
export PUBLISHER_URL=redis://localhost:6379
```

For development convenience, you can create a `.env` file in the project root with these variables (without the `export` prefix), which will be automatically loaded when running from the source directory.

### Running the Service

```bash
# Start the BSV21 overlay server
./server.run

# Or with custom port
./server.run -p 8080

# Or run with environment variables set inline
EVENTS_URL=mongodb://user:pass@mongo-host:27017/bsv21?authSource=admin BEEF_URL=redis://redis-host:6379 ./server.run
```

The server will start on port 3000 by default. You can now:
- Submit transactions to `/submit`
- Query events at `/1sat/events/:event`
- Check balances at `/1sat/bsv21/:event/balance`
- Subscribe to real-time updates at `/subscribe/:topics`

## API Documentation

### Event Queries

#### Get Events by Type
```
GET /1sat/events/:event
```

Query events by type with pagination support.

**Parameters:**
- `event` - Event type (e.g., `id:tokenId`, `p2pkh:address:tokenId`, `sym:SYMBOL`)
- `from` - Starting score as float64 (optional)
- `limit` - Number of results (max 1000, default 100)

**Example:**
```bash
# Get all events for a token
curl http://localhost:3000/1sat/events/id:36b8aeff1d04e07d1d6ea6d58e0e7c0860cd0c86b5a37a44166f84eb5643f5ff_1

# Get events for a specific address and token
curl http://localhost:3000/1sat/events/p2pkh:1F5VhMHukdnUES9kfXqzPzMeF1GPHKiF64:36b8aeff1d04e07d1d6ea6d58e0e7c0860cd0c86b5a37a44166f84eb5643f5ff_1
```

#### Get Unspent Events
```
GET /1sat/events/:event/unspent
```

Query only unspent outputs for an event type.

**Parameters:**
- `event` - Event type
- `from` - Starting score as float64 (optional)
- `limit` - Number of results (max 1000, default 100)

**Example:**
```bash
# Get unspent outputs for a token
curl http://localhost:3000/1sat/events/id:36b8aeff1d04e07d1d6ea6d58e0e7c0860cd0c86b5a37a44166f84eb5643f5ff_1/unspent
```

#### Get Token Information
```
GET /1sat/bsv21/:tokenId
```

Get detailed information about a BSV21 token.

**Example:**
```bash
# Get token details
curl http://localhost:3000/1sat/bsv21/36b8aeff1d04e07d1d6ea6d58e0e7c0860cd0c86b5a37a44166f84eb5643f5ff_1
```

#### Get Balance for Address
```
GET /1sat/bsv21/:tokenId/:lockType/:address/balance
```

Calculate the balance for a specific address and lock type.

**Parameters:**
- `tokenId` - Token identifier (txid_vout format)
- `lockType` - Type of locking script (e.g., `p2pkh`, `cos`, `ltm`)
- `address` - Address or identifier

**Example:**
```bash
# Get balance for P2PKH address
curl http://localhost:3000/1sat/bsv21/36b8aeff1d04e07d1d6ea6d58e0e7c0860cd0c86b5a37a44166f84eb5643f5ff_1/p2pkh/1F5VhMHukdnUES9kfXqzPzMeF1GPHKiF64/balance
```

#### Get Balance for Multiple Addresses
```
POST /1sat/bsv21/:tokenId/:lockType/balance
```

Calculate the combined balance for multiple addresses.

**Body:** JSON array of addresses

**Example:**
```bash
# Get combined balance for multiple addresses
curl -X POST http://localhost:3000/1sat/bsv21/36b8aeff.../p2pkh/balance \
  -H "Content-Type: application/json" \
  -d '["1F5VhMHukdnUES9kfXqzPzMeF1GPHKiF64", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"]'
```

### Block Information

#### Get Current Block Tip
```
GET /1sat/block/tip
```

Get information about the current blockchain tip.

**Example:**
```bash
curl http://localhost:3000/1sat/block/tip
```

#### Get Block by Height
```
GET /1sat/block/:height
```

Get block header information for a specific height.

**Example:**
```bash
curl http://localhost:3000/1sat/block/850000
```

#### Get Token Transactions at Block Height
```
GET /1sat/bsv21/:tokenId/block/:height
```

Get all transactions for a specific token at a given block height.

**Example:**
```bash
curl http://localhost:3000/1sat/bsv21/36b8aeff1d04e07d1d6ea6d58e0e7c0860cd0c86b5a37a44166f84eb5643f5ff_1/block/850000
```

### Real-time Events

#### Subscribe to Events
```
GET /1sat/subscribe/:topics
```

Subscribe to real-time events via Server-Sent Events (SSE).

**Parameters:**
- `topics` - Comma-separated list of event types to subscribe to

**Headers:**
- `Last-Event-ID` - Resume from a specific score (optional)

**Example:**
```javascript
const eventSource = new EventSource('http://localhost:3000/1sat/subscribe/id:tokenId1,p2pkh:address:tokenId2');
eventSource.onmessage = (event) => {
    console.log('New event:', event.data);
    console.log('Event score:', event.lastEventId);
};
```

### Transaction Submission

#### Submit BEEF Transaction
```
POST /submit
```

Submit a tagged BEEF transaction for processing.

**Headers:**
- `x-topics` - Comma-separated list of topics this transaction belongs to
- `Content-Type: application/octet-stream`

**Body:** Raw BEEF bytes

## Event Types

The BSV21 overlay indexes the following event types:

- `id:{tokenId}` - All events for a specific token
- `sym:{symbol}` - Events by token symbol (mint operations only)
- `p2pkh:{address}:{tokenId}` - P2PKH outputs for address and token
- `cos:{address}:{tokenId}` - Cosigner outputs
- `ltm:{tokenId}` - Lock-to-mint token events
- `pow20:{tokenId}` - Proof-of-work token events
- `list:{seller}:{tokenId}` - OrdLock marketplace listings
- `list:{tokenId}` - All listings for a token

## Architecture

### Core Components

The BSV21 overlay service is built around an event-driven architecture:

1. **Transaction Submission** - Clients submit BEEF transactions via HTTP API
2. **Event Processing** - Transactions are parsed and indexed as events
3. **Storage Layer** - Events are stored with full SPV validation
4. **Query Engine** - Efficient lookups by token ID, address, or other criteria
5. **Real-time Streaming** - Server-Sent Events for live updates

### Storage

The service uses two types of storage with auto-detection from connection strings:

#### BEEF Storage
Stores raw transaction data (BEEF - Bitcoin Extended Format) for SPV validation.

**Single Backend Options (auto-detected from URL):**
- **Redis** (`redis://...`): High-performance key-value storage (recommended for production)
- **MongoDB** (`mongodb://...`): Document storage with GridFS for large transactions
- **SQLite** (`./beef.db`): Local file database for development
- **Filesystem** (`./beef_storage/`): Directory-based storage (default if `BEEF_URL` not set)
- **JungleBus** (`junglebus://`): Fetches from JungleBus API (read-only)

**Hierarchical Storage:**

You can stack multiple storage backends to create a hierarchical cache system. Each layer checks its storage first, then falls back to the next layer if data is not found.

**Supported Formats:**
- JSON array: `["lru://100mb", "redis://localhost:6379", "junglebus://"]`
- Comma-separated: `"lru://100mb,redis://localhost:6379,junglebus://"`
- Single string: `"redis://localhost:6379"` (backwards compatible)

**Available Backends for Stacking:**
- `lru://100mb` or `lru://1gb` - In-memory LRU cache with size limit
- `redis://host:port?ttl=24h` - Redis cache with optional TTL (e.g., ?ttl=1h, ?ttl=30m)
  - TTL requires HEXPIRE support (Redis 7.4+ or compatible servers)
  - Automatically detects support at connection time
- `mongodb://host:port/db` - MongoDB storage
- `sqlite://path/to/db` or `./beef.db` - SQLite database
- `file://path/to/dir` or `./beef_storage/` - Filesystem storage
- `junglebus://` or `junglebus://custom.host.com` - JungleBus API (defaults to junglebus.gorillapool.io)

**Example Configurations:**

```bash
# High-performance production stack
export BEEF_URL='["lru://1gb", "redis://localhost:6379", "sqlite://./beef.db", "junglebus://"]'
# Data flows: Memory → Redis → SQLite → JungleBus API

# Development stack with fallback
export BEEF_URL="lru://100mb,./beef.db,junglebus://"
# Data flows: Memory → Local SQLite → JungleBus API

# Simple Redis with JungleBus fallback
export BEEF_URL="redis://localhost:6379,junglebus://"
# Data flows: Redis → JungleBus API
```

When data is found in a lower layer, it's automatically cached in upper layers for faster future access. This creates an efficient multi-tier caching system.

**Note:** If your connection strings contain commas, use the JSON array format to properly escape them.

#### Event Storage
Stores processed BSV21 events with indexing for efficient queries.

**Options (auto-detected from URL):**
- **MongoDB** (`mongodb://...`): Full-featured storage with Decimal128 for uint64 amounts
  - Include `?authSource=admin` in connection string if authenticating against admin database
  - Example: `mongodb://user:pass@host:27017/dbname?authSource=admin`
- **Redis** (`redis://...`): High-performance in-memory event storage
- **SQLite** (`./overlay.db`): Lightweight file-based storage for single-node deployments

Note: `PUBLISHER_URL` is optional. If not provided, the service won't publish real-time events but will still function normally for storage and queries.

### Event Value Storage

The event system supports storing arbitrary values with events:
- BSV21 token amounts are stored as uint64 values
- Values are aggregated using the `ValueSumUint64` method
- Storage backends handle large uint64 values correctly:
  - MongoDB: Uses Decimal128 to avoid int64 overflow
  - Redis: Stores as strings, parses as uint64
  - SQLite: Stores as BLOB, casts to INTEGER for queries

## Advanced Configuration

### Hard-coded Values

The following values are currently hard-coded but can be made configurable:

| Value | Current | Location | Purpose |
|-------|---------|----------|---------|
| Database name | Extracted from URL | MongoDB driver | MongoDB database name (e.g., `/bsv21` in connection string) |
| Whitelist key | `bsv21:whitelist` | process.go | Redis key for token whitelist |
| Topic prefix | `tok:` | process.go | Redis key prefix for token queues |
| Concurrency | `16` | process.go | Goroutine pool size |
| API limit | `1000` | server.go | Maximum results per query |
| Default database | `overlay` | storage/mongo.go | Default MongoDB database if not in URL |
| Default BEEF path | `./beef_storage/` | beef/factory.go | Default filesystem storage for BEEF |
| Default event DB | `./overlay.db` | storage/factory.go | Default SQLite database for events |

### Performance Tuning

- Adjust concurrency limits based on available CPU cores
- Increase batch sizes for better throughput on powerful hardware
- Configure MongoDB indexes for your query patterns
- Use separate Redis instances for queues and BEEF storage

## Development

### Testing

```bash
# Run all tests
go test ./...

# Run specific test
go test ./lookups -run TestBsv21Events
```

### Building Documentation

```bash
# Generate Go documentation
go doc -all ./...
```

## Troubleshooting

### Services won't start
- Ensure Redis and MongoDB are running
- Check environment variables are set correctly
- Verify services are started in the correct order

### High memory usage
- Reduce batch sizes in configuration
- Lower concurrency limits
- Check Redis memory usage with `INFO memory`

### Slow processing
- Increase concurrency limits if CPU allows
- Ensure MongoDB has proper indexes

## License

[License information here]

## Contributing

[Contribution guidelines here]