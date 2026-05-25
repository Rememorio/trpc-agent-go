# pgvector 存储

**包路径**：`trpc.group/trpc-go/trpc-agent-go/memory/pgvector`

`pgvector` 适合生产环境的 PostgreSQL 语义搜索。不同于 JSONB PostgreSQL 后端，它把记忆文本、topics、事件元数据、embedding 和搜索索引都存为 SQL 列。


## 特点

- ✅ PostgreSQL + pgvector 语义检索
- ✅ HNSW 向量索引和 search_vector hybrid search
- ✅ 支持事件元数据、kind/time 过滤和 RRF 融合
- ✅ 容量满时可淘汰最旧记忆
- ❌ 需要 pgvector extension 和 embedder

## 配置选项

| 配置项 | 作用 |
| --- | --- |
| `WithPGVectorClientDSN`, `WithHost`, `WithPostgresInstance` | 连接来源。 |
| `WithEmbedder(embedder)` | 必填，用于 add/search。 |
| `WithIndexDimension(dim)` | 向量维度，默认 `1536`。 |
| `WithMaxResults(limit)` | 默认 top-K，默认 `15`。 |
| `WithSimilarityThreshold(v)` | `[0,1]` 相似度阈值；`0` 不过滤。 |
| `WithHNSWIndexParams(params)` | 覆盖 HNSW 参数。 |
| `WithSchema`, `WithTableName`, `WithSkipDBInit` | DDL/schema 控制。 |
| `WithSoftDelete`, `WithMemoryLimit` | 删除和容量行为。 |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | 自动提取控制。 |

## 基础配置示例

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

`WithEmbedder` 必填。连接优先级为 DSN、直接 host 参数、注册实例、默认值。

## Schema 与索引

初始化会尽量创建 `vector` extension，检查 schema DDL 权限，然后创建表、索引、`search_vector` 列和 trigger。没有 CREATE 权限时跳过 DDL。

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

索引包括 `(app_name, user_id)`、`updated_at DESC`、`deleted_at`、`event_time DESC WHERE event_time IS NOT NULL`、`(app_name, user_id, memory_kind)`、`participants` GIN、`search_vector` GIN，以及 embedding 上的 HNSW `vector_cosine_ops`。默认 HNSW 参数为 `m = 16`、`ef_construction = 64`。

## 写入与容量

`AddMemory` 会 embed 文本、校验维度、转换成 `pgvector.Vector`、解析元数据并 upsert。重新添加同 ID 会刷新内容、topics、embedding、元数据、`updated_at`，并清空 `deleted_at`。

设置 `WithMemoryLimit(limit)` 后，pgvector 在新记忆插入且用户已达上限时，会用 CTE 淘汰该用户 `updated_at` 最旧的记忆。开启软删除时表现为设置 `deleted_at`；否则直接删除。这点和普通 SQL 后端“满了报错”不同。

## 搜索语义

向量搜索按 cosine distance 排序：

```sql
ORDER BY embedding <=> $1
```

默认 top-K 为 `15`，默认相似度阈值 `0.30`；非 hybrid 模式下可用 per-call `SearchOptions.SimilarityThreshold` 覆盖。

支持：

- `Kind`：过滤 `memory_kind`，`fact` 也匹配空 kind 旧数据；
- `TimeAfter` / `TimeBefore`：过滤 `event_time`，同时允许 `NULL`；
- `OrderByEventTime`：在向量距离后追加 `event_time ASC NULLS LAST`；
- `KindFallback`：指定 kind 少于 3 条时合并无 kind 搜索；
- `HybridSearch`：用 `search_vector @@ plainto_tsquery('english', $1)` 做关键词搜索，并通过 RRF 合并；
- `Deduplicate`：合并/排序后去重。

Hybrid search 使用 RRF 分数，不再套用 cosine threshold。
