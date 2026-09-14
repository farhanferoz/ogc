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

### 5. Temperature Override Pointer Fix & Calibrated Sampling (`internal/config/config.go`, `internal/transformer/request.go`)
- **Problem**: `ModelConfig.Temperature` was originally defined as `float64`, and the override logic checked `if model.Temperature > 0`. When setting explicit custom temperatures in `config.json`, Go zero-value evaluation caused edge cases. Furthermore, setting `0.0` (greedy decoding) on frontier reasoning models (like `qwen3.8-max`, `glm-5.3`) caused them to get trapped in recursive self-debating loops.
- **Fix**: Changed `Temperature` to `*float64` across config and request structs and checked `if model.Temperature != nil`. Configured default temperature to `0.7` across model presets in `config.json` for natural reasoning without deterministic deliberation traps.

### 6. System Prompt Tool-Calling Discipline Directive (`internal/transformer/request.go`)
- **Problem**: Distilled/flash models frequently output conversational intent (e.g. *"Let me read the next section:"*) and end their turn with `<|im_end|>` rather than emitting structured OpenAI `tool_calls`, yielding the prompt back to the human.
- **Fix**: When `len(anthropicReq.Tools) > 0`, `ogc` automatically appends a targeted directive instructing the model to invoke the tool directly rather than outputting conversational narration without a tool payload.

### 7. Upfront Prompt Token Counting in `message_start` Streaming Event (`internal/token/counter.go`, `internal/transformer/stream.go`, `internal/handlers/messages.go`)
- **Problem**: Claude Code calculates its status line context percentage (`ctx: x%`) strictly from the `input_tokens` reported in the initial `message_start` SSE event. OpenAI-compatible streaming endpoints (e.g. OpenCode Go) do not report prompt token usage until the final stream chunk. Because `ogc` initialized streaming sessions with `Usage.InputTokens: 0` in `message_start`, Claude Code recorded `0` input tokens for every streaming assistant turn, causing the status line to oscillate between `0%` (after streaming turns) and `x%` (after non-streaming turns).
- **Fix**:
  - Implemented `CountRequest` on `token.Counter` to accurately tally system prompt, message history (including text, tool_use, tool_result, and thinking blocks), tool schemas, and framing tokens.
  - In `handleStreaming`, `ogc` calculates `inputTokens` upfront and populates `Usage.InputTokens` directly in the initial `message_start` SSE frame.
  - Enabled `stream_options: {"include_usage": true}` on OpenAI chat completion requests for backends supporting stream usage.

### 8. Native Anthropic `thinking` Block Streaming Transformation (`pkg/types/anthropic.go`, `internal/transformer/stream.go`)
- **Problem**: When reasoning models (Qwen 3.8 Max, GLM 5.3, DeepSeek) stream reasoning tokens (`delta.reasoning` / `delta.reasoning_content`), `ogc` previously merged reasoning deltas into regular `text_delta` blocks. This caused raw internal chain-of-thought monologue (*"Wait, let me reconsider...", "Hmm, actually..."*) to dump directly onto the terminal as visible chat text, confusing the user and re-injecting internal thoughts back into the conversation history on subsequent turns.
- **Fix**:
  - Implemented stateful `emitThinking` in `streamSession` that emits proper `content_block_start` with `type: "thinking"` and `thinking_delta` events.
  - Emits a `signature_delta` (`proxy-thinking-placeholder`) before `content_block_stop` to match Anthropic's thinking-block event shape. (An earlier version of this note said Claude Code discards unsigned thinking blocks and reports "no visible output"; session transcripts show unsigned thinking blocks recorded normally, so that was not the cause — see §10.)
  - Automatically closes thinking blocks before regular text or tool call blocks begin, and ensures a visible text block exists on turn close.
  - Claude Code now cleanly captures raw reasoning inside its native collapsible thinking spinner, keeping visible chat and conversation history clean.

### 9. OpenCode Go Session Routing and Identification (`internal/client/opencode.go`, `internal/handlers/messages.go`)
- **Problem**: OpenCode Go introduced an upstream requirement enforcing `x-opencode-session` header routing (`MissingSessionID: Request is missing x-opencode-session and cannot be routed efficiently`) and agent client identification. Missing these headers triggered immediate `HTTP 400` errors.
- **Fix**:
  - Automatically identifies client upstream via `User-Agent: ogc/1.0 (Claude Code)`.
  - Captures incoming session IDs or deterministically derives a stable conversation session hash from message context, injecting `x-opencode-session` on both OpenAI and Anthropic upstream endpoints.

### 10. Native `/v1/messages` Passthrough and No More Silent Empty Turns (`internal/transformer/stream.go`, `internal/transformer/collect.go`, `internal/handlers/messages.go`, `pkg/types/openai.go`, config)
- **Problem**: Claude Code turns ended with nothing on screen (`[Your previous response had no visible output…]`) while `ogc` logged `streaming completed`. Every model was routed through OpenAI translation, which rebuilds Anthropic blocks from chat-completion chunks and closed any stream that simply ended — including one with no `finish_reason` or one carrying an `error` object — as a normal, empty message. The raw upstream bytes of the failing turns were not logged, so which of those translation failure modes fired is not pinned down.
- **Fix**:
  - OpenCode Go serves many models on its Anthropic-format `/v1/messages` endpoint — Claude Code's own protocol, with real thinking signatures handled upstream. Those models are configured `"provider": "anthropic"` and relayed unchanged. Probed 2026-09-13 with a streaming tool request: deepseek-v4-flash, deepseek-v4-pro, kimi-k3, minimax-m2.5/m2.7/m3, qwen3.6-plus/3.7-plus/3.7-max/3.8-max work natively (a follow-up turn sending back signed, unsigned or placeholder-signed thinking blocks is accepted). glm-*, kimi-k2.x, mimo-*, hy3 return HTTP 500 there and stay on `"openai"`.
  - The passthrough relay writes whole SSE events through `SSEWriter`. It previously `io.Copy`'d straight to the socket while the heartbeat goroutine wrote pings, so a ping could split an upstream event.
  - A relayed stream that ends before `message_stop`, or a translated stream that ends without `finish_reason` or carries an `error` object, now surfaces as an error event instead of an empty completed message.
  - Non-streaming passthrough keeps `signature_delta`, and Claude Code's `X-Claude-Code-Session-Id` is forwarded as the OpenCode session.

