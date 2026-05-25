# 内存存储（InMemory）

**包路径**：`trpc.group/trpc-go/trpc-agent-go/memory/inmemory`

内存后端适合单元测试、本地实验和 Demo。它没有外部依赖，所有记忆都保存在当前进程里，进程退出后数据丢失。


## 特点

- ✅ 无外部依赖，进程内读写最快
- ✅ 并发安全，适合测试和 Demo
- ❌ 不持久化，进程重启后丢失
- ❌ 不支持多实例共享和软删除

## 配置选项

| 配置项 | 作用 |
| --- | --- |
| `WithMemoryLimit(limit)` | 每用户 map 上限。 |
| `WithMinSearchScore(score)` | 关键词分数阈值；负数忽略。 |
| `WithMaxResults(max)` | 默认搜索上限；`0` 不截断。 |
| `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | 自动记忆 worker 配置。 |
| `WithToolEnabled`, `WithCustomTool`, `WithToolExposed` | 工具注册与暴露控制。 |

## 基础配置示例

```go
import memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"

memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithMemoryLimit(200),
)
defer memoryService.Close()
```

## 实现细节

服务内部使用嵌套 map，并用 mutex 保护读写：

```text
appName -> userID -> memoryID -> *memory.Entry
```

`memoryID` 由记忆文本、`<appName, userID>` 和身份相关元数据（`kind`、`event_time`、`participants`、`location`）生成。`Topics` 不参与 ID，所以只改标签不会改变记忆身份。

`ReadMemories` 按 `UpdatedAt` 倒序、`CreatedAt` 倒序返回。`SearchMemories` 在进程内扫描当前用户的 entry，使用共享关键词 scorer：内容和 topics 参与分词/BM25 风格打分，默认最低分数 `0.3`，默认最多 `10` 条，并支持 kind/time 过滤、事件时间排序、kind fallback 和去重。

## 限制与生命周期

- 默认每用户上限 `1000` 条。
- `WithMemoryLimit` 修改 map 容量上限，新增前检查。
- `DeleteMemory` 删除单条 map entry；`ClearMemories` 删除整个用户 map。
- 不支持软删除、TTL 或跨进程共享。
- `Close()` 只负责停止自动记忆 worker，没有持久化句柄需要关闭。

多实例部署时，每个进程都有独立数据；生产环境通常应选择 Redis 或 SQL/向量后端。

## 工具与自动模式

普通模式下，`Tools()` 直接暴露已启用工具。默认启用 `memory_add`、`memory_update`、`memory_search`、`memory_load`；`memory_delete`、`memory_clear` 合法但默认关闭。

设置 `WithExtractor(...)` 后会启动 `AutoMemoryWorker`。后台提取器可以使用 add/update/delete，但 `Tools()` 默认只暴露 `memory_search`。`memory_load` 和写工具需要通过 `WithToolEnabled`、`WithAutoMemoryExposedTools` 或 `WithToolExposed` 显式启用/暴露。
