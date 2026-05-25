# External Long-Term Memory Integration (`mem0`)

**Package**: `trpc.group/trpc-go/trpc-agent-go/memory/mem0`

`memory/mem0` integrates with [mem0](https://mem0.ai) as a hosted long-term memory system. It is intentionally **ingest-first**: the framework sends completed session transcripts to mem0, and mem0 owns extraction and storage.


## Features

- ✅ Hosted long-term memory without self-managed storage
- ✅ Incremental session transcript ingest after each turn
- ✅ Agent gets read-only memory_search by default
- ❌ Not a full memory.Service; no framework-side CRUD
- ❌ Data and extraction are owned by mem0

## Configuration Options

| Option | Effect |
| --- | --- |
| `WithAPIKey(key)` | API key for requests. |
| `WithHost(url)` | Base URL override. |
| `WithOrgProject(orgID, projectID)` | Adds org/project to ingest and retrieval. |
| `WithAsyncMode(bool)` | Sets mem0 `async_mode`. |
| `WithVersion(v)` | Ingest API version; default `v2`. |
| `WithTimeout(d)`, `WithHTTPClient(c)` | HTTP client controls. |
| `WithLoadToolEnabled(bool)` | Adds `memory_load` to `Tools()`. |
| `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout` | Worker, queue, and fallback timeout controls. |

## Basic Configuration Example

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

## Ingestor, Not MemoryService

Do not register this integration with `runner.WithMemoryService(...)`. It does not provide framework-owned CRUD writes to the agent. Use it as a session ingestor:

```go
r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(mem0Svc),
)
```

The agent can still receive read-only tools from `mem0Svc.Tools()`.


Defaults are host `https://api.mem0.ai`, HTTP timeout `10s`, async ingest `true`, ingest version `v2`, one worker, queue size `10`, and job timeout `30s`.

## Ingestion Flow

`IngestSession(ctx, sess, opts...)`:

1. validates `sess.AppName` and `sess.UserID`;
2. reads the last extracted timestamp from session state;
3. scans only new messages since that timestamp;
4. writes the latest timestamp back to session state;
5. enqueues a job to background workers;
6. if the queue is full and context is still valid, runs synchronous fallback with `WithMemoryJobTimeout`.

Per-request options are forwarded to mem0's create-memory payload:

- `session.WithIngestMetadata(...)` -> `metadata`
- `session.WithIngestAgentID(...)` -> `agent_id`
- `session.WithIngestRunID(...)` -> `run_id`

Create requests include `messages`, `user_id`, `app_id`, optional org/project, `infer: true`, `async_mode`, and `version`.

## Tools and Reads

`Tools()` always includes `memory_search`. It includes `memory_load` only when `WithLoadToolEnabled(true)` is set.

`ReadMemories` pages through mem0 `GET /v1/memories/` filtered by `user_id` and `app_id` (plus optional org/project), converts records to `memory.Entry`, sorts by `UpdatedAt` then `CreatedAt` descending, and applies `limit`.

`SearchMemories` calls mem0 search with an `AND` filter for `user_id` and `app_id`; empty query returns empty results. Returned metadata is mapped back into framework fields when present: topics, kind, event time, participants, and location. Kind/time filters from tool requests are applied after mem0 returns candidates.

## When to Use

Use mem0 when you want hosted extraction/storage and only read-oriented agent tools. Use a built-in backend when the agent needs full CRUD tools, framework-side preload semantics, or all data must remain in your own infrastructure.
