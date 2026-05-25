# Redis 存储

**包路径**：`trpc.group/trpc-go/trpc-agent-go/memory/redis`

Redis 后端适合多个进程共享记忆，但又不想引入 SQL 数据库的场景。它把 JSON 编码的 `memory.Entry` 存在 Redis Hash 中；搜索时加载当前用户的 Hash，在 Go 进程内做关键词打分。


## 特点

- ✅ 多进程共享，适合轻量分布式部署
- ✅ Redis Cluster 下按用户 hash tag 分槽
- ✅ Key 前缀便于多环境复用实例
- ❌ 无软删除，搜索需要加载用户 Hash 后在 Go 中打分

## 配置选项

| 配置项 | 作用 |
| --- | --- |
| `WithRedisClientURL(url)` | URL 创建客户端，最高优先级。 |
| `WithRedisInstance(name)` | URL 为空时使用注册实例。 |
| `WithKeyPrefix(prefix)` | 给所有 Redis key 加前缀。 |
| `WithExtraOptions(...)` | 透传给 Redis client builder。 |
| `WithMemoryLimit`, `WithMinSearchScore`, `WithMaxResults` | 容量和关键词搜索控制。 |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | 自动提取控制。 |

## 基础配置示例

```go
import memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"

redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithKeyPrefix("prod"),
)
if err != nil {
    // handle error
}
defer redisService.Close()
```

`WithRedisClientURL` 优先级高于 `WithRedisInstance`。`NewService` 返回前会用 5 秒超时执行 `PING`。

## Key 结构

每个用户一个 Redis Hash：

```text
mem:{<appName>:<userID>}
```

field 是 `memoryID`，value 是 JSON `memory.Entry`。`{app:user}` 是 Redis Cluster hash tag，可保证同一用户的记忆在一个 slot，同时不同用户能分散到不同 slot。

`WithKeyPrefix("prod")` 后 key 为：

```text
prod:mem:{<appName>:<userID>}
```

旧版本使用过 `mem:{appName}:userID`，当前布局改为 `{appName:userID}`；升级时旧数据需要显式迁移。

## 操作语义

| API | Redis 行为 |
| --- | --- |
| `AddMemory` | `HSET key memoryID entryJSON`。 |
| `UpdateMemory` | `HGET` 旧 entry 后更新；如果 ID 旋转，用事务 pipeline 写新 field 并删除旧 field。 |
| `DeleteMemory` | `HDEL key memoryID`。 |
| `ClearMemories` | `DEL key`。 |
| `ReadMemories` | `HGETALL`、JSON 解码、normalize 旧 entry、按 `UpdatedAt`/`CreatedAt` 倒序排序。 |
| `SearchMemories` | `HGETALL`、解码后走共享关键词搜索。 |

Redis 后端没有软删除，也没有内置 TTL。

## 搜索与容量

搜索不是 Redis 全文索引。服务会扫描当前用户 Hash，使用共享 scorer 对内容和 topics 打分；默认最低分数 `0.3`，默认最多 `10` 条，支持 kind/time 过滤、事件时间排序、kind fallback 和去重。

`WithMemoryLimit(limit)` 写入前检查 `HLEN`。到达上限时返回错误，不会自动淘汰旧 entry。

## 工具与自动模式

工具策略与其他内置后端一致：普通模式直接暴露已启用工具；自动模式（`WithExtractor`）启动 worker，`Tools()` 默认只暴露 `memory_search`。如需 `memory_load` 或写工具，需要显式启用/暴露。
