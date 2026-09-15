package handlers

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestHandleMessages_MessageStartCarriesInputTokens covers the status-line
// context percentage: Claude Code reads it from message_start, and a translated
// stream sent 0 there, so the percentage sat at 0% until the turn ended and
// then jumped. The upstream only reports usage at the end, so the proxy's own
// count goes in the envelope, as ogc did.
func TestHandleMessages_MessageStartCarriesInputTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
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
			"default": {Provider: "opencode-go", ModelID: "glm-5.3"},
			"glm-5.3": {Provider: "opencode-go", ModelID: "glm-5.3"},
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

	prompt := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 200)
	body := `{"model":"glm-5.3","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"` + prompt + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	h.HandleMessages(recorder, req)

	var start types.MessageEvent
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var event types.MessageEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		if event.Type == "message_start" {
			start = event
			break
		}
	}
	if start.Message == nil {
		t.Fatalf("no message_start in response: %s", recorder.Body.String())
	}
	if start.Message.Usage.InputTokens < 100 {
		t.Errorf("message_start input_tokens = %d, want the counted prompt (hundreds of tokens)", start.Message.Usage.InputTokens)
	}
}
