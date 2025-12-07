# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Overview

This is the BSV-21 overlay service, part of a larger monorepo of BSV blockchain overlay services. It handles BSV-21 token transactions, providing real-time event streaming, lookups, and SPV validation.

The service is built on a **unified storage interface** that abstracts away backend-specific implementations, allowing seamless switching between Redis, MongoDB, SQLite, and filesystem storage through configuration alone.

## Architecture

The service consists of a single executable with 2 main commands:

1. **bsv21 server** - HTTP API server providing lookup, streaming endpoints, and integrated transaction processing
2. **bsv21 config** - Configuration management CLI for whitelists and peer settings

### Unified Storage Architecture

The core innovation is the **EventDataStorage** interface that provides Redis-style operations (SAdd, HSet, ZAdd, etc.) across all storage backends:

```
┌─────────────────────────────────────────────────────────────────┐
│                    Unified Storage Interface                    │
├─────────────────────────────────────────────────────────────────┤
│  SAdd, HSet, ZAdd, Get, Set, Exists, Subscribe, Publish, etc.  │
└─────────────────┬───────────┬───────────┬───────────────────────┘
                  │           │           │
            ┌─────▼─────┐ ┌───▼───┐ ┌─────▼─────┐
            │   Redis   │ │ SQLite│ │  MongoDB  │
            │           │ │       │ │           │
            │ Native    │ │ Emul- │ │ Field     │
            │ Commands  │ │ ated  │ │ Mapping   │
            └───────────┘ └───────┘ └───────────┘
```

### Data Flow
```
External Data Sources → bsv21 server → Unified Storage Interface
                                              ↓
Clients ← HTTP API ← Event Storage (Redis/MongoDB/SQLite)
```

## Build Commands

```bash
# Build the unified executable
./build.sh

# Or build individually:
go build -o bsv21 .

# Run tests
go test ./...

# Run a specific test
go test ./sandbox -run TestName
```

## Running the Services

Start the main API server:
```bash
# Start the API server with integrated processing
./bsv21 server
```

The config utility can be run as needed:
```bash
# Manage token whitelist and peer configurations
./bsv21 config whitelist add --token your_token_id
./bsv21 config peer add --token your_token_id --peer https://peer.example.com
```

## Configuration

The service uses a **factory pattern** with connection strings for backend selection. Configuration is handled through environment variables that auto-detect storage types from URL schemes.

### Environment Variables

```bash
# Event Storage - Required (auto-detects backend from URL scheme)
EVENTS_URL=mongodb://user:pass@localhost:27017/bsv21?authSource=admin  # MongoDB
# OR
EVENTS_URL=redis://localhost:6379                                      # Redis  
# OR  
EVENTS_URL=./overlay.db                                                # SQLite (default)

# BEEF Storage - Optional, supports hierarchical stacking
BEEF_URL=redis://localhost:6379                                       # Single backend
# OR
BEEF_URL='["lru://1gb", "redis://localhost:6379", "junglebus://"]'     # Hierarchical stack

# Redis for Pub/Sub - Required for queue operations and SSE streaming
REDIS_URL=redis://localhost:6379

# Service Configuration
PORT=3000
HOSTING_URL=http://localhost:3000
HEADERS_URL=https://api.whatsonchain.com/v1/bsv/main/block
HEADERS_KEY=your_api_key

# Chain Tracker Configuration (optional)
# Use a remote chaintracks server instead of running locally
CHAINTRACKS_URL=http://chaintracks.example.com:8080
# Bootstrap URL for local chaintracks initialization (only used when CHAINTRACKS_URL is not set)
BOOTSTRAP_URL=http://bootstrap.example.com/headers

# Optional: Peer synchronization
PEERS=http://peer1:3000,http://peer2:3000
JUNGLEBUS=https://texas1.junglebus.gorillapool.io
```

### No-Dependency Default Configuration

The service works out-of-the-box with minimal setup:

```bash
# Only Redis is required - everything else defaults to local files
export REDIS_URL=redis://localhost:6379
# EVENTS_URL defaults to ./overlay.db (SQLite)
# BEEF_URL defaults to ./beef_storage/ (filesystem)
```

This provides a complete working system using:
- **SQLite** for event storage
- **Go channels** for pub/sub (internal-only, no external SSE)
- **Filesystem** for BEEF storage
- **Redis** for queue operations only

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