# Session System Prompt Demo

This example demonstrates the recommended way to keep a different
**session-level system prompt** for each session.

Instead of appending a `RoleSystem` event to session history, it stores the
prompt in session state with `UpdateSessionState`, then injects that value into
`LLMAgent` global instruction through a placeholder.

## Why this example exists

If you need **different system prompts for different sessions**, using
`AppendEvent(... RoleSystem ...)` is not reliable in this repository because the
session history path is not designed to treat appended system events as the
canonical system prompt source.

The recommended pattern is:

1. Store the session-specific prompt in session state.
2. Configure the agent with `WithGlobalInstruction(...)`.
3. Reference the session value through a placeholder such as
   `{session_system_prompt?}`.

## What it demonstrates

- Session-specific system prompt injection.
- Multi-turn chat with session switching.
- Multiple session backends via `examples/session` shared `util` package.
- Task-plan style prompts with `\n` support for multi-line plans.

## Quick start

```bash
cd examples/session/systemprompt
export OPENAI_API_KEY="your-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"

go run . -session=inmemory
```

You can also use other backends, consistent with the other session examples:

```bash
go run . -session=sqlite
go run . -session=redis
go run . -session=postgres
go run . -session=mysql
go run . -session=clickhouse
```

## Flags

| Flag | Description | Default |
| ---- | ----------- | ------- |
| `-model` | Model name to use | `MODEL_NAME` or `deepseek-chat` |
| `-session` | Session backend | `inmemory` |
| `-event-limit` | Maximum stored events per session | `1000` |
| `-session-ttl` | Session TTL | `24h` |
| `-streaming` | Enable streaming mode | `true` |

## Commands

| Command | Description |
| ------- | ----------- |
| `/prompt <text>` | Set the current session system prompt |
| `/plan <text>` | Alias of `/prompt`, useful when the prompt is a task plan |
| `/show-prompt` | Show the active session system prompt |
| `/new [id]` | Start a new session with the default prompt |
| `/use <id>` | Switch to an existing session |
| `/sessions` | List sessions and prompt previews |
| `/exit` | End the demo |

Compatibility aliases are also supported:

- `/persona <text>`
- `/show-plan`
- `/show-persona`

## Multi-line task-plan prompt

The demo accepts `\n` inside `/prompt` or `/plan`, and converts it into actual
newlines before storing it in session state.

Example:

```text
/plan You are coordinating a multimodal task.\nStep 1: Collect images.\nStep 2: Summarize the visual findings.\nStep 3: Draft the final answer.
```

That value is then injected into the agent's real system prompt for the current
session.

## Can this model a per-session task plan?

**Yes, partially.**

This example is a good fit when your requirement is:

- each session has a different plan or system prompt;
- the plan must persist across turns in that session;
- switching sessions should switch the active plan automatically.

It is **not** a full workflow engine by itself. The demo does **not**:

- generate the plan automatically from user profile or user info;
- track which step is already completed;
- manage multimodal artifacts or step state explicitly.

If your real scenario is:

1. read user info;
2. generate a multi-step multimodal plan;
3. keep chatting across turns while following that plan;

then this example covers **the prompt persistence/injection layer**, but you
still need one more orchestration layer to:

- generate the plan text;
- store or update step progress in session state;
- optionally store files, artifacts, or structured task state separately.

## Key implementation idea

The agent is configured like this conceptually:

```go
llmagent.WithGlobalInstruction(
    "...\nSession system prompt:\n{session_system_prompt?}",
)
```

And the session-specific value is updated with:

```go
sessionService.UpdateSessionState(ctx, key, session.StateMap{
    "session_system_prompt": []byte(prompt),
})
```

This keeps the **authoritative system prompt** on the agent side while still
making it **session-specific**.

## Files of interest

- `main.go`: interactive demo and session prompt management.
- `../util.go`: shared session backend creation utilities used by all session
  examples.
