package gomodels

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/routatic/proxy/internal/core"
)

// nativeCheckSessionID is the x-opencode-session value sent with every probe
// request. OpenCode Go requires the header on every call but doesn't need a
// per-conversation value for a one-off check, so a stable constant is enough
// (verified 2026-09-15: a missing header is a 400 MissingSessionID).
const nativeCheckSessionID = "gomodels-native-check"

// anthropicVersion is the anthropic-version header value OpenCode Go's
// /v1/messages endpoint expects (verified 2026-09-15).
const anthropicVersion = "2023-06-01"

// nativeCheckRequest is the minimal request body used to probe wire-format
// support: one short user turn with a tiny max_tokens, so the probe is cheap
// on whichever endpoint accepts it.
type nativeCheckRequest struct {
	Model     string               `json:"model"`
	MaxTokens int                  `json:"max_tokens"`
	Messages  []nativeCheckMessage `json:"messages"`
}

type nativeCheckMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// checkNativeMessages probes whether modelID accepts the Anthropic Messages
// wire format directly.
//
// It tries anthropicURL (/v1/messages) first; a 2xx response there means
// "yes". Otherwise it tries chatURL (/v1/chat/completions) with the same
// probe: a 2xx response there means "no" — the model works, just not on the
// Anthropic endpoint. If both fail, support is "unknown": a /messages
// failure alone never proves lack of support, since some models return a
// non-2xx there for unrelated reasons (verified 2026-09-15: a chat-only
// model returns HTTP 500 on /messages but 200 on /chat/completions).
func checkNativeMessages(ctx context.Context, hc *http.Client, anthropicURL, chatURL, apiKey, modelID string) NativeSupport {
	body, err := json.Marshal(nativeCheckRequest{
		Model:     modelID,
		MaxTokens: 16,
		Messages:  []nativeCheckMessage{{Role: "user", Content: "ok"}},
	})
	if err != nil {
		return NativeSupportUnknown
	}

	status := probe(ctx, hc, anthropicURL, apiKey, body, true)
	if status/100 == 2 {
		return NativeSupportYes
	}
	// No answer, a rate limit or a timeout on /messages says nothing about
	// support, and "no" is never re-probed, so leave it "unknown" to retry.
	if status == 0 || status == http.StatusTooManyRequests || status == http.StatusRequestTimeout {
		return NativeSupportUnknown
	}
	if probe(ctx, hc, chatURL, apiKey, body, false)/100 == 2 {
		return NativeSupportNo
	}
	return NativeSupportUnknown
}

// probe sends body to url with the auth headers appropriate for the
// Anthropic Messages endpoint (anthropic=true) or the OpenAI Chat
// Completions endpoint (anthropic=false), and returns the response status
// code, or 0 if no response arrived.
func probe(ctx context.Context, hc *http.Client, url, apiKey string, body []byte, anthropic bool) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(core.OpenCodeSessionHeader, nativeCheckSessionID)
	if anthropic {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", anthropicVersion)
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxSourceBytes))

	return resp.StatusCode
}
