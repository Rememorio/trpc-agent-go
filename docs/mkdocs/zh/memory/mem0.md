# 外部长时记忆平台集成（`mem0`）

**包路径**：`trpc.group/trpc-go/trpc-agent-go/memory/mem0`

`memory/mem0` 集成 [mem0](https://mem0.ai) 托管长时记忆系统。它是 **ingest-first** 模式：框架把完成后的会话转交给 mem0，由 mem0 负责提取和存储。


## 特点

- ✅ 外部托管长时记忆，无需自建存储
- ✅ Runner 每轮结束后增量 ingest session transcript
- ✅ Agent 默认只拿只读 memory_search 工具
- ❌ 不是完整 memory.Service，不提供框架侧 CRUD
- ❌ 数据和提取逻辑由 mem0 托管

## 配置选项

| 配置项 | 作用 |
| --- | --- |
| `WithAPIKey(key)` | 请求 API Key。 |
| `WithHost(url)` | 覆盖 base URL。 |
| `WithOrgProject(orgID, projectID)` | ingest 和检索带上 org/project。 |
| `WithAsyncMode(bool)` | 设置 mem0 `async_mode`。 |
| `WithVersion(v)` | ingest API version，默认 `v2`。 |
| `WithTimeout(d)`, `WithHTTPClient(c)` | HTTP client 控制。 |
| `WithLoadToolEnabled(bool)` | 将 `memory_load` 加入 `Tools()`。 |
| `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | worker、队列和 fallback 超时控制。 |

## 基础配置示例

```go
import (
    "os"
    "time"

    memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
)

mem0Svc, err := memorymem0.NewService(
    memorymem0.WithAPIKey(os.Getenv("MEM0_API_KEY")),
    memorymem0.WithHost(os.Getenv("MEM0_HOST")),
    memorymem0.WithOrgProject(os.Getenv("MEM0_ORG_ID"), os.Getenv("MEM0_PROJECT_ID")),
    memorymem0.WithLoadToolEnabled(true),
    memorymem0.WithMemoryJobTimeout(30*time.Second),
)
if err != nil {
    // handle error
}
defer mem0Svc.Close()
```

## Ingestor，不是 MemoryService

不要用 `runner.WithMemoryService(...)` 注册该集成。它不向 Agent 提供框架侧 CRUD 写操作。应作为 session ingestor 使用：

```go
r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(mem0Svc),
)
```

Agent 仍然可以通过 `mem0Svc.Tools()` 获得只读工具。


默认 host 为 `https://api.mem0.ai`，HTTP 超时 `10s`，异步 ingest 开启，版本 `v2`，1 个 worker，队列大小 `10`，任务超时 `30s`。

## Ingestion 流程

`IngestSession(ctx, sess, opts...)` 会：

1. 校验 `sess.AppName` 和 `sess.UserID`；
2. 从 session state 读取上次提取时间；
3. 只扫描该时间之后的新消息；
4. 把最新时间写回 session state；
5. 投递到后台 worker 队列；
6. 队列满且 context 未取消时，使用 `WithMemoryJobTimeout` 做同步 fallback。

请求级选项会转发给 mem0 create-memory payload：

- `session.WithIngestMetadata(...)` -> `metadata`
- `session.WithIngestAgentID(...)` -> `agent_id`
- `session.WithIngestRunID(...)` -> `run_id`

create 请求包含 `messages`、`user_id`、`app_id`、可选 org/project、`infer: true`、`async_mode` 和 `version`。

## 工具与读取

`Tools()` 一定包含 `memory_search`。只有 `WithLoadToolEnabled(true)` 时才包含 `memory_load`。

`ReadMemories` 会分页调用 mem0 `GET /v1/memories/`，按 `user_id` 和 `app_id`（以及可选 org/project）过滤，转成 `memory.Entry` 后按 `UpdatedAt`、`CreatedAt` 倒序排序并应用 limit。

`SearchMemories` 调用 mem0 search，并带上 `user_id`、`app_id` 的 `AND` filter；空 query 返回空结果。返回 metadata 会尽量映射回框架字段：topics、kind、event time、participants、location。工具请求中的 kind/time 过滤在 mem0 返回候选后应用。

## 适用场景

适合希望由 mem0 托管提取、排序和存储，且 Agent 只需要只读工具的场景。如果 Agent 需要完整 CRUD、框架侧 preload 语义，或数据必须全部留在自有基础设施中，应使用内置后端。
