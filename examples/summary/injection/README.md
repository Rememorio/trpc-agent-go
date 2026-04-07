# 📝 Summary Injection Mode Example

This example demonstrates the two session summary injection modes:

- **System mode** (default): Summary is merged into the system message, protected from token tailoring trimming.
- **User mode**: Summary is injected as a user message before session history, participating in token-budget trimming for a true sliding-window experience.

## What it shows

- Two sequential conversations with the same session — each using a different injection mode.
- The actual message sequence sent to the LLM is printed via a `BeforeModel` callback, so you can see exactly where the summary appears and with which role.
- In **system mode**, the summary is merged into the system prompt (or prepended as a new system message).
- In **user mode**, the summary appears as a user message between any system/few-shot content and the session history. If the first history message is also a user message, the summary is merged into it to avoid consecutive user messages.

## Prerequisites

- Go 1.21 or later.
- Model configuration via environment variables.

Environment variables:

- `OPENAI_API_KEY`: API key for model service.
- `OPENAI_BASE_URL` (optional): Base URL for the model API endpoint.

## Run

```bash
cd examples/summary/injection
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat
```

Command-line flags:

- `-model`: Model name. Default: `deepseek-chat`.

## Example Output

```
🧪 Summary Injection Mode Demo
Model: deepseek-chat
Session: injection-demo-1712500000
======================================================================
== Phase 1: Build conversation history (2 turns) ==

👤 User: My name is Alice and I work at TechCorp as a senior engineer.
🤖 Assistant: Nice to meet you, Alice! ...
👤 User: I'm working on a distributed cache system using Go and Redis.
🤖 Assistant: That sounds like an interesting project! ...

📝 Forcing summary generation...
✅ Summary: Alice is a senior engineer at TechCorp working on a distributed cache system using Go and Redis.

======================================================================
== Phase 2: System injection mode (default) ==

🧾 LLM request messages:
   [0] role=system content="You are a helpful assistant.\n\nHere is a brief summary..."
   [1] role=user   content="Based on our previous discussion, what language am I using?"

🤖 Assistant: Based on our conversation, you're using Go ...

======================================================================
== Phase 3: User injection mode ==

🧾 LLM request messages:
   [0] role=system content="You are a helpful assistant."
   [1] role=user   content="Context from previous interactions:\n\n<summary>..."
   [2] role=user   content="Based on our previous discussion, what language am I using?"

🤖 Assistant: You mentioned you're using Go ...
```

## Key Observations

1. **System mode**: Summary appears inside `messages[0]` (system role), merged with agent instruction.
2. **User mode**: Summary appears as a separate user message between system prompt and history. Token tailoring can trim it like any other user round.
3. Both modes produce correct responses — the LLM can access the summary context regardless of injection position.
