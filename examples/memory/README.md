# Memory Service Example

This example demonstrates how to use the tRPC Agent Go Memory Service with both in-memory and Redis storage backends.

## What is Memory Service?

This implementation showcases the essential features for building persistent memory systems in conversational AI applications:

- **💾 Dual Storage**: Support for both in-memory and Redis storage backends
- **🔍 Smart Search**: Semantic search with similarity scoring and filtering
- **📊 Memory Statistics**: Comprehensive analytics and insights
- **🗂️ Session Management**: Automatic session and event organization
- **⚡ High Performance**: Optimized search and storage operations

### Key Features

- **Flexible Storage**: Choose between fast in-memory storage or persistent Redis storage
- **Intelligent Search**: Find relevant memories using natural language queries
- **Rich Filtering**: Filter by session, author, time range, and similarity score
- **Memory Analytics**: Track memory usage patterns and statistics
- **Session Continuity**: Maintain conversation context across sessions
- **Error Handling**: Graceful error recovery and reporting

## Prerequisites

- Go 1.24 or later
- Redis server (optional, for Redis backend)

## Environment Variables

| Variable         | Description               | Default Value    |
| ---------------- | ------------------------- | ---------------- |
| `REDIS_ADDR`     | Redis server address      | `localhost:6379` |
| `REDIS_PASSWORD` | Redis password (optional) | ``               |
| `REDIS_DB`       | Redis database number     | `0`              |

## Command Line Arguments

| Argument          | Description               | Default Value    |
| ----------------- | ------------------------- | ---------------- |
| `-redis-addr`     | Redis server address      | `localhost:6379` |
| `-redis-password` | Redis password (optional) | ``               |
| `-redis-db`       | Redis database number     | `0`              |

## Usage

### Build the Example

```bash
cd examples
go mod tidy
go build -o memory-demo memory/main.go
```

### In-Memory Mode

```bash
./memory-demo
```

### Redis Mode

First, ensure Redis is running:

```bash
# Check Redis connection
redis-cli ping

# Start Redis if not running
redis-server
```

Then run the example:

```bash
./memory-demo -redis-addr=localhost:6379
```

### With Redis Authentication

```bash
./memory-demo -redis-addr=localhost:6379 -redis-password=your_password
```

### Using Environment Variables

If you have Redis configuration set in your environment:

```bash
export REDIS_ADDR="localhost:6379"
export REDIS_PASSWORD="your_password"
./memory-demo
```

### Using Environment Variable with Custom Redis

```bash
source ~/.bashrc && ./memory-demo -redis-addr="$REDIS_ADDR"
```

## Redis Setup

### Install Redis

```bash
# Ubuntu/Debian
sudo apt-get install redis-server

# macOS
brew install redis

# Docker
docker run -d --name redis-memory -p 6379:6379 redis:7-alpine
```

### Start Redis

```bash
# System service
sudo systemctl start redis-server

# Manual start
redis-server

# Docker
docker start redis-memory
```

### Test Connection

```bash
redis-cli ping
# Should return: PONG
```

## Troubleshooting

### Redis Connection Issues

If you encounter Redis connection problems:

1. **Check Redis Service Status**:

   ```bash
   redis-cli ping
   ```

2. **Verify Connection Parameters**:

   ```bash
   ./memory-demo -redis-addr=localhost:6379 -redis-password=your_password
   ```

3. **Test Network Connectivity**:
   ```bash
   telnet localhost 6379
   ```

### Common Errors

- `connection refused`: Redis service not running
- `authentication failed`: Incorrect Redis password
- `timeout`: Network connectivity issues
- `module not found`: Missing dependencies, run `go mod tidy`

### Build Issues

If you encounter build issues:

```bash
# Clean and rebuild
go clean -modcache
go mod tidy
go build -o memory-demo memory/main.go
```

## Extending Functionality

You can extend the functionality in several ways:

1. **Add New Storage Backends**: Implement the `memory.Service` interface
2. **Custom Search Algorithms**: Modify the `CalculateScore` function
3. **Additional Filters**: Extend the `SearchOptions` structure
4. **Performance Optimization**: Add caching and indexing mechanisms

## License

This project is licensed under the Apache 2.0 License. See the [LICENSE](../../LICENSE) file for details.
