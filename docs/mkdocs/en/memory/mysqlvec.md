# MySQL Vector (mysqlvec) Storage

**Package**: `trpc.group/trpc-go/trpc-agent-go/memory/mysqlvec`

`mysqlvec` stores durable memories in MySQL and supports semantic search. It uses MySQL 9.0+ native `VECTOR` columns when available, and falls back to `BLOB` embeddings plus Go-side cosine similarity on older MySQL versions.


## Features

- ✅ MySQL persistence plus semantic search
- ✅ Native VECTOR on MySQL 9.0+
- ✅ BLOB + Go-side cosine fallback for MySQL 8.x
- ✅ FULLTEXT + RRF hybrid search
- ❌ Requires an embedder; BLOB fallback is best for moderate data sizes

## Configuration Options

| Option | Effect |
| --- | --- |
| `WithMySQLClientDSN`, `WithMySQLInstance` | Connection source. |
| `WithEmbedder(embedder)` | Required for add/search. |
| `WithIndexDimension(dim)` | Embedding dimension; default `1536`. |
| `WithMaxResults(limit)` | Default top-K; default `15`. |
| `WithSimilarityThreshold(v)` | Cosine score threshold in `[0,1]`; `0` disables filtering. |
| `WithSoftDelete`, `WithMemoryLimit`, `WithSkipDBInit` | Storage lifecycle controls. |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | Auto extraction controls. |

## Basic Configuration Example

```go
import memorymysqlvec "trpc.group/trpc-go/trpc-agent-go/memory/mysqlvec"
import openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

mysqlvecService, err := memorymysqlvec.NewService(
    memorymysqlvec.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
    memorymysqlvec.WithEmbedder(embedder),
    memorymysqlvec.WithIndexDimension(1536),
    memorymysqlvec.WithSoftDelete(true),
)
if err != nil {
    // handle error
}
defer mysqlvecService.Close()
```

`WithEmbedder` is required. `NewService` detects native `VECTOR` support even with `WithSkipDBInit(true)`, so pre-created MySQL 9.0+ tables can use the native path.

## Schema and Version Detection

The startup probe uses `CAST('[1.0]' AS VECTOR)`. If it succeeds, the `embedding` column is `VECTOR(dim)`; otherwise it is `BLOB`.

```sql
CREATE TABLE IF NOT EXISTS memories (
    memory_id VARCHAR(64) PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_content TEXT NOT NULL,
    topics JSON,
    embedding VECTOR(1536) NOT NULL,
    memory_kind VARCHAR(32) NOT NULL DEFAULT 'fact',
    event_time TIMESTAMP(6) NULL,
    participants JSON,
    location VARCHAR(1024) NULL,
    created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at TIMESTAMP(6) NULL DEFAULT NULL,
    FULLTEXT INDEX idx_fulltext (memory_content),
    INDEX idx_app_user (app_name, user_id),
    INDEX idx_updated_at (updated_at DESC),
    INDEX idx_deleted_at (deleted_at),
    INDEX idx_event_time (event_time DESC),
    INDEX idx_kind (app_name, user_id, memory_kind)
);
```

Initialization also tries to add episodic columns to older schemas and ignores MySQL duplicate-column error `1060`.

## Add, Update, and Limits

`AddMemory` embeds the memory text, checks vector dimension, generates the metadata-aware ID, stores topics/participants as JSON, and upserts by `memory_id`. Upsert refreshes content, metadata, embedding, `updated_at`, and clears `deleted_at`.

`UpdateMemory` follows the same embedding path and can rotate IDs. Use `memory.WithUpdateResult(...)` to receive the new ID.

`WithMemoryLimit(limit)` counts active rows before insert. It does not evict old memories at capacity.

## Search Paths

Native `VECTOR` search computes:

```sql
1 - DISTANCE(embedding, STRING_TO_VECTOR(?), 'COSINE') AS similarity
ORDER BY DISTANCE(embedding, STRING_TO_VECTOR(?), 'COSINE')
```

Fallback search loads BLOB embeddings, deserializes them, computes cosine similarity in Go, sorts descending, and returns top-K.

Supported search features include kind filters (`fact` also matches empty legacy kind), time range filters that allow `NULL` event time, kind fallback when fewer than 3 requested-kind results are found, optional hybrid search through MySQL `FULLTEXT` + Reciprocal Rank Fusion, deduplication, and similarity threshold filtering. Default top-K is `15`; default threshold is `0.30`.
