# Local Patches Applied to OGC

This local build of `ogc` includes critical fixes for high-reliability Claude Code agentic sessions:

### 1. Tool Call Message Ordering & Interrupted Tool Call Recovery (`internal/transformer/request.go`)
- **Problem**: When a user interrupted a tool call (e.g. `Ctrl+C` / stop) or when user text accompanied tool results, the converted message array violated OpenAI API specifications (`An assistant message with 'tool_calls' must be followed by tool messages responding to each 'tool_call_id'`).
- **Fix**: 
  - `fixToolMessageOrdering` inspects all `assistant` messages with `tool_calls`.
  - Ensures tool messages strictly follow the assistant message immediately.
  - Automatically synthesizes `role: "tool", content: "[Operation interrupted by user]"` responses for any dangling tool calls from interrupted turns.
  - Appends user prompt text after all tool response blocks.

### 2. Thread-Safe `SSEWriter` with Continuous Heartbeat / Ping Keepalive (`internal/transformer/stream.go`, `internal/handlers/messages.go`)
- **Problem**: On large contexts (100k+ tokens) or with complex toolchains, upstream model prefill (TTFT) takes 30–70s before any response chunks arrive. Without streaming events on the socket, Claude Code's client-side request timeout aborts the connection and enters an exponential retry backoff loop (`Waiting for API response · will retry in Xm Ys · check your network`). Previous attempts at raw unmutexed background heartbeats suffered data races that corrupted SSE framing.
- **Fix**:
  - Implemented `SSEWriter` with internal `sync.Mutex` synchronization to guarantee thread-safe marshaling and writing of SSE events.
  - Fires an initial `event: ping\ndata: {"type":"ping"}\n\n` immediately upon HTTP connection and maintains a synchronized 2.5s ping ticker during upstream prefill and generation lulls.
  - Claude Code receives valid SSE ping events continuously, keeping the connection alive during long prefill times.

### 3. GLM & DeepSeek `reasoning_content` Streaming Delta Support (`pkg/types/openai.go`, `internal/transformer/stream.go`, `internal/transformer/collect.go`)
- **Problem**: Frontier reasoning models like `glm-5.3` stream their thinking tokens under `delta.reasoning_content` rather than `delta.reasoning` or `delta.content`. Because `reasoning_content` was unparsed, `ogc` discarded all thinking tokens during the 30–60s thinking phase. Claude Code received 0 bytes on the stream socket, timed out after ~30s (`client disconnected during stream`), and fell back to a duplicate non-streaming request that took another minute, causing the UI to hang on "Waiting for API response...".
- **Fix**: Added `reasoning_content` struct tagging and mapped it to streaming text deltas so Claude Code receives tokens immediately from turn start, keeping streaming alive with zero client timeouts.

### 4. Tool Call `Index` Parsing in Streaming Delta (`pkg/types/openai.go`, `internal/transformer/stream.go`)
- **Problem**: `ToolCall` was missing the `Index *int` field in `pkg/types/openai.go`. When upstream emitted multiple parallel tool calls across chunks, chunks for subsequent tool calls were erroneously attributed to tool index 0, merging separate tool calls into corrupted single blocks.
- **Fix**: Added `Index *int` to `ToolCall` and updated `processSSELine` to use `*tc.Index` for correct parallel tool tracking.

### 5. Temperature Override Bug Fix & Greedy Decoding Support (`internal/config/config.go`, `internal/transformer/request.go`)
- **Problem**: `ModelConfig.Temperature` was defined as `float64`, and the override logic checked `if model.Temperature > 0`. When setting `"temperature": 0.0` in `config.json` for deterministic tool execution, Go evaluated `0.0 > 0` as `false`, silently ignoring the override and falling back to the upstream provider's `0.7`/`1.0` default. This caused conversational drift, hallucinated code blocks, and dropped tool calls in distilled/flash models (`deepseek-v4-flash`).
- **Fix**: Changed `Temperature` to `*float64` across config and request structs and checked `if model.Temperature != nil`. All model presets in `config.json` updated to `0.0` for deterministic greedy tool calling.

### 6. System Prompt Tool-Calling Discipline Directive (`internal/transformer/request.go`)
- **Problem**: Distilled/flash models frequently output conversational intent (e.g. *"Let me read the next section:"*) and end their turn with `<|im_end|>` rather than emitting structured OpenAI `tool_calls`, yielding the prompt back to the human.
- **Fix**: When `len(anthropicReq.Tools) > 0`, `ogc` automatically appends a targeted directive instructing the model to invoke the tool directly rather than outputting conversational narration without a tool payload.
