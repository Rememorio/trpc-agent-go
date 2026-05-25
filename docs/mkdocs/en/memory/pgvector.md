# pgvector Storage

**Package**: `trpc.group/trpc-go/trpc-agent-go/memory/pgvector`

Use `pgvector` for production semantic search on PostgreSQL. Unlike the JSONB PostgreSQL backend, this backend stores memory text, topics, episodic metadata, embeddings, and search indexes as SQL columns.


## Features

- ✅ PostgreSQL + pgvector semantic search
- ✅ HNSW vector index and search_vector hybrid search
- ✅ Episodic metadata, kind/time filters, and RRF merging
- ✅ Can evict least-recently-updated memory at capacity
- ❌ Requires pgvector extension and an embedder

## Configuration Options

| Option | Effect |
| --- | --- |
| `WithPGVectorClientDSN`, `WithHost`, `WithPostgresInstance` | Connection source. |
| `WithEmbedder(embedder)` | Required for add/search. |
| `WithIndexDimension(dim)` | Vector dimension; default `1536`. |
| `WithMaxResults(limit)` | Default top-K; default `15`. |
| `WithSimilarityThreshold(v)` | Cosine threshold in `[0,1]`; `0` disables filtering. |
| `WithHNSWIndexParams(params)` | Overrides HNSW `M` and `EfConstruction`. |
| `WithSchema`, `WithTableName`, `WithSkipDBInit` | DDL/schema controls. |
| `WithSoftDelete`, `WithMemoryLimit` | Delete and capacity behavior. |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | Auto extraction controls. |

## Basic Configuration Example

```go
import memorypgvector "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
import openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

pgvectorService, err := memorypgvector.NewService(
    memorypgvector.WithPGVectorClientDSN("postgres://user:password@localhost:5432/dbname?sslmode=disable"),
    memorypgvector.WithEmbedder(embedder),
    memorypgvector.WithSoftDelete(true),
)
if err != nil {
    // handle error
}
defer pgvectorService.Close()
```

`WithEmbedder` is required. Connection priority is DSN, direct host options, registered instance, then defaults.

## Schema and Indexes

Initialization creates the `vector` extension when possible, checks schema DDL privilege, then creates table, indexes, a `search_vector` column, and trigger. If CREATE privilege is missing, DDL is skipped.

```sql
CREATE TABLE IF NOT EXISTS memories (
    memory_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memory_content TEXT NOT NULL,
    topics TEXT[],
    embedding vector(1536),
    memory_kind TEXT NOT NULL DEFAULT 'fact',
    event_time TIMESTAMP NULL,
    participants TEXT[],
    location TEXT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);
```

Indexes include `(app_name, user_id)`, `updated_at DESC`, `deleted_at`, `event_time DESC WHERE event_time IS NOT NULL`, `(app_name, user_id, memory_kind)`, GIN on `participants`, GIN on `search_vector`, and HNSW on `embedding vector_cosine_ops`. Default HNSW params are `m = 16` and `ef_construction = 64`.

## Write Path and Capacity

`AddMemory` embeds memory text, checks dimension, converts it to `pgvector.Vector`, resolves metadata, and upserts the row. Re-adding an existing ID refreshes content, topics, embedding, metadata, `updated_at`, and clears `deleted_at`.

When `WithMemoryLimit(limit)` is set, pgvector uses a CTE to evict the least recently updated memory for that user when inserting a new memory at capacity. With soft delete enabled, eviction sets `deleted_at`; otherwise it deletes the row. This differs from the plain SQL backends, which return an error at capacity.

## Search Semantics

Vector search orders by cosine distance:

```sql
ORDER BY embedding <=> $1
```

Default top-K is `15`, default similarity threshold is `0.30`, and per-call `SearchOptions.SimilarityThreshold` can override it when not using hybrid search.

Supported options:

- `Kind`: filters `memory_kind`; `fact` also matches empty legacy kind.
- `TimeAfter` / `TimeBefore`: filter `event_time` while allowing `NULL` event time.
- `OrderByEventTime`: appends `event_time ASC NULLS LAST` after vector distance.
- `KindFallback`: merges unfiltered search if a kind-filtered search returns fewer than 3 results.
- `HybridSearch`: runs `search_vector @@ plainto_tsquery('english', $1)` and merges keyword/vector results via Reciprocal Rank Fusion.
- `Deduplicate`: removes duplicates after merge/sort.

Hybrid search skips cosine-threshold filtering because RRF scores use a different scale.
