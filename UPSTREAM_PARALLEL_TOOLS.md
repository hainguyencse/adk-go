Parallel tools and progressive SSE upstream port

Reviewed the local official repository at `74faa9ab6bf58b47653ed275f4aa86a93e76280a` against this fork at
`08f718cf5eae7fe81afd36ae957ae8dab0f10f26`. The port is a selective backport to the fork's
`google.golang.org/adk` module and existing invocation/tool context APIs.

`platform/exec.go` has a single upstream implementation commit, `465fe1b3`.
It builds on the parallel execution introduced by `81dfe5eb`; copying that
platform file alone would leave this fork executing tools sequentially.
The table records the feature changes, directly related fixes and regression tests
included in this working-tree change. Paths without a package prefix are under
`internal/llminternal/`.

| Upstream commit | Behavior included | Files affected in this port |
| --- | --- | --- |
| [81dfe5eb](https://github.com/google/adk-go/commit/81dfe5eb5d23d5ea5d48d423a90a23b8a051df0d) | Parallel tool execution, ordered response slots, partial-call guard, progressive SSE aggregation. | `base_flow.go`, `stream_aggregator.go`, `converters/converters.go`; mock runner, agenttool expectations, async/replay tests and three recordings. |
| [68be1e5c](https://github.com/google/adk-go/commit/68be1e5cc6b03814177602c80b32b5c055862260) | Preserve streaming errors and finish reason. | `stream_aggregator.go` and streaming/replay tests. |
| [83692605](https://github.com/google/adk-go/commit/8369260531b706978d5200feb4ab1d80743c4d5e) | Carry thought signatures to the first function call in mixed responses. | `stream_aggregator.go` and tests. |
| [853c11b6](https://github.com/google/adk-go/commit/853c11b6dbbbde0c7d99f06ff6c17ab0b94dbf3d) | Exercise multiple tool confirmations together. | `parallel_function_call_hitl_test.go`, taken at `a7091922` for corrected event order. |
| [50a84c00](https://github.com/google/adk-go/commit/50a84c000e5022a86d78d22e094a111ec5fcd0ed) | Preserve thought signatures on generated confirmation requests. | `functions.go` and tests. |
| [a7091922](https://github.com/google/adk-go/commit/a7091922855391e2ea73943fbaf041d787af7fc2) | Emit completed tool responses before confirmation requests. | `base_flow.go`; HITL, functiontool and MCP confirmation test expectations. |
| [2472d61f](https://github.com/google/adk-go/commit/2472d61f7f05e27056101711b0a741c7bbc3e90c) | Accept metadata-only SSE chunks. | `stream_aggregator.go`, `converters/converters.go` and tests. |
| [465fe1b3](https://github.com/google/adk-go/commit/465fe1b30a1de794d0f3efd579a95eacefad7395) | Add TaskRunner, WithTaskRunner and RunTasks; route tool fan-out through the runner. | `platform/exec.go`, `platform/exec_test.go`, `base_flow.go`. |
| [2a04f92f](https://github.com/google/adk-go/commit/2a04f92f17766207279a9a15dee3c73f3fb9e6d4) | Emit confirmations in model-call order. | `functions.go` and tests. |
| [9eb29316](https://github.com/google/adk-go/commit/9eb29316f3b0c20eba91ade9863b948d0ddbd618) | Resume confirmed calls in request order, once per original call ID. | `request_confirmation_processor.go` and tests. |
| [e8958c4f](https://github.com/google/adk-go/commit/e8958c4fa6e2b08f6a28d3b847d21d3ffcba8000) | Retain usage metadata when later chunks omit it. | `stream_aggregator.go` and tests. |
| [685d0af0](https://github.com/google/adk-go/commit/685d0af03743320b52107c20c7039e881697a1bb) | Merge artifact deltas from all tool responses, preferring higher versions. | `base_flow.go` and merge tests. |
| [d6bdb0d2](https://github.com/google/adk-go/commit/d6bdb0d2e26944e7d9c39178e194cc7b4b9d8ea8) | Assemble streamed arrays and quoted JSON-path members; bound array indices. | `stream_aggregator.go` and tests. |
| [c71f49dc](https://github.com/google/adk-go/commit/c71f49dcc90aa57a2b937529d28537941a42306f) | Preserve empty text fields on signature-only history parts sent to Gemini. | `model/gemini/gemini.go`, its tests and the updated Gemini 3.1 replay recording. Provider helper adapted without continuation support. |
| [1862aaea](https://github.com/google/adk-go/commit/1862aaea60724b16784a55c01d51edadb2629470) | Handle a nil first streaming candidate. | `stream_aggregator.go` and tests. |

The streaming aggregator and converter match the reviewed upstream versions,
with imports adapted to the fork's module path. Existing fork live-session
handling, tool result caching, cache-hit metadata, and deferred long-running
responses remain in the tool execution path. The added `task_runner_test.go`
checks those integration points, custom task contexts, concurrent startup,
response order and action merging.

The upstream V2 module migration (`893e4a40`), context unification
(`d19c1670`, `81a63d8f`), and clock/UUID platform seams (`c6f168f5`) are
not dependencies of this port and were not copied. The platform documentation
omits references to clock/UUID APIs that this fork does not expose. No module
or dependency versions changed. The Gemini fix uses APIs already present in
this fork's genai v1.70.0. Test doubles embed the fork's model interface to
satisfy its additional live `Connect` method.

Tools in a single completed model response now execute concurrently by default.
Responses and merged event actions follow model-call order, regardless of
completion order. Partial streamed calls are displayed but execute only after
aggregation. Tool implementations and callbacks must support concurrent calls.
The fork's cache still reuses completed results; it does not coalesce identical
calls that overlap in the same parallel batch.

A caller can choose sequential execution by installing a runner on the context
passed to `runner.Run` (or `RunLive`):

```go
ctx = platform.WithTaskRunner(ctx, func(ctx context.Context, tasks []func(context.Context)) {
    for _, task := range tasks {
        task(ctx)
    }
})
```

Custom runners may bound concurrency or derive task contexts. Every task must
run exactly once. `platform.RunTasks` waits for all wrapped tasks to finish,
including when the installed runner dispatches asynchronously and returns early.

Validation:

- `go build ./...` passes.
- `go test -race -count=1 ./platform ./internal/llminternal/... ./tool/... ./runner ./model/gemini` passes, including all three upstream Gemini HTTP replay recordings.
- `go test -race -count=1 ./...` reports only the four pre-existing `TestInstructionProvider` subcase failures in `agent/llmagent`: expected requests omit the fork's `LiveConnectConfig`. The same four failed before this port. No races were reported.
- Selected new regression tests fail against the original production implementations in a separate temporary copy: task runner integration, partial-call guard, progressive arguments, array assembly, metadata-only chunks, artifact merging, confirmation ordering/resumption/signatures, and empty-text Gemini serialization.
- `go vet ./platform ./internal/llminternal/... ./model/gemini` passes. Staticcheck reports only the existing unused `liveResult.reconnect` field in `base_flow.go`; it is also present at the fork's original HEAD. `golangci-lint` is not installed.
- `go mod tidy -diff` reports existing `go.sum` drift. The same diff occurs on an untouched archive of the fork's HEAD. `go.mod` and `go.sum` are unchanged by this port.
- Changed Go files were formatted with gofmt and goimports. `git diff --check` passes.

Validation uses replay recordings and local mocks; no live model requests were made.
Changes are left uncommitted for review.
