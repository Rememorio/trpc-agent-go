# OpenAI Responses API Example

This example uses the first-class `model/openai/responses` adapter. The OpenAI
SDK reads `OPENAI_API_KEY` and optional `OPENAI_BASE_URL` from the environment.

```bash
cd examples/model/responses
go run . -model gpt-5.2
```

The adapter defaults to local, stateless replay with `store:false`. It retains
the ordered provider output items in `ProviderData`, including encrypted
reasoning needed for later turns. Use `WithStateMode` or per-request Responses
options when server-side response chains or Conversations are preferred.
