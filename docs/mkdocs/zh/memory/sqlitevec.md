# SQLiteVec（sqlite-vec）存储

**包路径**：`trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec`

`sqlitevec` 适合本地单文件持久化，同时需要语义记忆搜索的场景。它使用 sqlite-vec 的 `vec0` 虚拟表，因此必须配置 embedder。


## 特点

- ✅ 单文件持久化 + sqlite-vec 语义检索
- ✅ 支持事件元数据、软删除和 schema 迁移
- ✅ 支持 hybrid search 与 kind fallback
- ❌ 需要 embedder 和 CGO
- ❌ 容量满时不自动淘汰旧记忆

## 配置选项

| 配置项 | 作用 |
| --- | --- |
| `WithEmbedder(embedder)` | 必填，用于 add/search。 |
| `WithIndexDimension(dim)` | 向量维度，默认 `1536` 或 embedder 维度。 |
| `WithMaxResults(limit)` | 默认 top-K，默认 `10`。 |
| `WithTableName(name)` | 校验表名。 |
| `WithSoftDelete(enabled)` | 过滤 `deleted_at = 0`。 |
| `WithMemoryLimit(limit)` | 每用户行数上限。 |
| `WithSkipDBInit(skip)` | 跳过可用性和 schema 检查。 |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | 自动提取控制。 |

## 基础配置示例

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

`NewService` 会注册 sqlite-vec，要求 `WithEmbedder` 非空，校验向量维度，并接管 `*sql.DB`。

## 虚拟表结构

初始化先检查 `SELECT vec_version()`，然后创建 `vec0` 表：

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

`topics` 和 `participants` 存 JSON 字符串；时间字段存 UTC Unix 纳秒。活跃行使用 `deleted_at = 0`，软删除行保存删除时间。

初始化器能迁移旧 schema：复制到 `__schema_backup`，重建虚拟表，再恢复数据。既不是当前结构也不是可迁移旧结构时，会返回明确的 schema 过期错误。

## 写入与更新

`AddMemory` 对文本生成 embedding、校验维度、用 `vec_f32` 写入、生成 metadata-aware ID，并在事务中插入/更新。重新添加软删除记忆时会把 `deleted_at` 设回 `0`。

`UpdateMemory` 同样走 embedding 路径；文本或身份元数据变化时 ID 可能旋转，可用 `memory.WithUpdateResult(...)` 获取。

`WithMemoryLimit` 在事务内检查每用户容量，满时返回错误，不自动淘汰。

## 搜索语义

`SearchMemories` 先 embed query，然后执行：

```sql
WHERE embedding MATCH vec_f32(?) AND k = ?
```

sqlite-vec 返回的 `distance` 会转成 `Score = 1 - distance`。默认 top-K 为 `10`。

当启用 kind、时间范围、事件时间排序、kind fallback 或去重时，候选数可扩展到当前用户记忆总数，避免 top-K 提前截断。取回候选后支持 kind fallback、time filter、RRF hybrid search、非 hybrid 相似度阈值、事件时间排序和去重。

空 query 直接返回空，不调用 embedder。

## 运维注意事项

- 需要 CGO 和 sqlite-vec Go bindings；运行时不需要下载外部扩展文件。
- `ReadMemories` 按 `updated_at DESC, created_at DESC` 排序。
- `WithSkipDBInit(true)` 会跳过 sqlite-vec 可用性和 schema 检查，只适合外部迁移保证结构正确的场景。
