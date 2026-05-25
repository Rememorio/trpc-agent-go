# SQLite Storage

**Package**: `trpc.group/trpc-go/trpc-agent-go/memory/sqlite`

Use SQLite for local persistence without operating Redis/MySQL/PostgreSQL. It stores serialized `memory.Entry` payloads in one table and performs keyword search in Go after selecting rows for the current user.


## Features

- ✅ Single-file persistence with low deployment cost
- ✅ Soft delete and table-name validation
- ✅ Good for CLI, demos, and small single-node services
- ❌ Search is Go-side keyword scoring, not SQLite FTS
- ❌ Requires CGO via go-sqlite3

## Configuration Options

| Option | Effect |
| --- | --- |
| `WithTableName(name)` | Validated table name; default `memories`. |
| `WithSoftDelete(enabled)` | Uses `deleted_at` instead of hard delete. |
| `WithMemoryLimit(limit)` | Per-user row cap. |
| `WithSkipDBInit(skip)` | Skips table/index creation. |
| `WithMinSearchScore`, `WithMaxResults` | Keyword-search controls. |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | Auto extraction controls. |
| `WithToolEnabled`, `WithCustomTool`, `WithToolExposed` | Tool controls. |

## Basic Configuration Example

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
)

db, err := sql.Open("sqlite3", "file:memories.db?_busy_timeout=5000")
if err != nil {
    // handle error
}

memoryService, err := memorysqlite.NewService(
    db,
    memorysqlite.WithSoftDelete(true),
    memorysqlite.WithMemoryLimit(200),
)
if err != nil {
    // handle error
}
defer memoryService.Close()
```

`NewService` owns the passed `*sql.DB` and closes it from `Close()`. `github.com/mattn/go-sqlite3` requires CGO.

## Table Schema

Unless `WithSkipDBInit(true)` is set, initialization runs with a 30 second timeout and creates:

```sql
CREATE TABLE IF NOT EXISTS memories (
  memory_id TEXT PRIMARY KEY,
  app_name TEXT NOT NULL,
  user_id TEXT NOT NULL,
  memory_data BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  deleted_at INTEGER DEFAULT NULL
);
CREATE INDEX IF NOT EXISTS memories_app_user ON memories(app_name, user_id);
CREATE INDEX IF NOT EXISTS memories_updated_at ON memories(updated_at DESC);
CREATE INDEX IF NOT EXISTS memories_deleted_at ON memories(deleted_at)
WHERE deleted_at IS NOT NULL;
```

`created_at`, `updated_at`, and `deleted_at` are UTC Unix nanoseconds. `memory_data` is JSON for `memory.Entry`, so topics and episodic metadata live inside the payload. `WithTableName` is validated through `sqldb.ValidateTableName`.

## Write, Read, and Search

`AddMemory` applies optional `memory.WithMetadata`, generates the ID, marshals the entry, and upserts by `memory_id`. Upsert refreshes `memory_data`, `updated_at`, and clears `deleted_at`, so a soft-deleted memory can be resurrected if capacity permits.

`UpdateMemory` loads the current entry, applies patch-style update metadata, recalculates the ID, and updates the row. If text or identity metadata changes, the ID may rotate; use `memory.WithUpdateResult(...)` to capture it.

`ReadMemories` filters by `<appName, userID>`, optionally adds `deleted_at IS NULL`, orders by `updated_at DESC, created_at DESC`, and applies SQL `LIMIT`.

`SearchMemories` is not SQLite FTS. It selects visible user rows, decodes entries, and uses the shared keyword scorer over content/topics. Defaults are min score `0.3` and max `10`; kind/time filters, event-time ordering, kind fallback, and deduplication are supported after decoding.

## Soft Delete and Limits

`DeleteMemory` / `ClearMemories` hard delete by default. With `WithSoftDelete(true)`, they set `deleted_at` and reads/searches exclude deleted rows.

`WithMemoryLimit(limit)` counts rows before inserting a new memory. With soft delete enabled, deleted rows are excluded from the count. The backend does not evict old rows at capacity.
