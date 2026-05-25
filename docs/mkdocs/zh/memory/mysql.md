# MySQL 存储

**包路径**：`trpc.group/trpc-go/trpc-agent-go/memory/mysql`

MySQL 后端适合需要关系型持久化、JSON payload 和软删除的场景。它的搜索是在 Go 进程内加载当前用户行后做关键词打分，不使用 MySQL FULLTEXT。需要 MySQL 语义检索时请使用 [`mysqlvec`](mysqlvec.md)。


## 特点

- ✅ MySQL 持久化和 JSON payload
- ✅ 支持软删除、注册实例和外部迁移管理
- ✅ 适合已有 MySQL 基础设施的服务
- ❌ 搜索不是 FULLTEXT，而是 Go 侧关键词打分
- ❌ 容量满时不自动淘汰旧记忆

## 配置选项

| 配置项 | 作用 |
| --- | --- |
| `WithMySQLClientDSN(dsn)` | 推荐的直接连接串。 |
| `WithMySQLInstance(name)` | DSN 为空时使用注册实例。 |
| `WithTableName(name)` | 校验表名，默认 `memories`。 |
| `WithSoftDelete(enabled)` | 使用 `deleted_at` 过滤。 |
| `WithMemoryLimit(limit)` | 每用户行数上限。 |
| `WithSkipDBInit(skip)` | 跳过建表。 |
| `WithExtraOptions(...)` | 透传给 MySQL client builder。 |
| `WithMinSearchScore`, `WithMaxResults` | 关键词搜索控制。 |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | 自动提取控制。 |

## 基础配置示例

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

`WithMySQLClientDSN` 优先级高于 `WithMySQLInstance`。DSN 建议包含 `parseTime=true`。

## 表结构

开启 DB 初始化时会创建：

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

`memory_data` 是序列化后的 `memory.Entry`，topics 和事件元数据都在 JSON 内。`WithTableName` 校验表名：必须以字母或下划线开头，只能包含字母、数字、下划线，最长 64。

## 写入与删除语义

`AddMemory` 在配置容量上限时先统计当前用户行数，然后使用 `ON DUPLICATE KEY UPDATE`。主键为 `(app_name, user_id, memory_id)`，同一记忆身份会刷新 `memory_data` 和 `updated_at`。

`UpdateMemory` 读取旧 JSON entry，应用元数据更新，重新计算 ID 并更新行。文本或身份元数据变化时 ID 可能旋转，可用 `memory.WithUpdateResult(...)` 获取。

`DeleteMemory` / `ClearMemories` 默认硬删除。`WithSoftDelete(true)` 后改为设置 `deleted_at`，所有读取和搜索都会追加 `deleted_at IS NULL`。

容量达到上限时不会淘汰旧记忆，而是返回错误。

## 读取与搜索

`ReadMemories` 按 `app_name`、`user_id` 查询，排序为 `updated_at DESC, created_at DESC`，并按需追加 `LIMIT`。

`SearchMemories` 选择当前用户可见行，解码 `memory_data` 后对内容和 topics 做共享关键词打分。默认最低分数 `0.3`，默认最多 `10` 条；支持 kind/time 过滤、事件时间排序、kind fallback 和去重。

## 工具与自动模式

该后端支持通用记忆工具和 `WithExtractor(...)`。自动模式下，后台 worker 使用服务写 API；`Tools()` 默认只向 Agent 暴露 `memory_search`。
