# PostgreSQL 存储

**包路径**：`trpc.group/trpc-go/trpc-agent-go/memory/postgres`

该后端适合需要 PostgreSQL 持久化、JSONB 存储、schema 隔离和软删除，但不需要向量搜索的场景。需要 PostgreSQL 语义搜索时请使用 [`pgvector`](pgvector.md)。


## 特点

- ✅ PostgreSQL/JSONB 持久化
- ✅ 支持 schema、软删除和 DDL 权限检测
- ✅ 适合已有 PostgreSQL 基础设施
- ❌ 不做数据库侧全文/向量检索
- ❌ 容量满时不自动淘汰旧记忆

## 配置选项

| 配置项 | 作用 |
| --- | --- |
| `WithPostgresClientDSN(dsn)` | 最高优先级连接串。 |
| `WithHost`, `WithPort`, `WithUser`, `WithPassword`, `WithDatabase`, `WithSSLMode` | 直接连接参数。 |
| `WithPostgresInstance(name)` | 注册存储实例。 |
| `WithSchema(schema)`, `WithTableName(name)` | 校验后的 schema/table 名。 |
| `WithSoftDelete`, `WithMemoryLimit`, `WithSkipDBInit` | 生命周期和 DDL 控制。 |
| `WithMinSearchScore`, `WithMaxResults` | 关键词搜索控制。 |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | 自动提取控制。 |

## 基础配置示例

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

连接优先级为 DSN、直接 host/port/user/password/database 参数、注册实例、默认连接（`localhost:5432`，数据库 `trpc-agent-go-pgmemory`，`sslmode=disable`）。

## DDL 与 Schema 校验

`WithSchema` 会限定表名，例如 `agent.memories`；schema 必须已经存在。schema/table 名都会校验。

初始化会检查 `has_schema_privilege(schema, 'CREATE')`。没有 DDL 权限时跳过建表并记录 warning；有权限时创建表和索引，然后校验必需列并记录索引异常。

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

## 写入、读取与搜索

`AddMemory` 把 `memory.Entry` 编码为 JSONB，并按 `memory_id` 使用 `ON CONFLICT DO UPDATE`。`UpdateMemory` 读取 JSONB entry，应用元数据更新，重新计算 ID 并更新行；ID 旋转可通过 `memory.WithUpdateResult(...)` 获取。

`WithMemoryLimit(limit)` 插入前统计当前用户行数。开启软删除时只统计 `deleted_at IS NULL`；该后端不会自动淘汰旧行。

`ReadMemories` 按 `updated_at DESC, created_at DESC` 排序。`SearchMemories` 解码可见 JSONB 行后对内容和 topics 做共享关键词打分，不使用 PostgreSQL full-text。默认最低分数 `0.3`，默认最多 `10` 条，支持 kind/time 过滤、事件时间排序、kind fallback 和去重。

## 软删除与工具

`DeleteMemory` / `ClearMemories` 默认硬删除；`WithSoftDelete(true)` 后设置 `deleted_at`，读取和搜索只看活跃行。

`WithExtractor(...)` 启用自动记忆 worker。自动模式下 `Tools()` 默认只暴露 `memory_search`，其他工具按配置暴露。
