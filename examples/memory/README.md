# Memory Service and Tool Example

This example demonstrates both the Memory Service and Memory Tool functionality, showing how to store, retrieve, and search conversation memories using different approaches.

## What This Example Demonstrates

This implementation showcases two different approaches to working with memory in conversational AI:

### Part 1: Memory Service Example

- **📝 Direct Session Storage**: Store complete conversation sessions with events
- **🔍 Advanced Search**: Search memories with filters, sorting, and pagination
- **📊 Memory Statistics**: Get insights about stored memories and sessions
- **🗑️ Memory Management**: Delete specific sessions or all user memories

### Part 2: Memory Tool Example

- **🤖 Tool-Based Storage**: Use Memory Tool to automatically store user information
- **💡 Smart Information Extraction**: Tool designed to identify and store user preferences
- **🔗 Service Integration**: Memory Tool works with the same Memory Service
- **📋 Structured Input/Output**: JSON-based tool interface for easy integration

### Key Features

- **Memory Service**: Low-level API for direct memory management
- **Memory Tool**: High-level tool for automated user information storage
- **Semantic Search**: Find relevant memories using natural language queries
- **Memory Statistics**: Track memory usage and conversation patterns
- **Type Safety**: Strongly typed interfaces for reliable memory operations
- **Error Handling**: Graceful error recovery and reporting

## Prerequisites

- Go 1.23 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument      | Description                                     | Default Value    |
| ------------- | ----------------------------------------------- | ---------------- |
| `-model`      | Name of the model to use                        | `deepseek-chat`  |
| `-session`    | Session service: `inmemory` or `redis`          | `inmemory`       |
| `-redis-addr` | Redis server address (when using redis session) | `localhost:6379` |
| `-memory`     | Enable memory functionality                     | `true`           |

## Usage

### Basic Chat with Memory

```bash
cd examples/memory
export OPENAI_API_KEY="your-api-key-here"
go run main.go
```

### Custom Model with Memory

```bash
export OPENAI_API_KEY="your-api-key"
go run main.go -model gpt-4o
```

### Disable Memory (Basic Chat)

```bash
export OPENAI_API_KEY="your-api-key"
go run main.go -memory=false
```

### With Redis Session and Memory

```bash
export OPENAI_API_KEY="your-api-key"
go run main.go -session redis -redis-addr localhost:6379
```

## Memory System Features

The memory system provides several powerful features for managing conversation history:

### 📊 Memory Statistics (`/memory`)

View comprehensive statistics about stored memories:

```
📊 Memory Statistics:
   Total Memories: 15
   Total Sessions: 3
   Avg Memories/Session: 5.00
   Oldest Memory: 2024-01-15 10:30:00
   Newest Memory: 2024-01-15 14:45:00
```

### 📝 Session Summarization (`/summary`)

Generate AI-powered summaries of the current conversation session:

```
📝 Session Summary:
   The user and assistant discussed mathematical calculations,
   time zones, and memory system features. The user tested
   calculator and time tools, and explored memory commands
   including statistics and search functionality.
```

### 🔍 Memory Search (`/search <query>`)

Search through conversation history using natural language:

```
🔍 Found 3 memories for query: 'calculator':
   1. [2024-01-15 14:30:00] user: Can you calculate 25 * 4?
   2. [2024-01-15 14:31:00] assistant: I calculated 25 × 4 = 100 for you.
   3. [2024-01-15 14:35:00] user: What about 100 divided by 7?
```

## Implemented Tools

The example includes two working tools:

### 🧮 Calculator Tool

- **Function**: `calculator`
- **Operations**: add, subtract, multiply, divide
- **Usage**: "Calculate 15 \* 25" or "What's 100 divided by 7?"
- **Arguments**: operation (string), a (number), b (number)

### 🕐 Time Tool

- **Function**: `current_time`
- **Timezones**: UTC, EST, PST, CST, or local time
- **Usage**: "What time is it in EST?" or "Current time please"
- **Arguments**: timezone (optional string)

## Tool Calling Process

When you ask for calculations or time information, you'll see:

```
🔧 Tool calls initiated:
   • calculator (ID: call_abc123)
     Args: {"operation":"multiply","a":25,"b":4}

🔄 Executing tools...
✅ Tool response (ID: call_abc123): {"operation":"multiply","a":25,"b":4,"result":100}

🤖 Assistant: I calculated 25 × 4 = 100 for you.
```

## Chat Interface

The interface provides comprehensive memory management:

```
🚀 Multi-turn Chat with Runner + Tools + Memory
Model: gpt-4o-mini
Memory: true
Type 'exit' to end the conversation
==================================================
✅ Chat ready! Session: chat-session-1703123456

💡 Special commands:
   /history  - Show conversation history
   /new      - Start a new session
   /memory   - Show memory statistics
   /summary  - Generate session summary
   /search   - Search memory (usage: /search <query>)
   /exit      - End the conversation

👤 You: Hello! How are you today?
🤖 Assistant: Hello! I'm doing well, thank you for asking. I'm here and ready to help you with whatever you need. How are you doing today?

👤 You: /memory
📊 Memory Statistics:
   Total Memories: 2
   Total Sessions: 1
   Avg Memories/Session: 2.00
   Oldest Memory: 2024-01-15 14:30:00
   Newest Memory: 2024-01-15 14:31:00

👤 You: /search greeting
🔍 Found 1 memories for query: 'greeting':
   1. [2024-01-15 14:30:00] user: Hello! How are you today?

👤 You: /exit
👋 Goodbye!
```

### Session Commands

- `/history` - Ask the agent to show conversation history
- `/new` - Start a new session (resets conversation context)
- `/memory` - Show memory statistics and analytics
- `/summary` - Generate AI-powered session summary
- `/search <query>` - Search memory using natural language
- `/exit` - End the conversation

## Memory System Architecture

The memory system consists of several components:

### Memory Service

- **In-Memory Storage**: Fast, temporary storage for development and testing
- **Session Integration**: Automatic storage of conversation events
- **Event Processing**: Handles user messages, assistant responses, and tool calls

### Memory Summarizer

- **AI-Powered Summarization**: Uses the same LLM model for consistent summaries
- **Configurable Prompts**: Customizable summarization prompts and modes
- **Session Context**: Maintains conversation context for better summaries

### Memory Search

- **Semantic Search**: Find relevant memories using natural language queries
- **Content Extraction**: Intelligently extracts readable content from events
- **Ranked Results**: Returns search results with relevance scoring

## Memory vs Session

| Feature           | Session                      | Memory                          |
| ----------------- | ---------------------------- | ------------------------------- |
| **Purpose**       | Temporary conversation state | Persistent conversation history |
| **Lifetime**      | Session duration             | Permanent storage               |
| **Content**       | Current conversation         | All past conversations          |
| **Search**        | No                           | Semantic search available       |
| **Summarization** | No                           | AI-powered summaries            |
| **Analytics**     | No                           | Statistics and insights         |

## Best Practices

1. **Memory Management**: Use memory for long-term conversation storage
2. **Session Management**: Use sessions for temporary conversation state
3. **Search Queries**: Use natural language for better search results
4. **Regular Summaries**: Generate summaries periodically for long conversations
5. **Memory Cleanup**: Consider implementing memory cleanup for old conversations

## Troubleshooting

### Memory Not Working

- Ensure `-memory=true` flag is set (default)
- Check that the model service is accessible
- Verify API key configuration

### Search Not Finding Results

- Use natural language queries instead of exact keywords
- Check that conversations have been stored in memory
- Try different query formulations

### Summary Generation Fails

- Ensure the LLM model supports summarization
- Check API rate limits and quotas
- Verify model configuration and permissions
