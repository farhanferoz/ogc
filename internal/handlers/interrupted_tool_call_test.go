package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

// interruptedHistory holds both interruption shapes: toolu_a's real result
// arrives only after the later toolu_b call, and toolu_c never gets a result.
const interruptedHistory = `[
	{"role":"user","content":"read /a"},
	{"role":"assistant","content":[{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}}]},
	{"role":"user","content":"wait, read /b first"},
	{"role":"assistant","content":[{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}}]},
	{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"toolu_b","content":"B contents"},
		{"type":"tool_result","tool_use_id":"toolu_a","content":"A contents"}
	]},
	{"role":"assistant","content":[{"type":"tool_use","id":"toolu_c","name":"Read","input":{"path":"/c"}}]},
	{"role":"user","content":"never mind"}
]`

// wantToolAnswers is the answer each call must receive exactly once.
var wantToolAnswers = map[string]string{
	"toolu_a": "A contents",
	"toolu_b": "B contents",
	"toolu_c": core.InterruptedToolCallPlaceholder,
}

// TestHandleMessages_RepairsInterruptedToolCallsOnEveryTranslation checks,
// through the production wiring, that the chat-completions and Responses
// translations both forward a history where every tool call is answered
// exactly once, directly after the call.
func TestHandleMessages_RepairsInterruptedToolCallsOnEveryTranslation(t *testing.T) {
	var mu sync.Mutex
	bodies := make(map[string][]byte)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		if _, seen := bodies[r.URL.Path]; !seen {
			bodies[r.URL.Path] = body
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
			"default":      {Provider: "opencode-go", ModelID: "glm-5.3"},
			"glm-5.3":      {Provider: "opencode-go", ModelID: "glm-5.3"},
			"gpt-5.6-luna": {Provider: "opencode-go", ModelID: "gpt-5.6-luna", WireFormat: "responses"},
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

	send := func(t *testing.T, model, path string) []byte {
		t.Helper()
		mu.Lock()
		clear(bodies)
		mu.Unlock()
		body := `{"model":"` + model + `","stream":true,"max_tokens":64,"messages":` + interruptedHistory + `}`
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		h.HandleMessages(httptest.NewRecorder(), req)
		mu.Lock()
		defer mu.Unlock()
		got, ok := bodies[path]
		if !ok {
			t.Fatalf("model %s sent nothing to %s", model, path)
		}
		return got
	}

	t.Run("chat completions", func(t *testing.T) {
		var chatReq types.ChatCompletionRequest
		if err := json.Unmarshal(send(t, "glm-5.3", "/v1/chat/completions"), &chatReq); err != nil {
			t.Fatal(err)
		}
		answers := make(map[string][]string)
		for i, m := range chatReq.Messages {
			if m.Role == "tool" {
				answers[m.ToolCallID] = append(answers[m.ToolCallID], m.ContentText())
			}
			if m.Role != "assistant" || len(m.ToolCalls) == 0 {
				continue
			}
			var called, following []string
			for _, tc := range m.ToolCalls {
				called = append(called, tc.ID)
			}
			for j := i + 1; j < len(chatReq.Messages) && chatReq.Messages[j].Role == "tool"; j++ {
				following = append(following, chatReq.Messages[j].ToolCallID)
			}
			if !slices.Equal(called, following) {
				t.Errorf("tool messages directly after tool_calls %v = %v", called, following)
			}
		}
		for id, want := range wantToolAnswers {
			if got := answers[id]; !slices.Equal(got, []string{want}) {
				t.Errorf("answers for %s = %q, want exactly [%q]", id, got, want)
			}
		}
	})

	t.Run("responses", func(t *testing.T) {
		var responsesReq types.ResponsesRequest
		if err := json.Unmarshal(send(t, "gpt-5.6-luna", "/v1/responses"), &responsesReq); err != nil {
			t.Fatal(err)
		}
		answers := make(map[string][]string)
		items := responsesReq.Input
		for i, item := range items {
			if item.Type == "function_call_output" {
				var text string
				if err := json.Unmarshal(item.Output, &text); err != nil {
					t.Fatalf("decode output %s: %v", item.Output, err)
				}
				answers[item.CallID] = append(answers[item.CallID], text)
			}
			if item.Type != "function_call" || (i > 0 && items[i-1].Type == "function_call") {
				continue
			}
			var called, following []string
			j := i
			for ; j < len(items) && items[j].Type == "function_call"; j++ {
				called = append(called, items[j].CallID)
			}
			for ; j < len(items) && items[j].Type == "function_call_output"; j++ {
				following = append(following, items[j].CallID)
			}
			if !slices.Equal(called, following) {
				t.Errorf("function_call_output items directly after calls %v = %v", called, following)
			}
		}
		for id, want := range wantToolAnswers {
			if got := answers[id]; !slices.Equal(got, []string{want}) {
				t.Errorf("answers for %s = %q, want exactly [%q]", id, got, want)
			}
		}
	})
}
