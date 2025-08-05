# BSV21 Overlay Configuration Guide

This document outlines the configuration options for the BSV21 overlay service and proposes a system for managing hard-coded values.

## Current Configuration

### Environment Variables

The following environment variables are currently supported:

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `REDIS` | Redis connection URL | `redis://localhost:6379` | Yes |
| `REDIS_BEEF` | Separate Redis for BEEF storage | Uses `REDIS` value | No |
| `MONGO_URL` | MongoDB connection URL | `mongodb://localhost:27017` | Yes |
| `JUNGLEBUS` | JungleBus service URL | None | Yes |
| `BLOCK_HEADERS_URL` | Block headers service URL | None | Yes |
| `BLOCK_HEADERS_API_KEY` | API key for block headers | None | Yes |
| `HOSTING_URL` | Public URL of this service | None | Yes |
| `PORT` | HTTP server port | `3000` | No |
| `PEERS` | Comma-separated peer URLs | None | No |
| `ARC_API_KEY` | ARC service API key | None | No |
| `ARC_CALLBACK_TOKEN` | ARC webhook token | None | No |

### Command Line Flags

- `-p <port>` - Override the HTTP server port
- `-s` - Start in sync mode (server only)

## Proposed Configuration System

### Additional Environment Variables

To make the service more flexible, the following hard-coded values should become configurable:

#### Database Configuration
```bash
# Database name (currently hard-coded as "mnee")
BSV21_DB_NAME=bsv21

# Collection/table names
BSV21_EVENTS_COLLECTION=events
```

#### Queue Configuration
```bash
# Main queue name (currently "mnee")
BSV21_QUEUE_NAME=bsv21_queue

# Queue key prefixes
BSV21_TOKEN_QUEUE_PREFIX=tok:
BSV21_TOPIC_PREFIX=tm_

# Queue sizes
BSV21_JUNGLEBUS_QUEUE_SIZE=10000000
BSV21_CHANNEL_BUFFER_SIZE=1000
```

#### JungleBus Configuration
```bash
# Topic ID to subscribe to
BSV21_TOPIC_ID=9cdb5ad57e83efe18804f5910742d804f93ff5bc1a86831a347341b773d629be

# Starting block for initial sync
BSV21_START_BLOCK=883989
```

#### Processing Configuration
```bash
# Concurrency limits
BSV21_QUEUE_CONCURRENCY=16
BSV21_PROCESS_CONCURRENCY=16
BSV21_DOWNLOAD_CONCURRENCY=10

# Batch sizes
BSV21_QUEUE_BATCH_SIZE=1000
BSV21_API_MAX_LIMIT=1000
BSV21_API_DEFAULT_LIMIT=100

# Timeouts and delays
BSV21_QUEUE_SLEEP_DURATION=1s
BSV21_PROCESS_SLEEP_DURATION=5s
```

#### Service Configuration
```bash
# Service identifiers
BSV21_LOOKUP_SERVICE_ID=ls_bsv21
BSV21_TOPICS_SET_NAME=topics

# ARC configuration
BSV21_ARC_API_URL=https://arc.taal.com
BSV21_ARC_WAIT_FOR=ACCEPTED_BY_NETWORK
```

### Configuration File Support

In addition to environment variables, support for a configuration file would be beneficial:

```yaml
# config.yaml
database:
  name: bsv21
  mongodb:
    url: mongodb://localhost:27017
    collections:
      events: events

redis:
  url: redis://localhost:6379
  beef_url: redis://localhost:6379  # optional
  queues:
    main: bsv21_queue
    token_prefix: "tok:"
    topic_prefix: "tm:"

junglebus:
  url: https://junglebus.gorillapool.io
  topic_id: 9cdb5ad57e83efe18804f5910742d804f93ff5bc1a86831a347341b773d629be
  start_block: 883989
  queue_size: 10000000

processing:
  concurrency:
    queue: 16
    process: 16
    download: 10
  batch_sizes:
    queue: 1000
    api_max: 1000
    api_default: 100
  delays:
    queue_empty: 1s
    process_empty: 5s

server:
  port: 3000
  hosting_url: http://localhost:3000
  lookup_service_id: ls_bsv21
  topics_set: topics

arc:
  api_url: https://arc.taal.com
  wait_for: ACCEPTED_BY_NETWORK
  api_key: ${ARC_API_KEY}  # Support env var expansion
  callback_token: ${ARC_CALLBACK_TOKEN}

peers:
  - http://peer1:3000
  - http://peer2:3000
```

### Implementation Strategy

1. **Backward Compatibility**: All new configuration options should have defaults that match current hard-coded values

2. **Configuration Priority**:
   - Command line flags (highest priority)
   - Environment variables
   - Configuration file
   - Hard-coded defaults (lowest priority)

3. **Validation**: Configuration should be validated at startup with clear error messages

4. **Hot Reload**: Some configuration (like concurrency limits) could support hot reload without restart

### Example Implementation

```go
// config/config.go
package config

import (
    "os"
    "time"
    "github.com/spf13/viper"
)

type Config struct {
    Database DatabaseConfig
    Redis    RedisConfig
    JungleBus JungleBusConfig
    Processing ProcessingConfig
    Server   ServerConfig
    ARC      ARCConfig
    Peers    []string
}

type DatabaseConfig struct {
    Name string
    MongoDB MongoDBConfig
}

type ProcessingConfig struct {
    Concurrency ConcurrencyConfig
    BatchSizes  BatchSizeConfig
    Delays      DelayConfig
}

// Load configuration with priority: flags > env > file > defaults
func Load() (*Config, error) {
    viper.SetDefault("database.name", "mnee")
    viper.SetDefault("redis.queues.main", "mnee")
    viper.SetDefault("processing.concurrency.queue", 16)
    // ... more defaults

    // Bind environment variables
    viper.SetEnvPrefix("BSV21")
    viper.AutomaticEnv()

    // Load config file if exists
    viper.SetConfigName("config")
    viper.SetConfigType("yaml")
    viper.AddConfigPath(".")
    viper.AddConfigPath("/etc/bsv21/")
    
    if err := viper.ReadInConfig(); err != nil {
        if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
            return nil, err
        }
    }

    var cfg Config
    if err := viper.Unmarshal(&cfg); err != nil {
        return nil, err
    }

    return &cfg, nil
}
```

## Migration Path

1. **Phase 1**: Add configuration loading without changing behavior
   - Implement configuration system
   - Load configuration but continue using hard-coded values
   - Add logging to show what configuration was loaded

2. **Phase 2**: Use configuration with defaults
   - Replace hard-coded values with configuration lookups
   - Ensure all defaults match current values
   - Test thoroughly with default configuration

3. **Phase 3**: Document and promote configuration
   - Update documentation with all configuration options
   - Provide example configurations for common scenarios
   - Add configuration validation and helpful error messages

## Benefits

1. **Flexibility**: Different deployments can use different database names, queue names, etc.
2. **Multi-tenancy**: Multiple instances can run on the same infrastructure
3. **Performance Tuning**: Adjust concurrency and batch sizes for different hardware
4. **Environment-Specific**: Easy to have different settings for dev/staging/production
5. **Debugging**: Ability to reduce concurrency or batch sizes for debugging