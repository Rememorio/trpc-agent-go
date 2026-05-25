# SQLiteVec (sqlite-vec) Storage

**Package**: `trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec`

Use SQLiteVec when you want a local single-file store with semantic memory search. It uses sqlite-vec's `vec0` virtual table and therefore requires an embedder.


## Features

- ✅ Single-file persistence plus sqlite-vec semantic search
- ✅ Episodic metadata, soft delete, and schema migration
- ✅ Hybrid search and kind fallback
- ❌ Requires an embedder and CGO
- ❌ Does not evict old memories at capacity

## Configuration Options

| Option | Effect |
| --- | --- |
| `WithEmbedder(embedder)` | Required for add/search. |
| `WithIndexDimension(dim)` | Vector dimension; default `1536` or embedder dimension. |
| `WithMaxResults(limit)` | Default vector top-K; default `10`. |
| `WithTableName(name)` | Validated table name. |
| `WithSoftDelete(enabled)` | Filters active rows by `deleted_at = 0`. |
| `WithMemoryLimit(limit)` | Per-user row cap. |
| `WithSkipDBInit(skip)` | Skips availability/schema checks. |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | Auto extraction controls. |

## Basic Configuration Example

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"
)

db, err := sql.Open("sqlite3", "file:memories_vec.db?_busy_timeout=5000")
if err != nil {
    // handle error
}

emb := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

memoryService, err := memorysqlitevec.NewService(
    db,
    memorysqlitevec.WithEmbedder(emb),
    memorysqlitevec.WithIndexDimension(1536),
    memorysqlitevec.WithSoftDelete(true),
)
if err != nil {
    // handle error
}
defer memoryService.Close()
```

`NewService` registers sqlite-vec once, requires `WithEmbedder`, validates vector dimension, and owns the passed `*sql.DB`.

## Virtual Table Schema

Initialization checks `SELECT vec_version()` and creates a `vec0` table:

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS memories USING vec0(
  memory_id text primary key,
  embedding float[1536] distance_metric=cosine,
  app_name text,
  user_id text,
  created_at integer,
  updated_at integer,
  deleted_at integer,
  +memory_content text,
  +topics text,
  +memory_kind text,
  +event_time integer,
  +participants text,
  +location text
);
```

`topics` and `participants` are JSON strings. Timestamps are UTC Unix nanoseconds. Active rows use `deleted_at = 0`; soft-deleted rows store the deletion timestamp.

The initializer can migrate the legacy schema by copying rows to a `__schema_backup` table, recreating the virtual table, restoring rows, and dropping the backup. If the schema is neither current nor migratable, it returns an explicit outdated-schema error.

## Add and Update

`AddMemory` embeds the memory text, checks dimension, serializes the embedding as `vec_f32`, generates `memory_id`, and inserts/updates inside a transaction. Re-adding a soft-deleted memory sets `deleted_at` back to `0`.

`UpdateMemory` follows the same embedding path and may rotate the ID when text or identity metadata changes. Use `memory.WithUpdateResult(...)` when the caller needs the new ID.

`WithMemoryLimit` enforces a per-user cap inside the transaction and does not evict old rows.

## Search Semantics

`SearchMemories` embeds the query and uses sqlite-vec:

```sql
WHERE embedding MATCH vec_f32(?) AND k = ?
```

The sqlite-vec `distance` is converted to `Score = 1 - distance`. Default top-K is `10`, overridable by `WithMaxResults` or per-call `SearchOptions.MaxResults`.

When post-filters are requested (`Kind`, time range, event-time ordering, kind fallback, deduplication), the service can expand candidate count up to the user's stored memory count to avoid losing valid matches before filtering.

Supported post-processing includes kind fallback, time filters, RRF hybrid search with shared keyword results, similarity threshold filtering when not hybrid, event-time ordering, and deduplication.

## Operational Notes

- Requires CGO and sqlite-vec Go bindings; no external extension download is needed at runtime.
- Empty query returns empty results without calling the embedder.
- `ReadMemories` orders by `updated_at DESC, created_at DESC`.
- `WithSkipDBInit(true)` skips sqlite-vec availability and schema checks; use only with externally guaranteed schema.
