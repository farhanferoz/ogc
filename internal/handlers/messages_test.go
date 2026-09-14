package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xynogen/ogc/internal/client"
	"github.com/xynogen/ogc/internal/config"
	"github.com/xynogen/ogc/internal/metrics"
	"github.com/xynogen/ogc/internal/router"
)

func TestHandleStreaming_NoFallbackAfterPartialMessage(t *testing.T) {
	// The primary model starts a message and is cut off; its fallback would stream a
	// complete message. Retrying on the same stream would give the client two messages.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\""+req.Model+"\"}}\n\n")
		if req.Model == "backup" {
			io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"from backup\"}}\n\n")
			io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		}
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"primary": {Provider: "anthropic", ModelID: "primary"},
			"backup":  {Provider: "anthropic", ModelID: "backup"},
		},
		Fallbacks: map[string][]config.ModelConfig{
			"primary": {{Provider: "anthropic", ModelID: "backup"}},
		},
		Upstream: config.UpstreamConfig{BaseURL: upstream.URL, AnthropicBaseURL: upstream.URL, TimeoutMs: 5000},
	}
	h := NewMessagesHandler(cfg, client.NewClient(cfg.Upstream, "test-key"), router.NewModelRouter(cfg), nil, nil, metrics.New())

	body := `{"model":"primary","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	h.HandleMessages(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body)))

	out := rec.Body.String()
	if got := strings.Count(out, `"type":"message_start"`); got != 1 {
		t.Errorf("client received %d message_start events, want 1:\n%s", got, out)
	}
	if strings.Contains(out, "from backup") {
		t.Errorf("fallback output was appended to a partially sent message:\n%s", out)
	}
	if !strings.Contains(out, "event: error") {
		t.Errorf("expected an error event after the cut-off message:\n%s", out)
	}
}

func TestHandleStreaming_FallbackWhenPrimaryFailsBeforeAnyEvent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "primary" {
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"backup\"}}\n\n")
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"from backup\"}}\n\n")
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"primary": {Provider: "anthropic", ModelID: "primary"},
			"backup":  {Provider: "anthropic", ModelID: "backup"},
		},
		Fallbacks: map[string][]config.ModelConfig{
			"primary": {{Provider: "anthropic", ModelID: "backup"}},
		},
		Upstream: config.UpstreamConfig{BaseURL: upstream.URL, AnthropicBaseURL: upstream.URL, TimeoutMs: 5000},
	}
	h := NewMessagesHandler(cfg, client.NewClient(cfg.Upstream, "test-key"), router.NewModelRouter(cfg), nil, nil, metrics.New())

	body := `{"model":"primary","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	h.HandleMessages(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body)))

	out := rec.Body.String()
	if !strings.Contains(out, "from backup") || strings.Contains(out, "event: error") {
		t.Errorf("expected the fallback model's message and no error event:\n%s", out)
	}
	if got := strings.Count(out, `"type":"message_start"`); got != 1 {
		t.Errorf("client received %d message_start events, want 1:\n%s", got, out)
	}
}
