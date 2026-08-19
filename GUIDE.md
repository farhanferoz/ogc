# OpenCode Models Integration with Claude Code — Comprehensive Optimization & Setup Guide

A production-tested, self-contained guide for integrating, optimizing, and operating **OpenCode Go / OpenCode Zen** frontier models (GLM 5.3, Qwen 3.8 Max, DeepSeek V4 Pro/Flash, Kimi K3, MiniMax M3, etc.) seamlessly within **Claude Code** and **ccage**.

---

## 1. Architectural Overview

Claude Code natively speaks the **Anthropic Messages API** (with streaming Server-Sent Events, thinking blocks, and tool use content blocks). OpenCode Go provides OpenAI-compatible `/v1/chat/completions` endpoints.

The integration uses a high-performance local proxy daemon (`ogc`) running on `http://127.0.0.1:3456` that performs zero-buffering, bidirectional translation between the two protocols:

```
┌──────────────────────────────────────────────────────────────────────────────────┐
│                                 HOST ENVIRONMENT                                 │
│                                                                                  │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │                              Claude Code (CLI)                             │  │
│  │   • Configured with: ANTHROPIC_BASE_URL=http://127.0.0.1:3456              │  │
│  │   • Context Ceiling: CLAUDE_CODE_MAX_CONTEXT_TOKENS (per active model)     │  │
│  │   • Statusline Hook: ~/.claude/statusline-command.sh (true token ratios)   │  │
│  └──────────────────────────────────────┬─────────────────────────────────────┘  │
│                                         │ Anthropic Messages API (SSE Stream)    │
│                                         ▼                                        │
│  ┌────────────────────────────────────────────────────────────────────────────┐  │
│  │                            ogc (Local Go Proxy)                            │  │
│  │   • Stateful Anthropic SSE Stream Session (Text, Reasoning, Tool Calls)    │  │
│  │   • Message Ordering Sanitizer & Interrupted Tool Call Recovery            │  │
│  │   • Zero-buffering tight-loop I/O with immediate flush                     │  │
│  └──────────────────────────────────────┬─────────────────────────────────────┘  │
│                                         │ OpenAI Chat Completions API            │
└─────────────────────────────────────────┼────────────────────────────────────────┘
                                          ▼ HTTPS (TLS)
┌──────────────────────────────────────────────────────────────────────────────────┐
│                   OpenCode Go Upstream (https://opencode.ai/zen/go/v1)           │
│   • 26 Available Frontier & Specialist Models (GLM 5.3, Qwen, DeepSeek, etc.)     │
│   • Flat-rate unlimited tier ($10/mo)                                            │
└──────────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Step-by-Step Setup Guide

### Step 2.1: Build and Install the `ogc` Proxy

1. Clone or navigate to the `ogc` repository (located at `~/dev/opencode`):
   ```bash
   cd ~/dev/opencode
   make test
   make install
   ```
   *Expected binary location:* `~/go/bin/ogc` (or `/usr/local/bin/ogc`).

2. Verify compilation:
   ```bash
   ogc --help
   ```

---

### Step 2.2: Configure Upstream API & Models

1. Create the configuration directory:
   ```bash
   mkdir -p ~/.config/ogc
   ```

2. Store your OpenCode Go API key in `~/.config/ogc/.env`:
   ```bash
   cat <<'ENV' > ~/.config/ogc/.env
   OGC_API_KEY="sk-your-opencode-go-api-key"
   ENV
   chmod 600 ~/.config/ogc/.env
   ```

3. Create the model registry in `~/.config/ogc/config.json`:
   ```json
   {
     "api_key": "${OGC_API_KEY}",
     "host": "127.0.0.1",
     "port": 3456,
     "models": {
       "qwen3.8-max": { "provider": "openai", "model_id": "qwen3.8-max", "temperature": 0.7, "max_tokens": 65536 },
       "qwen3.7-max": { "provider": "openai", "model_id": "qwen3.7-max", "temperature": 0.7, "max_tokens": 65536 },
       "qwen3.7-plus": { "provider": "openai", "model_id": "qwen3.7-plus", "temperature": 0.7, "max_tokens": 65536 },
       "qwen3.6-plus": { "provider": "openai", "model_id": "qwen3.6-plus", "temperature": 0.7, "max_tokens": 65536 },
       "qwen3.5-plus": { "provider": "openai", "model_id": "qwen3.5-plus", "temperature": 0.7, "max_tokens": 65536 },
       "deepseek-v4-pro": { "provider": "openai", "model_id": "deepseek-v4-pro", "temperature": 0.7, "max_tokens": 65536 },
       "deepseek-v4-flash": { "provider": "openai", "model_id": "deepseek-v4-flash", "temperature": 0.7, "max_tokens": 65536 },
       "glm-5.3": { "provider": "openai", "model_id": "glm-5.3", "temperature": 0.7, "max_tokens": 65536 },
       "glm-5.2": { "provider": "openai", "model_id": "glm-5.2", "temperature": 0.7, "max_tokens": 65536 },
       "glm-5.1": { "provider": "openai", "model_id": "glm-5.1", "temperature": 0.7, "max_tokens": 65536 },
       "glm-5": { "provider": "openai", "model_id": "glm-5", "temperature": 0.7, "max_tokens": 65536 },
       "kimi-k3": { "provider": "openai", "model_id": "kimi-k3", "temperature": 0.7, "max_tokens": 65536 },
       "kimi-k2.7-code": { "provider": "openai", "model_id": "kimi-k2.7-code", "temperature": 0.7, "max_tokens": 65536 },
       "kimi-k2.6": { "provider": "openai", "model_id": "kimi-k2.6", "temperature": 0.7, "max_tokens": 65536 },
       "kimi-k2.5": { "provider": "openai", "model_id": "kimi-k2.5", "temperature": 0.7, "max_tokens": 65536 },
       "minimax-m3": { "provider": "openai", "model_id": "minimax-m3", "temperature": 0.7, "max_tokens": 65536 },
       "minimax-m2.7": { "provider": "openai", "model_id": "minimax-m2.7", "temperature": 0.7, "max_tokens": 65536 },
       "minimax-m2.5": { "provider": "openai", "model_id": "minimax-m2.5", "temperature": 0.7, "max_tokens": 65536 },
       "mimo-v2.5-pro": { "provider": "openai", "model_id": "mimo-v2.5-pro", "temperature": 0.7, "max_tokens": 65536 },
       "mimo-v2.5": { "provider": "openai", "model_id": "mimo-v2.5", "temperature": 0.7, "max_tokens": 65536 },
       "mimo-v2-pro": { "provider": "openai", "model_id": "mimo-v2-pro", "temperature": 0.7, "max_tokens": 65536 },
       "mimo-v2-omni": { "provider": "openai", "model_id": "mimo-v2-omni", "temperature": 0.7, "max_tokens": 65536 },
       "grok-4.5": { "provider": "openai", "model_id": "grok-4.5", "temperature": 0.7, "max_tokens": 65536 },
       "gpt-5.6-luna": { "provider": "openai", "model_id": "gpt-5.6-luna", "temperature": 0.7, "max_tokens": 65536 },
       "hy3": { "provider": "openai", "model_id": "hy3", "temperature": 0.7, "max_tokens": 65536 },
       "hy3-preview": { "provider": "openai", "model_id": "hy3-preview", "temperature": 0.7, "max_tokens": 65536 }
     },
     "upstream": {
       "base_url": "https://opencode.ai/zen/go/v1",
       "anthropic_base_url": "https://opencode.ai/zen/go/v1",
       "timeout_ms": 300000
     },
     "logging": {
       "level": "info",
       "requests": true
     }
   }
   ```

---

### Step 2.3: Install Systemd User Service

Run `ogc` as a background daemon managed by systemd:

```bash
mkdir -p ~/.config/systemd/user
cat <<'SERVICE' > ~/.config/systemd/user/ogc.service
[Unit]
Description=OGC - OpenCode Go Proxy for Claude Code
After=network.target

[Service]
Type=simple
ExecStart=%h/go/bin/ogc serve
Restart=always
RestartSec=3
Environment=PATH=/usr/local/bin:/usr/bin:/bin:%h/go/bin

[Install]
WantedBy=default.target
SERVICE

systemctl --user daemon-reload
systemctl --user enable --now ogc.service
```

Verify service is active:
```bash
systemctl --user status ogc.service
```

---

### Step 2.4: Configure Shell Launchers & Model Selectors

Add the model context resolver and interactive launcher to `~/.bashrc.d/claude-overrides.sh` (or `~/.bashrc`):

```bash
# Model context window resolver
_opencode_model_context_limit() {
    local m="${1##*/}"
    m="${m##*@cf/*/}"
    m=$(echo "$m" | tr '[:upper:]' '[:lower:]' | tr ' ' '-')
    case "$m" in
        glm-5.3|glm-5.2|qwen3.8-max|qwen3.7-max|qwen3.7-plus|qwen3.6-plus|deepseek-v4-pro|deepseek-v4-flash|minimax-m3|mimo-v2.5)
            echo 1000000 ;;
        kimi-k3|mimo-v2.5-pro|mimo-v2-pro)
            echo 1048576 ;;
        gpt-5.6-luna)
            echo 1050000 ;;
        grok-4.5)
            echo 500000 ;;
        qwen3.5-plus|kimi-k2.7-code|kimi-k2.6|kimi-k2.5|mimo-v2-omni)
            echo 262144 ;;
        hy3|hy3-preview)
            echo 256000 ;;
        glm-5.1|glm-5)
            echo 202752 ;;
        minimax-m2.7|minimax-m2.5)
            echo 204800 ;;
        *)
            echo 200000 ;;
    esac
}

# Interactive model selector
_opencode_select_model() {
    local models=(
        "qwen3.8-max" "qwen3.7-max" "qwen3.7-plus" "qwen3.6-plus" "qwen3.5-plus"
        "deepseek-v4-pro" "deepseek-v4-flash" "glm-5.3" "glm-5.2" "glm-5.1" "glm-5"
        "kimi-k3" "kimi-k2.7-code" "kimi-k2.6" "kimi-k2.5" "minimax-m3" "minimax-m2.7"
        "minimax-m2.5" "mimo-v2.5-pro" "mimo-v2.5" "mimo-v2-pro" "mimo-v2-omni"
        "grok-4.5" "gpt-5.6-luna" "hy3" "hy3-preview"
    )

    if [ ! -t 0 ] && [ ! -t 1 ]; then
        echo "${models[0]}"; return 0
    fi

    printf '\n\033[1;36mSelect OpenCode Go Model (%d available):\033[0m\n' "${#models[@]}" >&2
    local i
    for ((i=0; i<${#models[@]}; i++)); do
        local num=$((i+1))
        local tag=""
        [ "$num" -eq 1 ] && tag=" \033[33m(default)\033[0m"
        printf '  \033[1m%2d)\033[0m \033[32m%-18s\033[0m%b\n' "$num" "${models[i]}" "$tag" >&2
    done
    printf '\n' >&2

    local choice=""
    printf '\033[1mEnter choice [1-%d, Enter for 1, q to cancel]: \033[0m' "${#models[@]}" >&2
    read -r choice < /dev/tty || return 1
    case "$choice" in
        q|Q) return 1 ;;
        "") choice=1 ;;
    esac

    if [[ "$choice" =~ ^[0-9]+$ ]] && [ "$choice" -ge 1 ] && [ "$choice" -le "${#models[@]}" ]; then
        printf '%s\n' "${models[$((choice-1))]}"
        return 0
    fi
    return 1
}

# Main wrapper
opencode-claude() {
    local extra_flags=()
    local has_model=0
    local model_name=""
    local prev_arg=""

    for arg in "$@"; do
        if [ "$prev_arg" = "--model" ]; then
            model_name="$arg"; has_model=1; prev_arg=""; continue
        fi
        case "$arg" in
            --model=*) has_model=1; model_name="${arg#--model=}" ;;
            --model) has_model=1; prev_arg="--model" ;;
        esac
    done

    if [ "$has_model" -eq 0 ]; then
        local selected_model
        selected_model=$(_opencode_select_model "$@") || return $?
        model_name="$selected_model"
        extra_flags+=(--model "$selected_model")
    fi

    local ctx_limit
    ctx_limit=$(_opencode_model_context_limit "$model_name")
    export CLAUDE_CODE_MAX_CONTEXT_TOKENS="$ctx_limit"

    OPENCODE=1 claude "${extra_flags[@]}" "$@"
}

alias opencode-ccage-yolo="opencode-claude --dangerously-skip-permissions"
```

---

### Step 2.5: Configure Status Line & Token Normalization

In `~/.claude/statusline-command.sh`, add model capacity resolution and percentage recalculation:

```bash
# Lookup true context window size for known models
get_model_context_size() {
  local m="${1##*/}"
  m="${m##*@cf/*/}"
  m=$(echo "$m" | tr '[:upper:]' '[:lower:]' | tr ' ' '-')
  case "$m" in
    glm-5.3|glm-5.2|qwen3.8-max|qwen3.7-max|qwen3.7-plus|qwen3.6-plus|deepseek-v4-pro|deepseek-v4-flash|minimax-m3|mimo-v2.5)
      echo 1000000 ;;
    kimi-k3|mimo-v2.5-pro|mimo-v2-pro)
      echo 1048576 ;;
    gpt-5.6-luna)
      echo 1050000 ;;
    grok-4.5)
      echo 500000 ;;
    qwen3.5-plus|kimi-k2.7-code|kimi-k2.6|kimi-k2.5|mimo-v2-omni)
      echo 262144 ;;
    hy3|hy3-preview)
      echo 256000 ;;
    glm-5.1|glm-5)
      echo 202752 ;;
    minimax-m2.7|minimax-m2.5)
      echo 204800 ;;
    *)
      echo "" ;;
  esac
}

humanize() {
  awk -v n="$1" 'BEGIN {
    if (n >= 1000000) {
      val = n / 1000000;
      if (val >= 0.99 && val <= 1.05) printf "1M";
      else if (val == int(val)) printf "%dM", val;
      else printf "%.1fM", val;
    } else if (n >= 1000) {
      val = n / 1000;
      if (val == int(val)) printf "%dk", val;
      else if (val >= 100) printf "%.0fk", val;
      else printf "%.1fk", val;
    } else {
      printf "%d", n;
    }
  }'
}

# Inside status line context calculation:
if [ -n "$CTX" ]; then
  MODEL_ID=$(echo "$input" | jq -r '.model.id // .model.display_name // empty')
  KNOWN_CTX_SIZE=$(get_model_context_size "${MODEL_ID:-$MODEL}")
  if [ -n "$KNOWN_CTX_SIZE" ] && [ "$KNOWN_CTX_SIZE" -ne "${CTX_SIZE:-0}" ]; then
    if [ -n "$CTX_SIZE" ] && [ "$CTX_SIZE" -gt 0 ]; then
      USED_TOK=$(awk -v p="$CTX" -v s="$CTX_SIZE" 'BEGIN { printf "%d", (p/100)*s }')
      CTX=$(awk -v u="$USED_TOK" -v k="$KNOWN_CTX_SIZE" 'BEGIN { printf "%.1f", (u/k)*100 }')
    fi
    CTX_SIZE="$KNOWN_CTX_SIZE"
  elif [ -n "$CTX_SIZE" ]; then
    USED_TOK=$(awk -v p="$CTX" -v s="$CTX_SIZE" 'BEGIN { printf "%d", (p/100)*s }')
  fi

  CTX_SEG="ctx: $(printf '%.0f' "$CTX")%"
  if [ -n "$CTX_SIZE" ] && [ -n "$USED_TOK" ]; then
    CTX_SEG="$CTX_SEG ($(humanize "$USED_TOK")/$(humanize "$CTX_SIZE"))"
  fi
  LIMITS="$CTX_SEG"
fi
```

---

## 3. Core Protocol Patches & Optimizations in `ogc`

The following 6 protocol patches in `~/dev/opencode` ensure robust, hang-free Claude Code execution against OpenCode Go frontier models:

### Patch 1: Thread-Safe `SSEWriter` with Continuous Heartbeat / Ping Keepalive (`internal/transformer/stream.go`, `internal/handlers/messages.go`)
* **Problem:** On large contexts (100k+ tokens) with full toolchains, upstream prompt evaluation (TTFT) takes 30–70s before any token chunks arrive. Without data on the socket, Claude Code's stream watchdog times out and enters exponential backoff retry loops (`Waiting for API response · will retry in Xm Ys · check your network`). Raw unmutexed background heartbeats suffered TCP chunk data races that corrupted SSE framing.
* **Solution:** Implement `SSEWriter` with internal `sync.Mutex` synchronization. Immediately dispatch an initial `event: ping\ndata: {"type":"ping"}\n\n` upon connection, and maintain a synchronized 2.5s ping ticker during upstream prefill and generation lulls. Claude Code receives continuous valid Anthropic ping frames, resetting client idle timers with zero race conditions.

### Patch 2: Stateful Anthropic SSE Stream Session for Tool Calls (`internal/transformer/stream.go`)
* **Problem:** When OpenAI streams tool calls across multiple chunks, emitting a new `content_block_start` per chunk with `delta` corrupts Anthropic's SSE stream and causes Claude Code to abort.
* **Solution:** Implement a stateful `streamSession`:
  1. Emit exactly ONE `content_block_start` per tool call with `content_block: {"type":"tool_use", "id": "...", "name": "..."}`.
  2. Emit `content_block_delta` with `delta: {"type":"input_json_delta", "partial_json": "..."}` for argument chunks.
  3. Emit `content_block_stop` for each active tool block when finished.

### Patch 3: Tool Call `Index` Tracking in Streaming Deltas (`pkg/types/openai.go`, `internal/transformer/stream.go`)
* **Problem:** OpenAI chunked streaming sends `tool_calls[i]` with an `index` property. Without parsing `Index *int`, parallel tool calls streamed in separate chunks collapsed into index 0, corrupting multi-tool calls.
* **Solution:** Add `Index *int` to `ToolCall` and dynamically resolve `*tc.Index` in `streamSession` so parallel tool calls retain distinct block identities.

### Patch 4: Robust Full JSON Unmarshaling Over Fragile Substring Slicing (`internal/transformer/stream.go`)
* **Problem:** Naive substring fast-paths looking for `"delta":{"content":"` fail when text tokens contain escaped quotes (`\"`), backslashes, or code blocks, truncating content and double-escaping newlines.
* **Solution:** Removed manual string slicing in favor of standard `json.Unmarshal`, ensuring complete fidelity for complex code blocks, JSON payloads, and multiline responses.

### Patch 5: Real-Time Reasoning & Thinking Delta Streaming (`pkg/types/openai.go`, `internal/transformer/stream.go`, `internal/transformer/collect.go`)
* **Problem:** Frontier models like `glm-5.3` and DeepSeek stream thinking tokens under `delta.reasoning_content` (OpenAI extended format). If unparsed, thinking tokens are dropped, causing long silent periods that trigger client timeouts.
* **Solution:** Map `delta.reasoning_content` to Anthropic `content_block_delta` (`text_delta`) so tokens stream immediately from the first millisecond of generation.

### Patch 6: Strict Tool Message Ordering & Interruption Recovery (`internal/transformer/request.go`)
* **Problem:** OpenAI APIs reject requests if an `assistant` message with `tool_calls` is not immediately followed by `tool` role messages responding to each `tool_call_id`. If a user interrupts mid-turn (`Ctrl+C`), dangling tool calls cause perpetual 400 Bad Request loops.
* **Solution:** `fixToolMessageOrdering` inspects all assistant tool calls and synthesizes `role: "tool", content: "[Operation interrupted by user]"` for any missing responses.

---

## 4. Model Selection & Routing Playbook

| Category | Model Name | Upstream Context | Recommended Use Case |
| :--- | :--- | :--- | :--- |
| **Frontier Reasoning & Architecture** | `glm-5.3`<br>`qwen3.8-max`<br>`deepseek-v4-pro` | **1,000,000 (1M)** | Autonomous multi-stage planning, heavy debugging, large refactors across many files. |
| **High-Velocity Coding (Fast)** | `deepseek-v4-flash`<br>`qwen3.7-plus`<br>`minimax-m3` | **1,000,000 (1M)** | Rapid iterative coding, test suites, mechanical edits. Sub-second TTFT. |
| **Code Specialization** | `kimi-k2.7-code`<br>`kimi-k3` | **262k / 1M** | Deep syntax adherence (Rust, Python, C++). |
| **Lightweight Subagents** | `deepseek-v4-flash`<br>`minimax-m3` | **1,000,000 (1M)** | Parallel worker tasks dispatched by the orchestrator. |

---

## 5. Operating Best Practices, Context Management & Pitfalls

### 5.1 Context Limits & Auto-Compaction Realities

1. **Claude Code Auto-Compact Ceiling vs. 1M Display:**
   * While `statusline-command.sh` and model definitions display the full 1M context capacity (e.g. `ctx: 16% (164k/1M)`), Claude Code's internal compaction subsystem calculates its auto-compaction trigger based on standard 200k token architecture ($\approx 84–85\% \approx 168\text{k tokens}$).
   * As your session approaches 164k–168k tokens, the status bar will show `2% until auto-compact` despite only utilizing 16% of the 1M ceiling.

2. **The 160k+ Token API Error Wall:**
   * OpenCode Go backends evaluate the full prompt history turn-by-turn without persistent server-side KV caching.
   * At $>160\text{k tokens}$, every tool invocation sends a massive 160k+ token payload.
   * This frequently triggers `* API error · Retrying...` due to:
     * **TPM (Tokens Per Minute) exhaustion:** 1–2 turns immediately breach rate limits.
     * **Gateway / TTFT timeouts:** Upstream evaluation time exceeds HTTP client timeouts (60–120s).
     * **Upstream payload caps:** Some provider backends enforce hard 128k or 160k input limits.

3. **Rule of Thumb:** Proactively run `/compact` or `/clear` to maintain active context under **40,000–50,000 tokens**.

---

### 5.2 The `-c` (Continue) Bloat Trap & Flash Compaction Loops

When switching models or resuming work, beware of the **`-c` compaction loop trap**:

1. **The Trap:**
   * Running `opencode-ccage-yolo -c` reloads the full, uncompressed session history from disk.
   * If the previous session reached $\ge 168\text{k tokens}$, Claude Code immediately enters **auto-compaction on startup** (`0% until auto-compact`).
   * If you launched with a fast/flash model (such as `deepseek-v4-flash`), the lightweight model will struggle to summarize the dense 180k token history. It will take 40+ seconds and only shave off a negligible amount (e.g. `↓ 424 tokens` / $<0.3\%$).
   * Because token count remains above the compaction threshold, the session becomes permanently stuck in an auto-compaction and API retry loop.

2. **Safe Model Switching Protocol:**
   * **Compact before exit:** If you want to switch from a reasoning model (`glm-5.3`) to a fast model (`deepseek-v4-flash`) while retaining history, run `/compact` **before** `/exit` so the capable model performs the summarization.
   * **Prefer fresh starts without `-c`:** For new subtasks, launch fresh without `-c` and provide a concise 1-sentence prompt pointing the model to the target files:
     ```bash
     opencode-ccage-yolo --model deepseek-v4-flash
     # Prompt: "Continue converting Model inputs in /path/to/template.html and verify syntax."
     ```
   * **Recovery:** If caught in a compaction loop, press `Ctrl+C`, exit, and relaunch without `-c`.

---

### 5.3 Reasoning Effort Tuning

* **Rapid Iteration & Mechanical Edits:** `--effort medium` or `--effort low` (sub-second TTFT).
* **Architecture, Multi-File Planning & Hard Debugging:** `--effort high` or `--effort max`.

---

## 6. Verification & Health Check Commands

```bash
# 1. Check proxy daemon status
systemctl --user status ogc.service

# 2. View live proxy logs
journalctl --user -u ogc -f

# 3. Test live streaming and reasoning deltas
curl -N -s http://127.0.0.1:3456/v1/messages \
  -H "content-type: application/json" \
  -H "x-api-key: test" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "glm-5.3",
    "max_tokens": 100,
    "stream": true,
    "messages": [{"role": "user", "content": "Explain 2+2 in one sentence."}]
  }'

# 4. Launch Claude Code with OpenCode
opencode-ccage-yolo --model glm-5.3 --effort high
```
