# Mem0 Integration Example

这个示例展示新的 **ingest-first** `mem0` 集成方式：

- Runner 在对话完成后把 session 增量消息异步送入 mem0
- Agent 通过只读工具使用 mem0 中的长期记忆：
  - `memory_search`
  - `memory_load`
- **不再**把 mem0 当作 core `memory.Service` backend
- **不再**支持 `memory_add / memory_update / memory_delete / memory_clear`

## 环境变量

```bash
export OPENAI_API_KEY="your-openai-api-key"
export MEM0_API_KEY="your-mem0-api-key"
export MEM0_BASE_URL="https://api.mem0.ai"
# 或者：export MEM0_HOST="https://api.mem0.ai"
export MEM0_ORG_ID=""
export MEM0_PROJECT_ID=""
```

## 运行

```bash
cd examples
go run ./mem0 --model deepseek-chat
```

> 如果你本地没装 `ngo`，直接用 `go run ./mem0 --model deepseek-chat` 也可以。

## 核心接入点

```go
mem0Svc, _ := memorymem0.NewService(...)

agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New(modelName)),
    llmagent.WithTools(mem0Svc.Tools()),
)

runner := runner.NewRunner(
    appName,
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(mem0Svc),
)
```

## 设计说明

新的实现有两个核心原则：

1. **ingest-first**：把原始对话交给 mem0 native ingest
2. **read-only tools**：Agent 只通过 search/load 读取 mem0 中的记忆

这样避免了把 mem0 强行适配为框架内部的标准 CRUD memory backend。
