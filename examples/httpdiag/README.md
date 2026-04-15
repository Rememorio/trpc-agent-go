# HTTP Diagnostics (httpdiag) — Interactive Debug Chat

This example runs an interactive multi-turn chat with **all model HTTP
request/response bodies printed by default**, so you can see exactly what goes
over the wire between your agent and the LLM provider.

## What It Does

- Starts a multi-turn conversational loop (like `examples/runner`)
- Every HTTP request sent to the model API is logged (method, URL, full body)
- Every HTTP response received is logged (status, full body)
- 200-OK-with-hidden-error responses are automatically rewritten to 400

This is the fastest way to debug prompt formatting, token usage, tool call
serialization, and gateway-level issues.

## Prerequisites

- Go 1.23 or later
- A valid OpenAI-compatible API key

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument         | Description                                | Default Value |
| ---------------- | ------------------------------------------ | ------------- |
| `-model`         | Name of the model to use                   | `gpt-5.4`    |
| `-variant`       | OpenAI variant                             | `openai`      |
| `-streaming`     | Enable streaming mode                      | `true`        |
| `-req-body`      | Log full request body sent to model        | `true`        |
| `-resp-body`     | Log full response body from model          | `true`        |
| `-error-rewrite` | Rewrite 200-with-error responses to 400    | `true`        |

## Usage

### Default (httpdiag-only diagnostics):

```bash
cd examples/httpdiag
export OPENAI_API_KEY="your-api-key"
go run .
```

The example injects a dedicated logger via `httpdiag.SetLogger(...)`, so you see
HTTP diagnostics without turning on the framework's global debug logs.

### Quiet mode (metadata only, no bodies):

```bash
go run . -req-body=false -resp-body=false
```

### Non-streaming:

```bash
go run . -streaming=false
```

## Available Tools

| Tool             | Description                              |
| ---------------- | ---------------------------------------- |
| `calculator`     | add, subtract, multiply, divide, sqrt, power |
| `current_time`   | Get current time in a given timezone     |

## Example Session

```
🔍 httpdiag interactive demo: debug LLM HTTP interactions
Model: gpt-5.4
Streaming: true
Log req body: true
Log resp body: true
Error rewrite: true
Type '/exit' to end the conversation
Available tools: calculator, current_time
==================================================
✅ Chat ready! Session: httpdiag-1703123456

👤 You: What is 42 * 58?
httpdiag: -> POST https://api.openai.com/v1/chat/completions
httpdiag: request body:
{
  "model": "gpt-5.4",
  "messages": [{"role":"user","content":"What is 42 * 58?"}],
  "tools": [...]
}
httpdiag: <- POST https://api.openai.com/v1/chat/completions status=200
httpdiag: response body (status=200):
{
  "choices": [{"message":{"tool_calls":[...]}}]
}
🔧 Tool calls initiated:
   • calculator (ID: call_xxx)
     Args: {"operation":"multiply","a":42,"b":58}

🔄 Executing tools...
✅ Tool response (ID: call_xxx): {"operation":"multiply","a":42,"b":58,"result":2436}
httpdiag: -> POST https://api.openai.com/v1/chat/completions
httpdiag: request body:
{
  "messages": [{"role":"tool","content":"..."}]
}
...
🤖 Assistant: 42 × 58 = 2,436

👤 You: /exit
👋 Goodbye!
```

## How It Works

1. Builds a chain of `httpdiag.Middleware` functions based on CLI flags
2. Converts them to OpenAI SDK options via `httpdiag.OpenAIMiddleware(...)`
3. Passes the options to `openai.WithOpenAIOptions(...)` when creating the model
4. Every HTTP round-trip is intercepted and logged by the middleware chain

### Using with Anthropic

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
    "trpc.group/trpc-go/trpc-agent-go/plugin/httpdiag"
)

llm := anthropic.New("claude-sonnet-4-0",
    anthropic.WithAnthropicClientOptions(
        httpdiag.AnthropicMiddleware(
            httpdiag.RequestLoggingMiddleware(),
            httpdiag.RequestBodyLoggingMiddleware(),
            httpdiag.ResponseBodyLoggingMiddleware(),
            httpdiag.ErrorResponseMiddleware(),
        )...,
    ),
)
```

## Files

- `main.go` — Interactive chat loop + Runner setup with httpdiag middlewares
- `tools.go` — Calculator and current_time tool implementations
