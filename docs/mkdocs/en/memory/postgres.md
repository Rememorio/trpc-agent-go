# PostgreSQL Storage

**Package**: `trpc.group/trpc-go/trpc-agent-go/memory/postgres`

Use this backend for PostgreSQL durability, JSONB storage, schema qualification, and optional soft delete when vector search is not required. For PostgreSQL semantic search, use [`pgvector`](pgvector.md).


## Features

- ✅ PostgreSQL/JSONB persistence
- ✅ Schema support, soft delete, and DDL privilege checks
- ✅ Good fit for existing PostgreSQL deployments
- ❌ No database-side full-text/vector search in this backend
- ❌ Does not evict old memories at capacity

## Configuration Options

| Option | Effect |
| --- | --- |
| `WithPostgresClientDSN(dsn)` | Highest-priority connection string. |
| `WithHost`, `WithPort`, `WithUser`, `WithPassword`, `WithDatabase`, `WithSSLMode` | Direct connection settings. |
| `WithPostgresInstance(name)` | Registered storage instance. |
| `WithSchema(schema)`, `WithTableName(name)` | Validated schema/table names. |
| `WithSoftDelete`, `WithMemoryLimit`, `WithSkipDBInit` | Lifecycle and DDL controls. |
| `WithMinSearchScore`, `WithMaxResults` | Keyword-search controls. |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | Auto extraction controls. |

## Basic Configuration Example

```go
import memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"

postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresClientDSN("postgres://user:password@localhost:5432/dbname?sslmode=disable"),
    memorypostgres.WithSchema("agent"),
    memorypostgres.WithSoftDelete(true),
)
if err != nil {
    // handle error
}
defer postgresService.Close()
```

Connection priority is DSN, then direct host/port/user/password/database options, then registered instance, then defaults (`localhost:5432`, database `trpc-agent-go-pgmemory`, `sslmode=disable`).

## DDL and Schema Verification

`WithSchema` qualifies the table, for example `agent.memories`; the schema must already exist. Schema/table names are validated.

Initialization checks `has_schema_privilege(schema, 'CREATE')`. If the user lacks DDL privilege, DDL is skipped with a warning. If DDL runs, the service creates the table and expected indexes, then verifies required columns and logs index mismatches.

```sql
CREATE TABLE IF NOT EXISTS memories (
    memory_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memory_data JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);
CREATE INDEX IF NOT EXISTS memories_app_user ON memories(app_name, user_id);
CREATE INDEX IF NOT EXISTS memories_updated_at ON memories(updated_at DESC);
CREATE INDEX IF NOT EXISTS memories_deleted_at ON memories(deleted_at);
```

## Write, Read, and Search

`AddMemory` serializes `memory.Entry` into JSONB and upserts by `memory_id` with `ON CONFLICT (memory_id) DO UPDATE`. `UpdateMemory` reads the JSONB entry, applies metadata updates, recalculates the ID, and updates the row. `memory.WithUpdateResult(...)` captures ID rotation.

`WithMemoryLimit(limit)` counts rows in the current user scope before insert. With soft delete enabled, only `deleted_at IS NULL` rows count. This backend does not evict old rows.

`ReadMemories` orders by `updated_at DESC, created_at DESC`. `SearchMemories` decodes visible JSONB rows and uses shared keyword scoring over content/topics; it does not use PostgreSQL full-text search. Defaults are min score `0.3` and max results `10`, with support for kind/time filters, event-time ordering, kind fallback, and deduplication.

## Soft Delete and Tools

`DeleteMemory` / `ClearMemories` hard delete by default. `WithSoftDelete(true)` updates `deleted_at` and all read/search paths filter active rows only.

`WithExtractor(...)` enables auto memory workers. `Tools()` follows the common policy: `memory_search` only by default in auto mode, with `memory_load` and write tools exposed only when configured.
