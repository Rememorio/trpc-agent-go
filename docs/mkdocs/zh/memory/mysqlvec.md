# MySQL Vector（mysqlvec）存储

**包路径**：`trpc.group/trpc-go/trpc-agent-go/memory/mysqlvec`

`mysqlvec` 在 MySQL 中持久化记忆，并提供语义搜索。MySQL 9.0+ 可使用原生 `VECTOR` 列；较老版本会退化为 `BLOB` embedding，并在 Go 中计算余弦相似度。


## 特点

- ✅ MySQL 持久化 + 语义搜索
- ✅ MySQL 9.0+ 使用原生 VECTOR
- ✅ MySQL 8.x 可回退到 BLOB + Go 侧 cosine
- ✅ 支持 FULLTEXT + RRF hybrid search
- ❌ 需要 embedder，BLOB fallback 适合中等规模数据

## 配置选项

| 配置项 | 作用 |
| --- | --- |
| `WithMySQLClientDSN`, `WithMySQLInstance` | 连接来源。 |
| `WithEmbedder(embedder)` | 必填，用于 add/search。 |
| `WithIndexDimension(dim)` | embedding 维度，默认 `1536`。 |
| `WithMaxResults(limit)` | 默认 top-K，默认 `15`。 |
| `WithSimilarityThreshold(v)` | `[0,1]` 余弦分数阈值；`0` 不过滤。 |
| `WithSoftDelete`, `WithMemoryLimit`, `WithSkipDBInit` | 存储生命周期控制。 |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | 自动提取控制。 |

## 基础配置示例

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

`WithEmbedder` 必填。即使设置 `WithSkipDBInit(true)`，`NewService` 也会检测原生 `VECTOR` 支持。

## Schema 与版本检测

启动探测使用 `CAST('[1.0]' AS VECTOR)`。成功时 `embedding` 为 `VECTOR(dim)`，否则为 `BLOB`。

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

初始化还会尝试给旧表补 `memory_kind`、`event_time`、`participants`、`location`，并忽略 MySQL 重复列错误 `1060`。

## 写入、更新与容量

`AddMemory` 会 embed 记忆文本、校验维度、生成 metadata-aware ID，把 topics/participants 存成 JSON，并按 `memory_id` upsert。upsert 会刷新内容、元数据、embedding、`updated_at`，并清空 `deleted_at`。

`UpdateMemory` 同样可能因为文本或身份元数据变化导致 ID 旋转；可通过 `memory.WithUpdateResult(...)` 获取新 ID。

`WithMemoryLimit(limit)` 在插入前统计活跃行数，满时返回错误，不自动淘汰。

## 搜索路径

原生 `VECTOR` 路径使用：

```sql
1 - DISTANCE(embedding, STRING_TO_VECTOR(?), 'COSINE') AS similarity
ORDER BY DISTANCE(embedding, STRING_TO_VECTOR(?), 'COSINE')
```

fallback 路径会加载 BLOB embedding，在 Go 中反序列化并计算 cosine similarity，按相似度倒序返回 top-K。

支持 kind 过滤（`fact` 也匹配空 kind 旧数据）、时间范围过滤（允许 `event_time IS NULL`）、kind fallback（少于 3 条时合并无 kind 搜索）、MySQL `FULLTEXT` + RRF 的 hybrid search、去重和相似度阈值。默认 top-K `15`，默认阈值 `0.30`。
