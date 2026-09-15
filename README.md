# ogc

A Go CLI proxy that lets you use any [OpenAI-compatible API](https://platform.openai.com/docs/api-reference/chat) with [Claude Code](https://docs.anthropic.com/en/docs/claude-code).

`ogc` sits between Claude Code and your chosen backend, intercepting Anthropic API requests, transforming them to OpenAI Chat Completions format, and forwarding them to your endpoint. Claude Code thinks it's talking to Anthropic — but your requests go to whichever models and provider you configure.

## Why?

Claude Code is locked to the Anthropic API format. This proxy breaks that lock: point it at any OpenAI-compatible endpoint — [OpenAI](https://platform.openai.com/docs/api-reference/chat), a local [Ollama](https://ollama.ai) instance, [OpenRouter](https://openrouter.ai), [LiteLLM](https://docs.litellm.ai/), or your own deployment — and Claude Code just works with it. No patches, no forks, just set two environment variables and go.

## Features

- **Transparent Proxy** — Claude Code sends Anthropic-format requests, proxy transforms to OpenAI format and back
- **Any OpenAI-compatible Backend** — Works with OpenCode Go, Ollama, OpenRouter, vLLM, or any `/v1/chat/completions` endpoint
- **Model Routing** — Automatically routes to different models based on context (default, thinking, long context, background)
- **Fallback Chains** — If a model fails, automatically tries the next one in your configured chain
- **Circuit Breaker** — Tracks model health and skips failing models to avoid latency spikes
- **Real-time Streaming** — Full SSE streaming with live OpenAI → Anthropic format transformation
- **Tool Calling** — Proper Anthropic tool_use/tool_result ↔ OpenAI function calling translation
- **Provider Support** — Route to OpenAI (`/v1/chat/completions`) or Anthropic (`/v1/messages`) endpoints per model
- **Token Counting** — Uses tiktoken (cl100k_base) for accurate token counting and context threshold detection
- **JSON Configuration** — Flexible config file with environment variable overrides and `${VAR}` interpolation
- **Background Mode** — Run as daemon detached from terminal
- **Auto-start on Login** — Launch on system startup via launchd (macOS)

## Installation

### Go Install

```bash
go install github.com/xynogen/ogc/cmd/ogc@latest
```

### Build from Source

```bash
git clone https://github.com/xynogen/ogc.git
cd ogc
make build
# Optionally install to $GOPATH/bin
make install
```

### Download a Release Binary

Download the latest release for your platform from the [Releases page](https://github.com/xynogen/ogc/releases):

| Platform | File |
|----------|------|
| macOS (Apple Silicon) | `ogc_darwin-arm64` |
| macOS (Intel) | `ogc_darwin-amd64` |
| Linux (x86_64) | `ogc_linux-amd64` |
| Linux (ARM64) | `ogc_linux-arm64` |
| Windows (x86_64) | `ogc_windows-amd64.exe` |
| Windows (ARM64) | `ogc_windows-arm64.exe` |

```bash
# Example: macOS Apple Silicon
curl -L -o ogc https://github.com/xynogen/ogc/releases/latest/download/ogc_darwin-arm64
chmod +x ogc
sudo mv ogc /usr/local/bin/
```

### Requirements

- An API key for your chosen backend (if required)
- Go 1.21+ (only needed if building from source)

## Quick Start

### 1. Initialize Configuration

```bash
ogc init
```

Creates a default config at `~/.config/ogc/config.json`.

### 2. Set Your API Key and Backend URL

```bash
export OGC_API_KEY=your-api-key-here
export OGC_OPENAI_BASE=https://your-openai-compatible-endpoint/v1/chat/completions
```

Examples:
- **OpenAI**: `https://api.openai.com/v1/chat/completions`
- **Ollama**: `http://localhost:11434/v1/chat/completions`
- **OpenRouter**: `https://openrouter.ai/api/v1/chat/completions`
- **LiteLLM**: `http://localhost:4000/v1/chat/completions`

### 3. Start the Proxy

```bash
ogc serve
```

You'll see output like:

```
Starting ogc v0.1.0
Listening on 127.0.0.1:3456
Forwarding to: https://your-openai-compatible-endpoint/v1/chat/completions

Configure Claude Code with:
  export ANTHROPIC_BASE_URL=http://127.0.0.1:3456
  export ANTHROPIC_AUTH_TOKEN=unused
```

#### Running in Background

To run the proxy in the background (detached from terminal):

```bash
ogc serve --background
# or
ogc serve -b
```

This starts the server as a background daemon and returns immediately. Logs are written to `~/.config/ogc/ogc.log`.

#### Auto-start on Login

To start the proxy automatically when you log in:

```bash
ogc autostart enable
```

This creates a launchd plist on macOS. To disable:

```bash
ogc autostart disable
```

Check status:

```bash
ogc autostart status
```

### 4. Configure Claude Code

In a separate terminal (or the same one before running `claude`):

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:3456
export ANTHROPIC_AUTH_TOKEN=unused
```

### 5. Run Claude Code

```bash
claude
```

That's it. Claude Code will now route all requests through ogc to your configured backend.

## How It Works

```
┌─────────────┐     Anthropic API      ┌─────────────┐     OpenAI API          ┌──────────────────────┐
│  Claude Code ├──────────────────────►│  ogc         ├────────────────────────►│  Any OpenAI-compatible│
│  (CLI)       │  POST /v1/messages   │  (Proxy)     │  /v1/chat/completions  │  backend              │
│              │◄──────────────────────┤              │◄────────────────────────┤                      │
└─────────────┘   Anthropic SSE        └─────────────┘   OpenAI SSE             └──────────────────────┘
```

1. Claude Code sends a request in [Anthropic Messages API](https://docs.anthropic.com/en/api/messages) format
2. ogc parses the request, counts tokens, and selects a model via routing rules
3. The request is transformed to [OpenAI Chat Completions](https://platform.openai.com/docs/api-reference/chat) format
4. The transformed request is sent to your configured endpoint
5. The response (streaming or non-streaming) is transformed back to Anthropic format
6. Claude Code receives the response as if it came from Anthropic directly

### What Gets Transformed

| Anthropic | OpenAI |
|-----------|--------|
| `system` (string or array) | `messages[0]` with `role: "system"` |
| `content: [{"type":"text","text":"..."}]` | `content: "..."` |
| `tool_use` content blocks | `tool_calls` array |
| `tool_result` content blocks | `role: "tool"` messages |
| `thinking` content blocks | Skipped (no equivalent) |
| `stop_reason: "end_turn"` | `finish_reason: "stop"` |
| `stop_reason: "tool_use"` | `finish_reason: "tool_calls"` |
| SSE `message_start` / `content_block_delta` / `message_stop` | SSE `role` / `delta.content` / `[DONE]` |

## Configuration

### Config File

Location: `~/.config/ogc/config.json`

Override with `OGC_CONFIG` environment variable.

### Full Config Reference

```json
{
  "api_key": "${OGC_API_KEY}",
  "host": "127.0.0.1",
  "port": 3456,

  "models": {
    "default": {
      "provider": "openai",
      "model_id": "gpt-4o",
      "temperature": 0.7,
      "max_tokens": 4096
    }
  },

  "model_mapping": {
    "claude-opus": "default",
    "claude-sonnet": "default",
    "claude-haiku": "default"
  },

  "upstream": {
    "base_url": "https://api.openai.com/v1",
    "anthropic_base_url": "https://api.anthropic.com",
    "timeout_ms": 300000
  },

  "logging": {
    "level": "info",
    "requests": true
  }
}
```

### Environment Variables

Environment variables override config file values. Config values also support `${VAR}` interpolation.

| Variable | Description | Default |
|----------|-------------|---------|
| `OGC_API_KEY` | API key for your backend (**required**) | — |
| `OGC_CONFIG` | Custom config file path | `~/.config/ogc/config.json` |
| `OGC_HOST` | Proxy listen host | `127.0.0.1` |
| `OGC_PORT` | Proxy listen port | `3456` |
| `OGC_OPENAI_BASE` | Full URL to your OpenAI-compatible `/v1/chat/completions` endpoint | `https://api.openai.com/v1/chat/completions` |
| `OGC_LOG_LEVEL` | Log level: `debug`, `info`, `warn`, `error` | `info` |

### Model Mapping

The `model_mapping` section maps Claude Code model names to your configured models:

| Claude Model | Maps To | Notes |
| ------------ | ------- | ----- |
| `claude-opus` | Your preferred model | Primary model |
| `claude-sonnet` | Your preferred model | Balanced choice |
| `claude-haiku` | Your preferred model | Fast/cheap option |

### Provider

The `provider` field in each model config determines the endpoint:

| Provider | Endpoint | Use Case |
| -------- | -------- | -------- |
| `openai` (default) | `/v1/chat/completions` | OpenAI-compatible APIs |
| `anthropic` | `/v1/messages` | Anthropic-compatible APIs |

```json
{
  "models": {
    "minimax": {
      "provider": "anthropic",
      "model_id": "MiniMax-Text-01"
    }
  }
}
```

### Fallback Chains

When a model request fails (network error, rate limit, server error), the proxy tries the next model in the fallback chain:

```
Primary model → Fallback 1 → Fallback 2 → ... → Error (all failed)
```

Each model also has a **circuit breaker** that tracks consecutive failures. After 3 failures, the circuit opens and that model is skipped for 30 seconds, then tested again (half-open state).

## CLI Commands

```
ogc serve              Start the proxy server
ogc serve -b          Start in background (detached from terminal)
ogc serve --port 8080  Start on a custom port
ogc serve --config /path/to/config.json  Use a custom config
ogc stop               Stop the running proxy server
ogc status             Check if the proxy is running
ogc autostart enable   Enable auto-start on login
ogc autostart disable  Disable auto-start on login
ogc autostart status   Check autostart status
ogc init               Create default configuration file
ogc validate           Validate configuration file
ogc models             List configured models
ogc --version          Show version
```

## API Endpoints

The proxy exposes these endpoints that Claude Code expects:

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/messages` | Main chat endpoint (Anthropic format) |
| `POST` | `/v1/messages/count_tokens` | Token counting |
| `GET` | `/health` | Health check |

## Troubleshooting

### "invalid request body" Error

This means the proxy couldn't parse the request from Claude Code. Enable debug logging to see the raw request:

```json
{ "logging": { "level": "debug" } }
```

Or set the environment variable:

```bash
export OGC_LOG_LEVEL=debug
```

### "all models failed" Error

All models in the fallback chain returned errors. Check:

1. Your API key is valid: `ogc validate`
2. You haven't exceeded your backend's usage limits
3. Your backend is reachable: `curl -H "Authorization: Bearer $OGC_API_KEY" $OGC_OPENAI_BASE`

### Connection Refused

Make sure the proxy is running:

```bash
ogc status
```

And Claude Code is pointing to the right address:

```bash
echo $ANTHROPIC_BASE_URL  # Should be http://127.0.0.1:3456
```

### Streaming Not Working

The proxy transforms OpenAI SSE to Anthropic SSE in real-time. If streaming appears broken:

1. Set log level to `debug` to see the raw SSE chunks
2. Check that no proxy or firewall is buffering the connection
3. Try a non-streaming request first to verify the model works

### Subagents fail with "500 no model mapping found"

A subagent pinned to `haiku`, `sonnet` or `opus` is sent with that tier's Claude model name, and ogc refuses a name it has no mapping for. Point every tier at the model you launch with, before starting Claude Code:

```bash
export ANTHROPIC_DEFAULT_OPUS_MODEL=<model> ANTHROPIC_DEFAULT_SONNET_MODEL=<model> \
       ANTHROPIC_DEFAULT_HAIKU_MODEL=<model> ANTHROPIC_DEFAULT_FABLE_MODEL=<model> \
       ANTHROPIC_SMALL_FAST_MODEL=<model>
```

### A subagent finishes but its result never reaches the session

With `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`, Claude Code launches a named subagent as an agent-team teammate. A teammate's result reaches the lead session only when the lead's current turn ends, so a session that keeps working in one long turn (an autonomous run, for example) may never receive it. Turn agent teams off in the session's `settings.json`:

```json
{ "env": { "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "0" } }
```

A named subagent then runs as an ordinary subagent, and its result arrives as soon as it finishes, even mid-turn. Set it in `settings.json`, not as a shell export: a settings-file `env` value overrides a shell export.

Measured 2026-09-15 on Claude Code 2.1.272 with DeepSeek V4 Flash, with the lead kept busy:
- agent teams on: the result arrived about 2 minutes after the subagent finished, at the lead's turn end;
- agent teams off: it arrived 4 seconds after launch.

### Debug Mode

For maximum logging, run with debug level:

```bash
OGC_LOG_LEVEL=debug ogc serve
```

This logs:

- Raw Anthropic request body from Claude Code
- Transformed OpenAI request sent to your backend
- Raw OpenAI response received
- SSE stream events during streaming

## Architecture

```
cmd/ogc/main.go           CLI entry point (cobra commands)
internal/
├── config/
│   ├── config.go               Config types
│   └── loader.go               JSON loading, env overrides, ${VAR} interpolation
├── router/
│   ├── model_router.go         Model selection based on scenario
│   ├── scenarios.go            Scenario detection (default/think/long_context/background)
│   └── fallback.go            Fallback handler with circuit breaker
├── server/
│   └── server.go               HTTP server setup, graceful shutdown, PID management
├── handlers/
│   ├── messages.go             POST /v1/messages handler (streaming + non-streaming)
│   └── health.go               Health check and token counting endpoints
├── transformer/
│   ├── request.go              Anthropic → OpenAI request transformation
│   ├── response.go             OpenAI → Anthropic response transformation
│   └── stream.go               Real-time SSE stream transformation
├── client/
│   └── opencode.go             HTTP client for the OpenAI-compatible backend
└── token/
    └── counter.go              Tiktoken token counter (cl100k_base)
pkg/types/
├── anthropic.go                Anthropic API types (polymorphic system/content fields)
└── openai.go                   OpenAI API types
configs/
└── config.example.json         Example configuration
```

### Key Design Decisions

- **Polymorphic field handling**: Anthropic's `system` and `content` fields accept both strings and arrays. We use `json.RawMessage` with accessor methods (`SystemText()`, `ContentBlocks()`) to handle both formats correctly.
- **Real-time stream proxying**: SSE events are transformed in-flight, not buffered. This means Claude Code sees responses as they arrive from the backend.
- **Circuit breaker per model**: Each model gets its own circuit breaker. After 3 consecutive failures, the model is skipped for 30 seconds, then tested again.
- **Environment variable interpolation**: Config values like `"${OGC_API_KEY}"` are resolved at load time, so you never need to put secrets in the config file.

## Development

```bash
# Build (version auto-detected from git)
make build

# Run in development mode
make run

# Run tests with race detector
make test

# Run go vet
make vet

# Clean build artifacts
make clean

# Install to $GOPATH/bin
make install

# Build cross-platform release binaries
make dist
```

## Credits

This project is a fork of [ogc](https://github.com/samueltuyizere/ogc) by [samueltuyizere](https://github.com/samueltuyizere). All core ideas, architecture, and original implementation belong to them.

## License

MIT
