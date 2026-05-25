# Memory 记忆管理

## 概述

Memory 是 tRPC-Agent-Go 的长期记忆管理模块，用于在跨会话场景中保存和检索用户相关信息。与 Session 只管理单个会话上下文不同，Memory 的隔离维度是 `<appName, userID>`，更适合保存可复用的用户画像、偏好、事实和事件。

### 定位

Memory 可以理解为围绕用户逐步积累的“长期档案”。它适合记录：

- 稳定事实：用户姓名、职业、常用语言、技术背景
- 偏好信息：偏好简短回答、喜欢某类推荐方式
- 可复用事件：某天参加过会议、去过某地、完成过某个任务
- 业务上下文：项目技术栈、团队角色、历史问题状态

### 两种记忆模式

| 维度 | 工具驱动模式（Agentic） | 自动提取模式（Auto） |
| --- | --- | --- |
| 工作方式 | Agent 自己决定何时调用记忆工具 | Runner 响应结束后触发后台提取 |
| 用户体验 | 用户可见工具调用 | 后台静默创建/更新记忆 |
| 写操作 | `memory_add`、`memory_update` 等工具由 Agent 调用 | Extractor 后台调用 add/update/delete |
| 默认暴露工具 | `add`、`update`、`search`、`load` | 默认只向 Agent 暴露 `memory_search` |
| 适合场景 | 用户明确要求“记住...”或需要强控制 | 自然对话中持续积累长期记忆 |

自动提取模式需要配置 `Extractor`，推荐作为长期记忆的默认使用方式。

## 快速开始

### 自动提取模式（推荐）

```go
import (
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory/extractor"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

extractorModel := openai.New("deepseek-v4-flash")
memExtractor := extractor.NewExtractor(extractorModel)

memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(memExtractor),
    memoryinmemory.WithAsyncMemoryNum(1),
    memoryinmemory.WithMemoryQueueSize(10),
    memoryinmemory.WithMemoryJobTimeout(30*time.Second),
)
defer memoryService.Close()

chatModel := openai.New("deepseek-v4-flash")
agent := llmagent.New(
    "memory-assistant",
    llmagent.WithModel(chatModel),
    llmagent.WithTools(memoryService.Tools()), // Auto 模式默认只暴露 memory_search
)

r := runner.NewRunner(
    "memory-chat",
    agent,
    runner.WithSessionService(sessioninmemory.NewSessionService()),
    runner.WithMemoryService(memoryService),
)
defer r.Close()

_, _ = r.Run(ctx, "user123", "session456", model.NewUserMessage(
    "你好，我叫张三，我喜欢 Go 编程",
))
```

### 工具驱动模式

```go
memoryService := memoryinmemory.NewMemoryService()

agent := llmagent.New(
    "memory-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithInstruction("记住用户的重要信息，并在需要时回忆。"),
    llmagent.WithTools(memoryService.Tools()), // 默认暴露 add/update/search/load
)

r := runner.NewRunner(
    "memory-chat",
    agent,
    runner.WithSessionService(sessioninmemory.NewSessionService()),
    runner.WithMemoryService(memoryService),
)
```

## 核心概念

### UserKey

Memory 的最小隔离维度是 `UserKey`：

```go
type UserKey struct {
    AppName string
    UserID  string
}
```

同一用户跨多个 Session 的记忆会归到同一个 `<appName, userID>` 下。

### Entry 与 Memory

一条记忆包含 ID、用户维度、内容、主题、创建/更新时间和可选事件元数据：

```go
type Memory struct {
    Memory      string
    Topics      []string
    LastUpdated *time.Time

    Kind         Kind        // fact / episode
    EventTime    *time.Time
    Participants []string
    Location     string
}
```

`Kind` 用于区分稳定事实（`fact`）和发生过的事件（`episode`）。事件类记忆可通过 `event_time`、`participants`、`location` 做更精确的检索和去重。

### Memory ID

Memory ID 基于以下信息生成 SHA256：

- memory 文本
- `appName`
- `userID`
- 非空/有效的事件元数据：`kind`、`event_time`、`participants`、`location`

`topics` 不参与 ID，因此只调整标签不会生成新记忆；修改记忆文本或身份元数据时，`UpdateMemory` 可能导致 ID 旋转，可用 `memory.WithUpdateResult(...)` 获取新 ID。

### 工具集合

合法工具包括：

| 工具 | 说明 | 默认启用 |
| --- | --- | --- |
| `memory_add` | 添加记忆 | Agentic 模式启用；Auto 模式后台启用 |
| `memory_update` | 更新记忆 | Agentic 模式启用；Auto 模式后台启用 |
| `memory_delete` | 删除记忆 | 默认不向 Agent 暴露；Auto 模式后台启用 |
| `memory_clear` | 清空当前用户记忆 | 默认关闭 |
| `memory_search` | 搜索相关记忆 | 默认启用；Auto 模式默认暴露 |
| `memory_load` | 加载最近记忆 | Agentic 模式默认启用；Auto 模式需显式启用 |

## 存储后端对比

| 后端 | 持久化 | 语义搜索 | 软删除 | 典型场景 |
| --- | --- | --- | --- | --- |
| [InMemory](inmemory.md) | ❌ | ❌ | ❌ | 单元测试、本地 Demo |
| [Redis](redis.md) | ✅ | ❌ | ❌ | 多实例共享、轻量生产部署 |
| [SQLite](sqlite.md) | ✅ | ❌ | ✅ | 单机服务、CLI、本地持久化 |
| [SQLiteVec](sqlitevec.md) | ✅ | ✅ | ✅ | 本地单文件 + 语义搜索 |
| [MySQL](mysql.md) | ✅ | ❌ | ✅ | 已有 MySQL 的生产服务 |
| [MySQL Vector](mysqlvec.md) | ✅ | ✅ | ✅ | MySQL 语义搜索，支持 VECTOR/BLOB fallback |
| [PostgreSQL](postgres.md) | ✅ | ❌ | ✅ | PostgreSQL/JSONB 持久化 |
| [pgvector](pgvector.md) | ✅ | ✅ | ✅ | PostgreSQL 语义搜索和 hybrid search |
| [mem0](mem0.md) | 托管 | 托管 | 托管 | 外部长时记忆平台，ingest-first |

## 后端选择建议

- **开发测试**：使用 [InMemory](inmemory.md)，无需外部依赖。
- **多实例共享但不需要 SQL**：使用 [Redis](redis.md)。
- **单机持久化**：使用 [SQLite](sqlite.md)。
- **本地语义搜索**：使用 [SQLiteVec](sqlitevec.md)。
- **已有 MySQL 基础设施**：使用 [MySQL](mysql.md)；需要语义搜索时使用 [MySQL Vector](mysqlvec.md)。
- **已有 PostgreSQL 基础设施**：使用 [PostgreSQL](postgres.md)；需要语义搜索时使用 [pgvector](pgvector.md)。
- **希望由外部系统托管提取和存储**：使用 [mem0](mem0.md)，通过 `runner.WithSessionIngestor(...)` 接入。

## 搜索能力

### 关键词搜索

非向量后端（InMemory、Redis、SQLite、MySQL、PostgreSQL）会先加载当前用户可见记忆，再使用共享关键词 scorer 对内容和 topics 打分。默认最低分数为 `0.3`，默认最多返回 `10` 条。

支持的通用过滤包括：

- `Kind`
- `TimeAfter` / `TimeBefore`
- `OrderByEventTime`
- `KindFallback`
- `Deduplicate`

### 语义与混合搜索

向量后端会先对 query 生成 embedding，再做相似度检索：

- SQLiteVec：使用 sqlite-vec `MATCH vec_f32(?)`
- MySQL Vector：MySQL 9.0+ 使用 `VECTOR`；旧版本回退到 BLOB + Go 侧 cosine
- pgvector：使用 PostgreSQL `embedding <=> $1`

`mysqlvec` 和 `pgvector` 支持 hybrid search，将关键词分支和向量分支通过 Reciprocal Rank Fusion 合并。`pgvector` 还会维护 `search_vector` 和 GIN 索引用于全文检索分支。

## 自动记忆模式

配置 `WithExtractor(...)` 后，内置后端会启动后台 `AutoMemoryWorker`。Runner 每轮响应结束后调用 `EnqueueAutoMemoryJob`，Extractor 分析会话并通过服务写 API 添加、更新或删除记忆。

Auto 模式下工具暴露更保守：

- 默认只向 Agent 暴露 `memory_search`
- `memory_load` 需要显式 `WithToolEnabled(memory.LoadToolName, true)`
- 写工具如需暴露给 Agent，需使用 `WithAutoMemoryExposedTools(...)` 或 `WithToolExposed(...)`

## 相关文档

- [InMemory 后端](inmemory.md)
- [Redis 后端](redis.md)
- [SQLite 后端](sqlite.md)
- [SQLiteVec 后端](sqlitevec.md)
- [MySQL 后端](mysql.md)
- [MySQL Vector 后端](mysqlvec.md)
- [PostgreSQL 后端](postgres.md)
- [pgvector 后端](pgvector.md)
- [mem0 集成](mem0.md)
- [Session 会话管理](../session/index.md)
