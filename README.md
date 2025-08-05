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

The service is configured using environment variables:

```bash
# BEEF Storage Configuration (Required)
export REDIS_BEEF=redis://localhost:6379

# Event Storage Configuration (Choose one)
# For MongoDB event storage:
export MONGO_URL=mongodb://localhost:27017
export EVENT_STORAGE=mongo  # Options: mongo, redis, sqlite

# For Redis event storage (alternative to MongoDB):
# export REDIS=redis://localhost:6379
# export EVENT_STORAGE=redis

# For SQLite event storage (alternative to MongoDB):
# export SQLITE_PATH=./events.db
# export EVENT_STORAGE=sqlite

# Service Configuration
export PORT=3000
export HOSTING_URL=http://localhost:3000

# Block Headers Service (for SPV validation)
export BLOCK_HEADERS_URL=https://api.whatsonchain.com/v1/bsv/main/block
export BLOCK_HEADERS_API_KEY=your_api_key

# Optional: Peer sync for horizontal scaling
export PEERS=http://peer1:3000,http://peer2:3000

# Optional: ARC Integration
export ARC_API_KEY=your_arc_api_key
export ARC_CALLBACK_TOKEN=your_callback_token
```

For development convenience, you can create a `.env` file in the project root with these variables (without the `export` prefix), which will be automatically loaded when running from the source directory.

### Running the Service

```bash
# Start the BSV21 overlay server
./server.run

# Or with custom port
./server.run -p 8080

# Or run with environment variables set inline
REDIS_BEEF=redis://redis-host:6379 MONGO_URL=mongodb://mongo-host:27017 ./server.run
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
- `from` - Starting position as `height.idx` (optional)
- `limit` - Number of results (max 1000, default 100)

**Example:**
```bash
# Get all events for a token
curl http://localhost:3000/1sat/events/id:36b8aeff1d04e07d1d6ea6d58e0e7c0860cd0c86b5a37a44166f84eb5643f5ff_1

# Get events for a specific address and token
curl http://localhost:3000/1sat/events/p2pkh:1F5VhMHukdnUES9kfXqzPzMeF1GPHKiF64:36b8aeff...
```

#### Get Token Balance
```
GET /1sat/bsv21/:event/balance
```

Calculate the total balance for unspent outputs of an event type.

**Example:**
```bash
# Get total balance for a token
curl http://localhost:3000/1sat/bsv21/id:36b8aeff.../balance

# Get balance for address-specific holdings
curl http://localhost:3000/1sat/bsv21/p2pkh:1F5VhMHukdnUES9kfXqzPzMeF1GPHKiF64:36b8aeff.../balance
```

### Real-time Events

#### Subscribe to Events
```
GET /subscribe/:topics
```

Subscribe to real-time events via Server-Sent Events (SSE).

**Parameters:**
- `topics` - Comma-separated list of topics to subscribe to

**Example:**
```javascript
const eventSource = new EventSource('http://localhost:3000/subscribe/topic1,topic2');
eventSource.onmessage = (event) => {
    console.log('New event:', event.data);
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

### Lookup Service

#### Query Events
```
POST /lookup
```

Query events using the lookup service protocol.

**Body:**
```json
{
    "service": "ls_bsv21",
    "query": {
        "event": "id:tokenId",
        "from": {
            "height": 850000,
            "idx": 0
        },
        "limit": 100,
        "spent": false
    }
}
```

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

The service uses two types of storage:

#### BEEF Storage (Required)
- **Redis** (`REDIS_BEEF`): Stores raw transaction data (BEEF - Bitcoin Extended Format)
- Used for SPV validation and transaction retrieval
- Must be configured for all deployments

#### Event Storage (Choose One)
- **MongoDB** (default): Full-featured event storage with Decimal128 for uint64 amounts
- **Redis**: High-performance in-memory event storage
- **SQLite**: Lightweight file-based storage for single-node deployments

Note: The `REDIS` environment variable is only needed if using Redis for event storage or the optional JungleBus tools.

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
| Database name | `mnee` | Multiple files | MongoDB database name |
| Queue name | `mnee` | sub.go, queue.go | Main Redis queue |
| Topic ID | `9cdb5ad5...` | sub.go | JungleBus subscription |
| Start block | `883989` | sub.go | Initial sync block |
| Concurrency | `16` | queue.go, process.go | Goroutine pool size |
| Batch size | `1000` | Multiple files | Query and API limits |
| Queue size | `10000000` | sub.go | JungleBus queue capacity |

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
- Check network latency to JungleBus

## Advanced Features

### Bulk Data Ingestion with JungleBus

For deployments that need to process all BSV21 transactions on the network (not just those submitted via API), this repository includes optional JungleBus integration tools.

#### When to Use JungleBus

- Processing historical blockchain data
- Monitoring all BSV21 activity network-wide
- Building comprehensive token indexes
- Running a public BSV21 explorer

#### Components

- `sub.run` - Subscribes to JungleBus for blockchain transactions
- `queue.run` - Filters and sorts BSV21 transactions by token ID
- `process.run` - Validates and indexes transactions in bulk

#### Configuration

```bash
# Additional environment variables for JungleBus
export REDIS=redis://localhost:6379  # Queue storage (can be same as REDIS_BEEF)
export JUNGLEBUS=https://junglebus.gorillapool.io
export JUNGLEBUS_TOPIC_ID=9cdb5ad57e83efe18804f5910742d804f93ff5bc1a86831a347341b773d629be
export JUNGLEBUS_FROM_BLOCK=883989  # Optional: starting block

# Queue configuration
export QUEUE_NAME=mnee  # Redis queue name
export QUEUE_BATCH_SIZE=1000
export QUEUE_CONCURRENCY=16
```

#### Running with JungleBus

```bash
# Start all services in order
./sub.run     # 1. Subscribe to blockchain data
./queue.run   # 2. Process transaction queue
./process.run # 3. Index BSV21 transactions
./server.run  # 4. API server (same as standalone)
```

#### Architecture with JungleBus

```
Blockchain → JungleBus → sub → Redis Queue → queue → process → Event Storage
                                                                      ↓
                                                                 API Server
```

Note: The API server works the same whether using JungleBus or not. JungleBus simply provides an additional data source beyond the HTTP submission endpoint.

## License

[License information here]

## Contributing

[Contribution guidelines here]