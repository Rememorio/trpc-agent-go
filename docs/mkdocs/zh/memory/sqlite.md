# SQLite 存储

**包路径**：`trpc.group/trpc-go/trpc-agent-go/memory/sqlite`

SQLite 后端适合需要本地持久化，但不想运维 Redis/MySQL/PostgreSQL 的场景。它把序列化后的 `memory.Entry` 存入单表，并在选择当前用户行后于 Go 进程内做关键词搜索。


## 特点

- ✅ 单文件持久化，部署成本低
- ✅ 支持软删除和表名校验
- ✅ 适合 CLI、Demo、单机小服务
- ❌ 搜索不是 SQLite FTS，而是 Go 侧关键词打分
- ❌ 依赖 go-sqlite3，需要 CGO

## 配置选项

| 配置项 | 作用 |
| --- | --- |
| `WithTableName(name)` | 校验表名，默认 `memories`。 |
| `WithSoftDelete(enabled)` | 使用 `deleted_at` 软删除。 |
| `WithMemoryLimit(limit)` | 每用户行数上限。 |
| `WithSkipDBInit(skip)` | 跳过建表/建索引。 |
| `WithMinSearchScore`, `WithMaxResults` | 关键词搜索控制。 |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | 自动提取控制。 |

## 基础配置示例

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

`NewService` 接管传入的 `*sql.DB`，`Close()` 会关闭它。`github.com/mattn/go-sqlite3` 需要 CGO。

## 表结构

未设置 `WithSkipDBInit(true)` 时，会在 30 秒超时内创建：

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

`created_at`、`updated_at`、`deleted_at` 存 UTC Unix 纳秒。`memory_data` 是 `memory.Entry` 的 JSON，因此 topics 和事件元数据都在 payload 中。`WithTableName` 会通过 `sqldb.ValidateTableName` 校验。

## 写入、读取与搜索

`AddMemory` 应用可选 `memory.WithMetadata`，生成 ID，编码 entry，并按 `memory_id` upsert。upsert 会刷新 `memory_data`、`updated_at` 并清空 `deleted_at`，因此软删除的记忆可被复活。

`UpdateMemory` 加载旧 entry，应用补丁式元数据，重新计算 ID 后更新行。文本或身份元数据变化时 ID 可能旋转，可通过 `memory.WithUpdateResult(...)` 获取。

`ReadMemories` 按 `<appName, userID>` 查询，开启软删除时追加 `deleted_at IS NULL`，排序为 `updated_at DESC, created_at DESC`。

`SearchMemories` 不是 SQLite FTS。它选择可见用户行、解码 entry 后用共享关键词 scorer 搜索内容和 topics；默认最低分数 `0.3`，默认最多 `10` 条，支持 kind/time 过滤、事件时间排序、kind fallback 和去重。

## 软删除与容量

`DeleteMemory` / `ClearMemories` 默认硬删除。`WithSoftDelete(true)` 后改为设置 `deleted_at`，读取和搜索会排除已删除行。

`WithMemoryLimit(limit)` 插入新记忆前统计当前用户行数；开启软删除时已删除行不计数。容量满时返回错误，不自动淘汰。
