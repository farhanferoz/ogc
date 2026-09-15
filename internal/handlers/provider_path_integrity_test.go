package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/internal/core"
	"github.com/routatic/proxy/internal/metrics"
	"github.com/routatic/proxy/internal/provider"
	"github.com/routatic/proxy/internal/router"
	"github.com/routatic/proxy/internal/transformer"
	"github.com/routatic/proxy/pkg/types"
)

// newProviderRegistryTestHandler builds a MessagesHandler with a populated
// providerRegistry (the "new" dispatch path), matching production wiring in
// internal/server/server.go where OpenCodeGoProvider is always registered.
// This is the path that actually serves opencode-go streaming requests by
// default; newStreamingTestHandler (nil providerRegistry) exercises only the
// legacy fallback branch, which production never reaches for this provider.
func newProviderRegistryTestHandler(t *testing.T, upstreamURL string) *MessagesHandler {
	t.Helper()
	cfg := &config.Config{
		APIKey: "test-key",
		OpenCodeGo: config.OpenCodeGoConfig{
			AnthropicBaseURL: upstreamURL,
			BaseURL:          upstreamURL,
			TimeoutMs:        5000,
		},
	}
	atomicCfg := config.NewAtomicConfig(cfg, "/tmp/test-config.json")

	registry := core.NewProviderRegistry()
	if err := registry.Register(provider.NewOpenCodeGoProvider(atomicCfg)); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	return &MessagesHandler{
		providerRegistry:    registry,
		fallbackHandler:     router.NewFallbackHandler(slog.Default(), 3, 30*time.Second),
		logger:              slog.Default(),
		metrics:             metrics.New(),
		streamProxy:         NewStreamProxy(),
		streamHandler:       transformer.NewStreamHandler(),
		requestTransformer:  transformer.NewRequestTransformer(),
		responseTransformer: transformer.NewResponseTransformer(),
	}
}

// --- Behaviour 4: non-streaming native passthrough keeps thinking-block signatures ---

func TestProviderPath_NonStreamingAnthropicPassthrough_KeepsSignature(t *testing.T) {
	const upstreamJSON = `{"id":"msg_1","type":"message","role":"assistant","model":"minimax-m3","content":[{"type":"thinking","thinking":"reasoning...","signature":"sig-abc123"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(upstreamJSON))
	}))
	defer upstream.Close()

	handler := newProviderRegistryTestHandler(t, upstream.URL)

	rawBody := json.RawMessage(`{"model":"claude-opus-4-8","max_tokens":256,"messages":[{"role":"user","content":"hello"}]}`)
	var anthropicReq types.MessageRequest
	if err := json.Unmarshal(rawBody, &anthropicReq); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	chain := []config.ModelConfig{{Provider: "opencode-go", ModelID: "minimax-m3"}}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	handler.handleNonStreaming(recorder, req, &anthropicReq, &core.NormalizedRequest{}, chain, rawBody, router.Scenario(""), "")

	body := recorder.Body.String()
	if !strings.Contains(body, `"signature":"sig-abc123"`) {
		t.Errorf("MISSING: signature dropped from non-streaming native passthrough response:\n%s", body)
	} else {
		t.Logf("body:\n%s", body)
	}
}

func runStreamingRequest(t *testing.T, handler *MessagesHandler, modelID string) *httptest.ResponseRecorder {
	t.Helper()
	rawBody := json.RawMessage(`{
		"model": "claude-opus-4-8",
		"stream": true,
		"max_tokens": 256,
		"messages": [{"role":"user","content":"hello"}]
	}`)
	var anthropicReq types.MessageRequest
	if err := json.Unmarshal(rawBody, &anthropicReq); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	chain := []config.ModelConfig{{Provider: "opencode-go", ModelID: modelID}}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 8*time.Second)
	defer cancel()

	handler.handleStreaming(recorder, req.WithContext(ctx), &anthropicReq, &core.NormalizedRequest{Stream: true}, chain, rawBody, router.Scenario(""), "")
	return recorder
}

// --- Behaviour 1: interleaved ping cannot split a native passthrough event ---

func TestProviderPath_AnthropicPassthrough_NoKeepaliveSplit(t *testing.T) {
	blockCh := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Stall well past the heartbeat interval so a real ping would fire
		// if the heartbeat were not paused during native passthrough.
		select {
		case <-blockCh:
		case <-time.After(6 * time.Second):
		}
		_, _ = fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\"}\n\n")
		_, _ = fmt.Fprintf(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer upstream.Close()

	handler := newProviderRegistryTestHandler(t, upstream.URL)

	done := make(chan *httptest.ResponseRecorder)
	go func() {
		done <- runStreamingRequest(t, handler, "minimax-m3")
	}()

	time.Sleep(1200 * time.Millisecond)
	close(blockCh)

	var recorder *httptest.ResponseRecorder
	select {
	case recorder = <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("handleStreaming did not return")
	}

	body := recorder.Body.String()
	if !strings.Contains(body, "message_start") || !strings.Contains(body, "content_block_delta") {
		t.Fatalf("missing expected events in body:\n%s", body)
	}
	if strings.Contains(body, ":keepalive") {
		t.Errorf("keepalive leaked into native passthrough stream, could split an event:\n%s", body)
	}
}

// --- Behaviour 2: native passthrough that ends before message_stop is an error ---

func TestProviderPath_AnthropicPassthrough_TruncatedBeforeMessageStop(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
		_, _ = fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\"}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// No message_stop: upstream cuts off (e.g. crashed) mid-turn.
	}))
	defer upstream.Close()

	handler := newProviderRegistryTestHandler(t, upstream.URL)
	recorder := runStreamingRequest(t, handler, "minimax-m3")
	body := recorder.Body.String()

	// Desired behaviour: partial content reached the client, so the handler
	// must surface an error event rather than closing the stream as if it
	// completed normally (no message_stop, no error).
	gotMessageStop := strings.Contains(body, "message_stop")
	gotErrorEvent := strings.Contains(body, `"type":"error"`) || strings.Contains(body, "event: error")

	if !gotMessageStop && !gotErrorEvent {
		t.Errorf("MISSING: stream ended before message_stop with no error event surfaced (silent truncation):\n%s", body)
	} else {
		t.Logf("body:\n%s", body)
	}
}

// --- Behaviour 3a: translated stream ending with no finish_reason ---

func TestProviderPath_TranslatedStream_EOFWithoutFinishReason(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Connection ends with no finish_reason chunk and no [DONE].
	}))
	defer upstream.Close()

	handler := newProviderRegistryTestHandler(t, upstream.URL)
	// deepseek-v4-pro classifies to WireFormatOpenAIChat by default (see
	// internal/provider/opencode_go_wireformat_test.go).
	recorder := runStreamingRequest(t, handler, "deepseek-v4-pro")
	body := recorder.Body.String()

	gotErrorEvent := strings.Contains(body, `"type":"error"`) || strings.Contains(body, "event: error")
	gotMessageStop := strings.Contains(body, "message_stop")

	if gotMessageStop && !gotErrorEvent {
		t.Errorf("MISSING: translated stream with no finish_reason was closed as a complete message instead of surfaced as an error:\n%s", body)
	} else {
		t.Logf("body:\n%s", body)
	}
}

// --- Behaviour 3b: translated stream carrying an in-stream error object ---

func TestProviderPath_TranslatedStream_InStreamErrorObject(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
		_, _ = fmt.Fprintf(w, "data: {\"error\":{\"message\":\"upstream exploded\",\"type\":\"server_error\"}}\n\n")
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer upstream.Close()

	handler := newProviderRegistryTestHandler(t, upstream.URL)
	recorder := runStreamingRequest(t, handler, "deepseek-v4-pro")
	body := recorder.Body.String()

	gotErrorEvent := strings.Contains(body, `"type":"error"`) || strings.Contains(body, "event: error")
	gotMessageStop := strings.Contains(body, "message_stop")

	if gotMessageStop && !gotErrorEvent {
		t.Errorf("MISSING: in-stream error object was silently dropped and the stream closed as complete:\n%s", body)
	} else {
		t.Logf("body:\n%s", body)
	}
}

// --- Behaviour 5: no fallback to a second model after a genuine error following partial content ---

func TestProviderPath_NoFallbackAfterPartialContent_OnGenuineError(t *testing.T) {
	primaryHit := make(chan struct{}, 1)
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHit <- struct{}{}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Forcibly sever the TCP connection mid-response so the client sees a
		// genuine read error (io.ErrUnexpectedEOF), not a clean EOF.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("ResponseWriter does not support hijacking")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer primary.Close()

	fallbackHit := int32(0)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHit++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
		_, _ = fmt.Fprintf(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer fallback.Close()
	_ = fallbackHit

	// Route primary model through `primary`'s upstream via a config whose
	// AnthropicBaseURL differs per attempt is not directly supported by the
	// single-provider test handler, so instead we point the shared
	// AnthropicBaseURL at `primary` and rely on ssePayloadWritten gating:
	// only one model is needed to prove the gate, since a second model would
	// hit the SAME upstream and still must not be attempted.
	handler := newProviderRegistryTestHandler(t, primary.URL)
	chain := []config.ModelConfig{
		{Provider: "opencode-go", ModelID: "minimax-m3"},
		{Provider: "opencode-go", ModelID: "minimax-m2.5"},
	}

	rawBody := json.RawMessage(`{"model":"claude-opus-4-8","stream":true,"max_tokens":256,"messages":[{"role":"user","content":"hello"}]}`)
	var anthropicReq types.MessageRequest
	if err := json.Unmarshal(rawBody, &anthropicReq); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 8*time.Second)
	defer cancel()

	handler.handleStreaming(recorder, req.WithContext(ctx), &anthropicReq, &core.NormalizedRequest{Stream: true}, chain, rawBody, router.Scenario(""), "")

	select {
	case <-primaryHit:
	default:
		t.Fatal("primary upstream was never hit")
	}

	body := recorder.Body.String()
	// A second message_start EVENT would indicate the fallback model was tried
	// on the same stream after partial content — the bug this behaviour
	// prevents. Count "event: message_start" lines, not the substring, since
	// the message_start payload itself also contains the text "message_start".
	startCount := strings.Count(body, "event: message_start")
	if startCount > 1 {
		t.Errorf("fallback ran on the same stream after partial content: %d message_start events\n%s", startCount, body)
	} else {
		t.Logf("startCount=%d body:\n%s", startCount, body)
	}
}
