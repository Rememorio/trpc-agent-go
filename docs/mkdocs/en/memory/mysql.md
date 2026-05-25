# MySQL Storage

**Package**: `trpc.group/trpc-go/trpc-agent-go/memory/mysql`

Use this backend when you want a conventional relational store with JSON payloads and optional soft delete. Search is application-side keyword scoring after loading rows for the current user; it does not use MySQL full-text indexes. For MySQL semantic search, use [`mysqlvec`](mysqlvec.md).


## Features

- ✅ MySQL persistence with JSON payloads
- ✅ Soft delete, registered instances, and external migrations
- ✅ Good fit for existing MySQL deployments
- ❌ Search is Go-side keyword scoring, not FULLTEXT
- ❌ Does not evict old memories at capacity

## Configuration Options

| Option | Effect |
| --- | --- |
| `WithMySQLClientDSN(dsn)` | Preferred direct connection string. |
| `WithMySQLInstance(name)` | Uses registered MySQL instance when DSN is absent. |
| `WithTableName(name)` | Validated table name; default `memories`. |
| `WithSoftDelete(enabled)` | Uses `deleted_at` filtering. |
| `WithMemoryLimit(limit)` | Per-user row cap. |
| `WithSkipDBInit(skip)` | Skips table creation. |
| `WithExtraOptions(...)` | Passed to the MySQL client builder. |
| `WithMinSearchScore`, `WithMaxResults` | Keyword-search controls. |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | Auto extraction controls. |

## Basic Configuration Example

```go
import memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"

dsn := "user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN(dsn),
    memorymysql.WithSoftDelete(true),
)
if err != nil {
    // handle error
}
defer mysqlService.Close()
```

`WithMySQLClientDSN` has priority over `WithMySQLInstance`. Use `parseTime=true` because the client scans timestamp columns as `time.Time`.

## Schema

With database initialization enabled, the service creates:

```sql
CREATE TABLE IF NOT EXISTS memories (
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSON NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (app_name, user_id, memory_id),
    INDEX idx_app_user (app_name, user_id),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

`memory_data` is a serialized `memory.Entry`, so topics and episodic metadata are stored in JSON. `WithTableName` validates that the name starts with a letter or underscore, contains only alphanumeric characters/underscores, and is at most 64 characters.

## Write and Delete Semantics

`AddMemory` checks `WithMemoryLimit` first when configured, then inserts with `ON DUPLICATE KEY UPDATE`. The primary key is `(app_name, user_id, memory_id)`, so re-adding the same memory identity refreshes `memory_data` and `updated_at`.

`UpdateMemory` reads the existing JSON entry, applies patch metadata, recalculates the ID, and updates the row. If memory text or identity metadata changes, the ID can rotate; `memory.WithUpdateResult(...)` captures the new ID.

`DeleteMemory` and `ClearMemories` hard delete by default. `WithSoftDelete(true)` changes them to set `deleted_at`, and all reads/searches add `deleted_at IS NULL`.

The backend does not evict old rows when the memory limit is reached; add returns an error.

## Read and Search

`ReadMemories` filters by `app_name` and `user_id`, orders by `updated_at DESC, created_at DESC`, and applies `LIMIT` when requested.

`SearchMemories` selects visible rows for the user, decodes `memory_data`, and uses the shared keyword scorer over content and topics. Defaults are min score `0.3` and max results `10`. Kind/time filters, event-time ordering, kind fallback, and deduplication are supported after decoding.

## Tools and Auto Mode

The backend supports common memory tools and `WithExtractor(...)`. In auto mode, a background worker uses service write APIs, while `Tools()` exposes only the configured agent-facing subset (`memory_search` by default).
