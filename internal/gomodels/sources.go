package gomodels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// maxSourceBytes bounds how much of a source response Sync will read, so a
// misbehaving or compromised endpoint cannot exhaust memory.
const maxSourceBytes = 10 << 20 // 10 MiB

// liveModelsResponse is the shape of GET {LiveURL} — OpenCode Go's own model
// list (verified 2026-09-15): {"data":[{"id":"minimax-m3",...}, ...]}.
type liveModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// fetchLiveModels returns the current live model ids from url, in the order
// the endpoint returned them.
func fetchLiveModels(ctx context.Context, hc *http.Client, url string) ([]string, error) {
	body, err := getBody(ctx, hc, url, "application/json")
	if err != nil {
		return nil, fmt.Errorf("fetch live models: %w", err)
	}
	var resp liveModelsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse live models: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("live models response has no entries")
	}
	ids := make([]string, 0, len(resp.Data))
	for _, m := range resp.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("live models response has no model ids")
	}
	return ids, nil
}

// fetchDocsTable downloads the OpenCode Go docs markdown from url and parses
// its Endpoints table via the existing ParseDocsTable.
func fetchDocsTable(ctx context.Context, hc *http.Client, url string) ([]DocsModel, error) {
	body, err := getBody(ctx, hc, url, "text/plain")
	if err != nil {
		return nil, fmt.Errorf("fetch docs table: %w", err)
	}
	models, err := ParseDocsTable(string(body))
	if err != nil {
		return nil, fmt.Errorf("parse docs table: %w", err)
	}
	return models, nil
}

// modelMetadata is the subset of a models.dev catalog entry gomodels needs.
type modelMetadata struct {
	Name  string `json:"name"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
}

// modelsDevProvider is the "opencode-go" key models.dev groups its models
// under (verified 2026-09-15) — it happens to match this proxy's own
// config.ProviderOpenCodeGo, but it names an external API's key, not our
// provider identifier, so it is kept local rather than imported.
const modelsDevProvider = "opencode-go"

// fetchMetadata downloads models.dev's api.json from url and returns the
// opencode-go provider's per-model metadata, keyed by model id.
//
// A response that simply lacks an "opencode-go" key is not an error — every
// model still works with id-only fallbacks — but a transport or parse
// failure is, since it could mean the whole document is truncated or wrong.
func fetchMetadata(ctx context.Context, hc *http.Client, url string) (map[string]modelMetadata, error) {
	body, err := getBody(ctx, hc, url, "application/json")
	if err != nil {
		return nil, fmt.Errorf("fetch model metadata: %w", err)
	}
	var doc map[string]struct {
		Models map[string]modelMetadata `json:"models"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse model metadata: %w", err)
	}
	provider, ok := doc[modelsDevProvider]
	if !ok {
		return map[string]modelMetadata{}, nil
	}
	return provider.Models, nil
}

// getBody performs a bounded GET request and returns the response body.
func getBody(ctx context.Context, hc *http.Client, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", accept)

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d from %s", resp.StatusCode, url)
	}

	limited := http.MaxBytesReader(nil, resp.Body, maxSourceBytes)
	return io.ReadAll(limited)
}
