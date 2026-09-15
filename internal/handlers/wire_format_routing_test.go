package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
)

// TestHandleMessages_WireFormatOverrideSelectsGoEndpoint checks, through the
// production wiring (provider registry + model router), that a requested Go
// model reaches the upstream endpoint its wire_format names.
func TestHandleMessages_WireFormatOverrideSelectsGoEndpoint(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
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
			"default":           {Provider: "opencode-go", ModelID: "glm-5.3"},
			"deepseek-v4-flash": {Provider: "opencode-go", ModelID: "deepseek-v4-flash", WireFormat: "anthropic"},
			"glm-5.3":           {Provider: "opencode-go", ModelID: "glm-5.3"},
			"gpt-5.6-luna":      {Provider: "opencode-go", ModelID: "gpt-5.6-luna", WireFormat: "responses"},
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

	for model, wantPath := range map[string]string{
		"deepseek-v4-flash": "/v1/messages",
		"glm-5.3":           "/v1/chat/completions",
		"gpt-5.6-luna":      "/v1/responses",
	} {
		t.Run(model, func(t *testing.T) {
			mu.Lock()
			paths = nil
			mu.Unlock()
			body := `{"model":"` + model + `","stream":true,"max_tokens":64,` +
				`"messages":[{"role":"user","content":"hi"}]}`
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			h.HandleMessages(httptest.NewRecorder(), req)

			mu.Lock()
			defer mu.Unlock()
			if len(paths) == 0 || paths[0] != wantPath {
				t.Fatalf("upstream paths = %v, want first %s", paths, wantPath)
			}
		})
	}
}
