# Redis Storage

**Package**: `trpc.group/trpc-go/trpc-agent-go/memory/redis`

Redis is useful when multiple processes need to share memory without running a SQL database. The backend stores JSON-encoded `memory.Entry` values in Redis hashes and performs keyword search in Go after loading the current user's hash.


## Features

- ✅ Shared memory across processes
- ✅ Redis Cluster-friendly per-user hash tag
- ✅ Key prefix for shared instances/environments
- ❌ No soft delete; search loads a user hash and scores in Go

## Configuration Options

| Option | Effect |
| --- | --- |
| `WithRedisClientURL(url)` | URL-based client; highest priority. |
| `WithRedisInstance(name)` | Uses a registered storage Redis instance when URL is absent. |
| `WithKeyPrefix(prefix)` | Prefixes all Redis keys. |
| `WithExtraOptions(...)` | Passed to the Redis client builder. |
| `WithMemoryLimit`, `WithMinSearchScore`, `WithMaxResults` | Capacity and keyword-search controls. |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | Auto extraction controls. |

## Basic Configuration Example

```go
import memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"

redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithKeyPrefix("prod"),
)
if err != nil {
    // handle error
}
defer redisService.Close()
```

`WithRedisClientURL` has priority over `WithRedisInstance`. `NewService` pings Redis with a 5 second timeout before returning.

## Key Layout

Each user scope is one Redis hash:

```text
mem:{<appName>:<userID>}
```

Fields are `memoryID`; values are JSON `memory.Entry`. The `{app:user}` hash tag keeps one user's hash on a single Redis Cluster slot while distributing different users across slots. `WithKeyPrefix("prod")` turns the key into `prod:mem:{app:user}`.

Older data using the previous `mem:{appName}:userID` layout must be migrated explicitly.

## Operation Semantics

| API | Redis behavior |
| --- | --- |
| `AddMemory` | `HSET key memoryID entryJSON`. |
| `UpdateMemory` | `HGET`, patch entry, and `HSET`; if ID rotates, a transaction pipeline writes the new field and deletes the old one. |
| `DeleteMemory` | `HDEL key memoryID`. |
| `ClearMemories` | `DEL key`. |
| `ReadMemories` | `HGETALL`, JSON decode, normalize old entries, sort by `UpdatedAt`/`CreatedAt` descending. |
| `SearchMemories` | `HGETALL`, decode, then shared keyword scoring. |

Redis memory has no soft delete and no built-in TTL. Use SQL backends if auditability or soft deletion is required.

## Search and Limits

Search is not Redis full text. It scans the current user's hash in application code, scores content/topics with the shared scorer, defaults to min score `0.3` and max results `10`, and supports kind/time filters, event-time sorting, kind fallback, and deduplication.

`WithMemoryLimit(limit)` checks `HLEN` before writing. At capacity it returns an error; it does not evict old entries.

## Tools and Auto Mode

The tool policy is shared with other built-in backends: normal mode exposes enabled tools directly; auto mode (`WithExtractor`) starts a worker and exposes only `memory_search` by default. Enable `memory_load` or expose write tools explicitly when needed.
