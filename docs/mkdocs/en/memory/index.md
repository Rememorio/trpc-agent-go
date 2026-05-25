# Memory Management

## Overview

Memory is tRPC-Agent-Go's long-term memory module. It stores and retrieves user-related information across sessions. Unlike Session, which manages context for one conversation, Memory is isolated by `<appName, userID>` and is better suited for reusable user profiles, preferences, facts, and events.

### Positioning

Memory can be understood as a long-term profile accumulated around one user. It is suitable for:

- Stable facts: name, occupation, preferred language, technical background
- Preferences: concise answers, recommendation style, formatting choices
- Reusable events: meetings, trips, completed tasks
- Business context: project stack, team roles, historical issue status

### Two Memory Modes

| Dimension | Agentic Mode | Auto Mode |
| --- | --- | --- |
| How it works | Agent decides when to call memory tools | Runner triggers background extraction after a response |
| User experience | Tool calls are visible | Memories are created/updated in the background |
| Writes | Agent calls `memory_add`, `memory_update`, etc. | Extractor calls add/update/delete in background |
| Default exposed tools | `add`, `update`, `search`, `load` | only `memory_search` is exposed to the agent |
| Best for | Explicit “remember this” flows | Natural long-term memory accumulation |

Auto mode requires an `Extractor` and is recommended for most long-term memory use cases.

## Quick Start

### Auto Mode (Recommended)

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
    llmagent.WithTools(memoryService.Tools()), // Auto mode exposes memory_search by default.
)

r := runner.NewRunner(
    "memory-chat",
    agent,
    runner.WithSessionService(sessioninmemory.NewSessionService()),
    runner.WithMemoryService(memoryService),
)
defer r.Close()

_, _ = r.Run(ctx, "user123", "session456", model.NewUserMessage(
    "Hi, my name is John, and I like Go programming",
))
```

### Agentic Mode

```go
memoryService := memoryinmemory.NewMemoryService()

agent := llmagent.New(
    "memory-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithInstruction("Remember important user information and recall it when needed."),
    llmagent.WithTools(memoryService.Tools()), // add/update/search/load by default.
)

r := runner.NewRunner(
    "memory-chat",
    agent,
    runner.WithSessionService(sessioninmemory.NewSessionService()),
    runner.WithMemoryService(memoryService),
)
```

## Core Concepts

### UserKey

Memory is isolated by `UserKey`:

```go
type UserKey struct {
    AppName string
    UserID  string
}
```

Memories for the same user across multiple sessions are stored under the same `<appName, userID>` scope.

### Entry and Memory

A memory entry contains ID, user scope, content, topics, timestamps, and optional episodic metadata:

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

`Kind` distinguishes stable facts from happened events. Episode memories can use `event_time`, `participants`, and `location` for more precise retrieval and deduplication.

### Memory ID

Memory ID is a SHA256 generated from:

- memory text
- `appName`
- `userID`
- non-empty identity metadata: `kind`, `event_time`, `participants`, `location`

`topics` are intentionally excluded, so tag-only edits do not create a new identity. Updating memory text or identity metadata may rotate the ID; use `memory.WithUpdateResult(...)` to receive the new ID.

### Tools

| Tool | Purpose | Default |
| --- | --- | --- |
| `memory_add` | Add memory | enabled in Agentic mode; enabled for Auto background use |
| `memory_update` | Update memory | enabled in Agentic mode; enabled for Auto background use |
| `memory_delete` | Delete memory | hidden from agent by default; enabled for Auto background use |
| `memory_clear` | Clear current user memories | disabled by default |
| `memory_search` | Search relevant memories | enabled by default; exposed in Auto mode |
| `memory_load` | Load recent memories | enabled in Agentic mode; explicit opt-in in Auto mode |

## Storage Backend Comparison

| Backend | Persistence | Semantic Search | Soft Delete | Typical Use |
| --- | --- | --- | --- | --- |
| [InMemory](inmemory.md) | ❌ | ❌ | ❌ | Unit tests and local demos |
| [Redis](redis.md) | ✅ | ❌ | ❌ | Shared lightweight deployments |
| [SQLite](sqlite.md) | ✅ | ❌ | ✅ | Single-node services, CLI, local persistence |
| [SQLiteVec](sqlitevec.md) | ✅ | ✅ | ✅ | Single-file local semantic search |
| [MySQL](mysql.md) | ✅ | ❌ | ✅ | Existing MySQL production services |
| [MySQL Vector](mysqlvec.md) | ✅ | ✅ | ✅ | MySQL semantic search with VECTOR/BLOB fallback |
| [PostgreSQL](postgres.md) | ✅ | ❌ | ✅ | PostgreSQL/JSONB persistence |
| [pgvector](pgvector.md) | ✅ | ✅ | ✅ | PostgreSQL semantic and hybrid search |
| [mem0](mem0.md) | hosted | hosted | hosted | External ingest-first long-term memory platform |

## Backend Selection

- **Development/testing**: [InMemory](inmemory.md)
- **Shared multi-instance memory without SQL**: [Redis](redis.md)
- **Single-node persistence**: [SQLite](sqlite.md)
- **Local semantic search**: [SQLiteVec](sqlitevec.md)
- **Existing MySQL infrastructure**: [MySQL](mysql.md), or [MySQL Vector](mysqlvec.md) for semantic search
- **Existing PostgreSQL infrastructure**: [PostgreSQL](postgres.md), or [pgvector](pgvector.md) for semantic search
- **Hosted extraction/storage**: [mem0](mem0.md) through `runner.WithSessionIngestor(...)`

## Search Capabilities

### Keyword Search

Non-vector backends (InMemory, Redis, SQLite, MySQL, PostgreSQL) load visible memories for the current user and use the shared keyword scorer over content and topics. The default minimum score is `0.3`; the default result cap is `10`.

Common filters include:

- `Kind`
- `TimeAfter` / `TimeBefore`
- `OrderByEventTime`
- `KindFallback`
- `Deduplicate`

### Semantic and Hybrid Search

Vector backends embed the query and run similarity search:

- SQLiteVec: sqlite-vec `MATCH vec_f32(?)`
- MySQL Vector: MySQL 9.0+ `VECTOR`, or BLOB + Go-side cosine fallback
- pgvector: PostgreSQL `embedding <=> $1`

`mysqlvec` and `pgvector` support hybrid search by merging keyword and vector branches with Reciprocal Rank Fusion. `pgvector` also maintains a `search_vector` column and GIN index for its full-text branch.

## Auto Memory Mode

When `WithExtractor(...)` is configured, built-in backends start an `AutoMemoryWorker`. Runner calls `EnqueueAutoMemoryJob` after each response, and the extractor analyzes the session to add, update, or delete memories through the service APIs.

Auto mode exposes tools conservatively:

- `memory_search` is exposed by default;
- `memory_load` requires `WithToolEnabled(memory.LoadToolName, true)`;
- write tools require `WithAutoMemoryExposedTools(...)` or `WithToolExposed(...)` if the agent should call them directly.

## Related Documentation

- [InMemory Backend](inmemory.md)
- [Redis Backend](redis.md)
- [SQLite Backend](sqlite.md)
- [SQLiteVec Backend](sqlitevec.md)
- [MySQL Backend](mysql.md)
- [MySQL Vector Backend](mysqlvec.md)
- [PostgreSQL Backend](postgres.md)
- [pgvector Backend](pgvector.md)
- [mem0 Integration](mem0.md)
- [Session Management](../session/index.md)
