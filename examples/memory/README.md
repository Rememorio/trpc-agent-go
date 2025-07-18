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

- Go 1.23 or later
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

## Demo Output

### In-Memory Mode Example

```
=== Memory Service Demo (In-Memory Mode) ===

1. Adding sessions to memory...
   - Added session: user1-session1 (3 events)
   - Added session: user1-session2 (2 events)
   - Added session: user2-session1 (2 events)

2. Searching memories for user1 with query "hello"...
   Search Results:
   - Memory 1: "Hello! How can I help you today?" (Score: 1.0)
   - Memory 2: "Hello there! Nice to meet you." (Score: 1.0)
   - Memory 3: "Good morning! Hello world." (Score: 1.0)
   Total: 3 memories, Search time: 1.2ms

3. Searching memories for user1 with query "python"...
   Search Results:
   - Memory 1: "Python is a great programming language." (Score: 1.0)
   - Memory 2: "I can help you with Python coding." (Score: 1.0)
   Total: 2 memories, Search time: 0.8ms

4. Getting memory statistics for user1...
   Memory Stats:
   - Total Memories: 5
   - Oldest Memory: 2024-01-15 10:00:00 +0000 UTC
   - Newest Memory: 2024-01-15 12:00:00 +0000 UTC

5. Getting memory statistics for user2...
   Memory Stats:
   - Total Memories: 2
   - Oldest Memory: 2024-01-15 11:00:00 +0000 UTC
   - Newest Memory: 2024-01-15 11:30:00 +0000 UTC

6. Deleting user1's memories...
   Successfully deleted all memories for user1

7. Getting memory statistics for user1 after deletion...
   Memory Stats:
   - Total Memories: 0
   - Oldest Memory: 0001-01-01 00:00:00 +0000 UTC
   - Newest Memory: 0001-01-01 00:00:00 +0000 UTC

=== Demo completed successfully! ===
```

### Redis Mode Example

```
=== Memory Service Demo (Redis Mode) ===

1. Adding sessions to memory...
   - Added session: user1-session1 (3 events)
   - Added session: user1-session2 (2 events)
   - Added session: user2-session1 (2 events)

2. Searching memories for user1 with query "hello"...
   Search Results:
   - Memory 1: "Hello! How can I help you today?" (Score: 1.0)
   - Memory 2: "Hello there! Nice to meet you." (Score: 1.0)
   - Memory 3: "Good morning! Hello world." (Score: 1.0)
   Total: 3 memories, Search time: 2.1ms

3. Searching memories for user1 with query "python"...
   Search Results:
   - Memory 1: "Python is a great programming language." (Score: 1.0)
   - Memory 2: "I can help you with Python coding." (Score: 1.0)
   Total: 2 memories, Search time: 1.8ms

4. Getting memory statistics for user1...
   Memory Stats:
   - Total Memories: 5
   - Oldest Memory: 2024-01-15 10:00:00 +0000 UTC
   - Newest Memory: 2024-01-15 12:00:00 +0000 UTC

5. Getting memory statistics for user2...
   Memory Stats:
   - Total Memories: 2
   - Oldest Memory: 2024-01-15 11:00:00 +0000 UTC
   - Newest Memory: 2024-01-15 11:30:00 +0000 UTC

6. Deleting user1's memories...
   Successfully deleted all memories for user1

7. Getting memory statistics for user1 after deletion...
   Memory Stats:
   - Total Memories: 0
   - Oldest Memory: 0001-01-01 00:00:00 +0000 UTC
   - Newest Memory: 0001-01-01 00:00:00 +0000 UTC

=== Demo completed successfully! ===
```

## Core Features

### 1. Session Memory Management

The example creates multiple user sessions, each containing multiple events:

- **user1-session1**: Contains 3 events covering greetings and programming discussions
- **user1-session2**: Contains 2 events about Python programming
- **user2-session1**: Contains 2 events about Go programming

### 2. Intelligent Search

The memory service supports intelligent search based on keyword similarity:

- **Similarity Scoring**: Uses `CalculateScore` function to compute match relevance
- **Multi-keyword Matching**: Supports matching and scoring with multiple keywords
- **Case-insensitive**: Ignores case differences during search
- **Real-time Sorting**: Sorts results by similarity score

### 3. Memory Statistics

The `MemoryStats` structure provides comprehensive memory system analytics:

```go
type MemoryStats struct {
    TotalMemories int       // Total number of memories
    OldestMemory  time.Time // Oldest memory timestamp
    NewestMemory  time.Time // Newest memory timestamp
}
```

### 4. Search Options

Supports various search options and filtering conditions:

- **Pagination**: `Limit`, `Offset`, `NextToken`
- **Filtering**: `SessionID`, `Authors`, `TimeRange`, `MinScore`
- **Sorting**: `SortBy` (timestamp/score), `SortOrder` (asc/desc)

## Performance Comparison

| Feature            | In-Memory Mode | Redis Mode  |
| ------------------ | -------------- | ----------- |
| Startup Speed      | Very Fast      | Medium      |
| Search Performance | Very Fast      | Fast        |
| Persistence        | No             | Yes         |
| Memory Usage       | High           | Low         |
| Scalability        | Single Machine | Distributed |

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

## Dependencies

The memory service example depends on the following packages:

### Core Dependencies

- `memory/inmemory`: In-memory storage implementation
- `memory/redis`: Redis storage implementation
- `memory`: Core interfaces and utility functions

### External Dependencies

- `github.com/redis/go-redis/v9`: Redis client library
- `github.com/stretchr/testify`: Testing framework (for unit tests)

### Module Dependencies

```go
require (
    trpc.group/trpc-go/trpc-agent-go/memory v0.0.0
    trpc.group/trpc-go/trpc-agent-go/memory/inmemory v0.0.0
    trpc.group/trpc-go/trpc-agent-go/memory/redis v0.0.0
    github.com/redis/go-redis/v9 v9.0.0
)
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

## Project Structure

```
examples/memory/
├── main.go              # Main program
├── README.md           # This documentation
└── test_memory_redis.sh # Redis test script
```

## Extending Functionality

You can extend the functionality in several ways:

1. **Add New Storage Backends**: Implement the `memory.Service` interface
2. **Custom Search Algorithms**: Modify the `CalculateScore` function
3. **Additional Filters**: Extend the `SearchOptions` structure
4. **Performance Optimization**: Add caching and indexing mechanisms

## License

This project is licensed under the Apache 2.0 License. See the [LICENSE](../../LICENSE) file for details.
