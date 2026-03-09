# PGVector Session Demo

This example shows how to use `session/pgvector` as a session backend and run semantic recall over the current conversation.

## Features

- Chat with a normal `Runner`
- Persist the session in PostgreSQL with the `pgvector` extension
- Recall semantically similar session events with `/search <query>`

## Prerequisites

- PostgreSQL with the `vector` extension installed
- Chat model credentials:
  - `OPENAI_API_KEY`
  - `OPENAI_BASE_URL` if you use a compatible endpoint
- Embedding credentials:
  - `OPENAI_EMBEDDING_API_KEY` and `OPENAI_EMBEDDING_BASE_URL`
  - If omitted, the example falls back to `OPENAI_API_KEY` and `OPENAI_BASE_URL`

## PGVector Environment Variables

| Variable | Default |
| --- | --- |
| `PGVECTOR_HOST` | `localhost` |
| `PGVECTOR_PORT` | `5432` |
| `PGVECTOR_USER` | `postgres` |
| `PGVECTOR_PASSWORD` | `` |
| `PGVECTOR_DATABASE` | `trpc-agent-go-pgsession` |
| `PGVECTOR_EMBEDDER_MODEL` | `text-embedding-3-small` |

## Run

```bash
cd examples/session/pgvector
go run . -model deepseek-chat
```

## Commands

- `/search <query>`: recall the most similar events from the current session
- `/new`: start a fresh session
- `/exit`: quit

## Example

```text
[7c0ac8f2] You: I'm planning a trip to Osaka in April
│  User: I'm planning a trip to Osaka in April
│  Assistant: Nice. I can help with itinerary ideas, flights, and hotels.

[7c0ac8f2] You: /search japan travel
Semantic recall for "japan travel":
  1. [0.927] assistant Nice. I can help with itinerary ideas, flights, and hotels.
```
