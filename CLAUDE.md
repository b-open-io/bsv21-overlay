# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Overview

This is the BSV-21 overlay service, part of a larger monorepo of BSV blockchain overlay services. It handles BSV-21 token transactions, providing real-time event streaming, lookups, and SPV validation.

## Architecture

The service consists of 4 main executables that work together:

1. **sub.run** - JungleBus subscriber that listens for BSV-21 transactions
2. **queue.run** - Processes incoming transactions and queues them by token ID
3. **process.run** - Validates transactions and updates token state
4. **server.run** - HTTP API server providing lookup and streaming endpoints

### Data Flow
```
JungleBus → sub → Redis Queue → queue → Token Queues → process → SQLite/Redis
                                                                      ↓
Clients ← server ← Event Storage
```

## Build Commands

```bash
# Build all executables
./build.sh

# Or build individually:
go build -o sub.run cmd/sub/sub.go
go build -o queue.run cmd/queue/queue.go
go build -o process.run cmd/process/process.go
go build -o server.run cmd/server/server.go

# Run tests
go test ./...

# Run a specific test
go test ./sandbox -run TestName
```

## Running the Services

Services should be started in this order:
```bash
# 1. Start subscription service
./sub.run

# 2. Start queue processor
./queue.run

# 3. Start token processor
./process.run

# 4. Start API server
./server.run
```

## Configuration

Create a `.env` file with:
```
REDIS=redis://localhost:6379
BLOCK_HEADERS_URL=https://api.whatsonchain.com/v1/bsv/main/block
BLOCK_HEADERS_API_KEY=your_api_key
HOSTING_URL=http://localhost:3000
PEERS=http://peer1:3000,http://peer2:3000
JUNGLEBUS=https://texas1.junglebus.gorillapool.io
PORT=3000
```

## API Endpoints

- `POST /submit` - Submit tagged BEEF transactions (requires `x-topics` header)
- `POST /lookup` - Query BSV-21 events
- `GET /subscribe/:topics` - SSE stream for real-time events
- `POST /arc-ingest` - ARC webhook for transaction status

## Lookup Query Formats

The lookup service supports queries like:
- `id:tokenId` - Events for a specific token
- `sym:SYMBOL` - Events by token symbol
- `p2pkh:address` - Events for a P2PKH address
- `cos:address` - Events for cosigner addresses
- `ltm:tokenId` - LTM token events
- `pow20:tokenId` - POW20 token events
- `list:address` or `list:tokenId` - OrdLock listings

## Key Dependencies

Uses local module replacements (defined in parent go.mod):
- `github.com/b-open-io/go-sdk` - BSV SDK with overlay primitives
- `github.com/b-open-io/overlay` - Shared overlay utilities
- `github.com/b-open-io/go-overlay-services` - Core overlay framework

## Storage

- **Redis**: Topic management, queues, and caching
- **SQLite**: Event storage and indexing
- Topics are dynamically loaded from Redis set "topics"

## Development Tips

- Check `storage/redis/keys.go` for Redis key patterns
- Event processing logic is in `cmd/process/process.go`
- Lookup implementations are in `lookups/events/`
- Topic management is in `topics/`