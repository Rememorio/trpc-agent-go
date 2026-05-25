# In-Memory Storage

**Package**: `trpc.group/trpc-go/trpc-agent-go/memory/inmemory`

Use this backend for tests, local experiments, and demos where data loss on process restart is acceptable. It has no external dependency and keeps all entries in the current process.


## Features

- ✅ No external dependency; fastest in-process reads/writes
- ✅ Concurrency-safe, good for tests and demos
- ❌ No persistence after process restart
- ❌ No multi-instance sharing or soft delete

## Configuration Options

| Option | Effect |
| --- | --- |
| `WithMemoryLimit(limit)` | Per-user map cap. |
| `WithMinSearchScore(score)` | Keyword threshold; negative values are ignored. |
| `WithMaxResults(max)` | Default search cap; `0` disables truncation. |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | Auto memory worker settings. |
| `WithToolEnabled`, `WithCustomTool`, `WithToolExposed` | Tool registration and exposure controls. |

## Basic Configuration Example

```go
import memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"

memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithMemoryLimit(200),
)
defer memoryService.Close()
```

## Implementation Details

The service stores entries in nested maps guarded by mutexes:

```text
appName -> userID -> memoryID -> *memory.Entry
```

`memoryID` is generated from memory text, `<appName, userID>`, and identity metadata (`kind`, `event_time`, `participants`, `location`). `Topics` are not part of the ID, so tag-only changes do not create a new identity.

`ReadMemories` returns the current user's entries ordered by `UpdatedAt` descending, then `CreatedAt` descending. `SearchMemories` scans the in-process entries and uses the shared keyword scorer: content and topics are tokenized, scores below `0.3` are filtered by default, and the default cap is `10` results. It also supports kind/time filters, event-time ordering, kind fallback, and deduplication through `memory.SearchOptions`.

## Limits and Lifecycle

- Default per-user memory limit: `1000`.
- `WithMemoryLimit` changes the map-size cap checked before add.
- `DeleteMemory` removes one map entry; `ClearMemories` deletes the whole user map.
- There is no soft delete, TTL, or cross-process sharing.
- `Close()` only stops auto-memory workers; there is no storage handle to close.

## Tools and Auto Mode

Without an extractor, `Tools()` exposes enabled tools directly. Default enabled tools are `memory_add`, `memory_update`, `memory_search`, and `memory_load`; `memory_delete` and `memory_clear` are valid but disabled.

With `WithExtractor(...)`, an `AutoMemoryWorker` is started. Background extraction may use add/update/delete, but `Tools()` exposes only `memory_search` by default. `memory_load` and write tools must be explicitly enabled/exposed with `WithToolEnabled`, `WithAutoMemoryExposedTools`, or `WithToolExposed`.
