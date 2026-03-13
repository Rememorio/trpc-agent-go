# Context-Aware Summary Routing Example

This example demonstrates a business-side pattern for two related needs:

1. Pick a different summarizer dynamically from `ctx`.
2. Let the summarizer distinguish whether the summary was triggered from a sync path or an async path.

The framework intentionally does not own a built-in request or trigger schema for
this. The example shows how to do it with your own `ctx` helpers and a custom
router summarizer.

## What it shows

- A business-defined `summaryRequest` is stored on `ctx`.
- A business-defined `summaryMode` (`sync` / `async`) is stored on `ctx`.
- A custom `routingSummarizer` implements:
  - `summary.SessionSummarizer`
  - `ShouldSummarizeWithContext(ctx, sess)`
- The router picks one of four concrete summarizers:
  - `vip-sync`
  - `vip-async`
  - `default-sync`
  - `default-async`

In production, those four concrete summarizers can be real
`summary.NewSummarizer(...)` instances with different prompts, models, or
checker combinations. In this example they are lightweight stubs so the demo
can run without an external model provider.

## Run

```bash
go run ./examples/summary/contextaware
```

Example output:

```text
== Sync summary ==
router=vip-sync tenant=vip scene=billing mode=sync ...

== Async summary ==
router=default-async tenant=standard scene=support mode=async ...
```

## Key idea

The current framework code will use `ShouldSummarizeWithContext(ctx, sess)` when
your summarizer provides it, even though the released `SessionSummarizer`
interface itself stays unchanged for compatibility.

That means the recommended pattern is:

1. Define your own request struct and async marker on `ctx`.
2. Implement a router summarizer that reads those values.
3. In sync paths, call `CreateSessionSummary` with the annotated `ctx`.
4. In async paths, call `EnqueueSummaryJob` with the annotated `ctx`.

If you override `EnqueueSummaryJob` in your own service wrapper, add the async
marker to `ctx` before delegating, and the same values will remain visible in
both the gate phase and the final `Summarize` phase.
