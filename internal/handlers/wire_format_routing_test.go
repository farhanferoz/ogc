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
	"github.com/routatic/proxy/internal/gomodels"
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

// TestHandleMessages_GoModelsSnapshotRoutesUnconfiguredModel checks, through
// the same production wiring as above, that a model known only from the
// go-models snapshot (never hand-configured, see internal/router's
// resolveFromGoModelsSnapshot) still reaches the upstream endpoint its
// snapshot entry names — and that hand config still wins when both exist.
func TestHandleMessages_GoModelsSnapshotRoutesUnconfiguredModel(t *testing.T) {
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
			"default": {Provider: "opencode-go", ModelID: "glm-5.3"},
			// glm-5.3 is hand-configured for chat/completions; the snapshot
			// below disagrees (claims it's native-anthropic) and must lose.
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
	modelRouter := router.NewModelRouter(atomicCfg)
	modelRouter.SetGoModelsSnapshot(&gomodels.Snapshot{Models: []gomodels.Model{
		{ID: "glm-5.3", WireFormat: gomodels.WireFormatAnthropic},
		{ID: "minimax-m3", WireFormat: gomodels.WireFormatOpenAI, NativeMessages: gomodels.NativeSupportYes},
		{ID: "grok-4.6", WireFormat: gomodels.WireFormatResponses},
	}})
	h := NewMessagesHandler(
		client.NewOpenCodeClient(atomicCfg, nil),
		registry,
		modelRouter,
		router.NewFallbackHandler(slog.Default(), 100, time.Second),
		counter, metrics.New(), nil, nil, nil,
	)

	request := func(model string) string {
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
		if len(paths) == 0 {
			t.Fatalf("model %s reached no upstream path", model)
		}
		return paths[0]
	}

	for model, wantPath := range map[string]string{
		"minimax-m3": "/v1/messages",         // snapshot-only, native_messages: yes
		"grok-4.6":   "/v1/responses",        // snapshot-only, wire_format: responses
		"glm-5.3":    "/v1/chat/completions", // hand config wins over the snapshot's "anthropic" claim
	} {
		t.Run(model, func(t *testing.T) {
			if got := request(model); got != wantPath {
				t.Fatalf("upstream path = %q, want %q", got, wantPath)
			}
		})
	}

	t.Run("setter update changes routing with no config reload", func(t *testing.T) {
		modelRouter.SetGoModelsSnapshot(&gomodels.Snapshot{Models: []gomodels.Model{
			{ID: "minimax-m3", WireFormat: gomodels.WireFormatOpenAI}, // no longer native
		}})
		if got := request("minimax-m3"); got != "/v1/chat/completions" {
			t.Fatalf("upstream path after snapshot swap = %q, want %q", got, "/v1/chat/completions")
		}
	})
}
