package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/routatic/proxy/internal/client"
	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/internal/core"
	"github.com/routatic/proxy/internal/metrics"
	"github.com/routatic/proxy/internal/provider"
	"github.com/routatic/proxy/internal/router"
	"github.com/routatic/proxy/internal/token"
	"github.com/routatic/proxy/pkg/types"
)

// claudeCodeFields are top-level fields Claude Code sends that the proxy has
// no model of. OpenCode Go rejects the request when thinking is rewritten
// (adaptive + budget_tokens 0 returns 400/500), and dropping output_config
// silently discards the chosen effort level.
const claudeCodeFields = `
	"thinking":{"type":"adaptive","display":"omitted"},
	"output_config":{"effort":"high"},
	"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},
	"metadata":{"user_id":"u1"},
	"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"1h"}}],
	"tools":[{"name":"Read","description":"read a file","input_schema":{"type":"object"}}],
	"tool_choice":{"type":"auto"},
	"stop_sequences":["</done>"],
	"top_p":0.9,
	"top_k":40,
	"max_tokens":64`

// TestHandleMessages_NativeAnthropicForwardsClientBody checks, through the
// production wiring, that a native /v1/messages model receives the client's
// request as sent: only model and stream are set, and messages carry the
// interrupted-tool-call repair.
func TestHandleMessages_NativeAnthropicForwardsClientBody(t *testing.T) {
	var mu sync.Mutex
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		if r.URL.Path == "/v1/messages" && got == nil {
			got = body
		}
		mu.Unlock()
		http.Error(w, `{"error":"fake"}`, http.StatusInternalServerError)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		APIKey: "test-key",
		OpenCodeGo: config.OpenCodeGoConfig{
			BaseURL:          upstream.URL + "/v1/chat/completions",
			AnthropicBaseURL: upstream.URL + "/v1/messages",
			ResponsesBaseURL: upstream.URL + "/v1/responses",
			TimeoutMs:        5000,
		},
		Models: map[string]config.ModelConfig{
			"default":       {Provider: "opencode-go", ModelID: "glm-5.3"},
			"qwen3.8-flash": {Provider: "opencode-go", ModelID: "qwen3.8-flash", WireFormat: "anthropic"},
		},
	}
	atomicCfg := config.NewAtomicConfig(cfg, t.TempDir()+"/config.json")
	registry := core.NewProviderRegistry()
	if err := registry.Register(provider.NewOpenCodeGoProvider(atomicCfg)); err != nil {
		t.Fatal(err)
	}
	counter, err := token.NewCounter()
	if err != nil {
		t.Fatal(err)
	}
	h := NewMessagesHandler(
		client.NewOpenCodeClient(atomicCfg, nil),
		registry,
		router.NewModelRouter(atomicCfg),
		router.NewFallbackHandler(slog.Default(), 100, time.Second),
		counter, metrics.New(), nil, nil, nil,
	)

	for _, stream := range []bool{true, false} {
		name := map[bool]string{true: "streaming", false: "non-streaming"}[stream]
		t.Run(name, func(t *testing.T) {
			mu.Lock()
			got = nil
			mu.Unlock()
			streamJSON, _ := json.Marshal(stream)
			sent := `{"model":"qwen3.8-flash","stream":` + string(streamJSON) + `,` + claudeCodeFields +
				`,"messages":` + interruptedHistory + `}`
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(sent))
			req.Header.Set("Content-Type", "application/json")
			h.HandleMessages(httptest.NewRecorder(), req)

			mu.Lock()
			defer mu.Unlock()
			if got == nil {
				t.Fatal("nothing reached /v1/messages")
			}
			var sentFields, gotFields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(sent), &sentFields); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(got, &gotFields); err != nil {
				t.Fatalf("upstream body is not a JSON object: %v", err)
			}

			for key := range sentFields {
				if key == "messages" {
					continue
				}
				if !jsonEqual(t, sentFields[key], gotFields[key]) {
					t.Errorf("field %s = %s, want %s", key, gotFields[key], sentFields[key])
				}
			}
			for key := range gotFields {
				if _, ok := sentFields[key]; !ok {
					t.Errorf("upstream body has field %s = %s that the client never sent", key, gotFields[key])
				}
			}

			var messages []types.Message
			if err := json.Unmarshal(gotFields["messages"], &messages); err != nil {
				t.Fatal(err)
			}
			answers := make(map[string][]string)
			for _, m := range messages {
				for _, b := range m.ContentBlocks() {
					if b.Type == "tool_result" {
						answers[b.ToolUseID] = append(answers[b.ToolUseID], b.TextContent())
					}
				}
			}
			for id, want := range wantToolAnswers {
				if got := answers[id]; !slices.Equal(got, []string{want}) {
					t.Errorf("answers for %s = %q, want exactly [%q]", id, got, want)
				}
			}
		})
	}
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}
