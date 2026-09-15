// This file is package config_test (an external test package), not config,
// specifically so it can import internal/provider without an import cycle:
// internal/provider imports internal/config, and internal/gomodels (which
// the merge in loader.go mirrors rather than importing, see loader.go's
// mergeGoModelsSnapshot) imports internal/core, which also imports
// internal/config.
package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/internal/core"
	"github.com/routatic/proxy/internal/provider"
)

// TestLoadFromPath_SnapshotOnlyNativeModelRoutesToMessagesEndpoint proves,
// through the same production code the request handler calls
// (OpenCodeGoProvider.WireFormat, see internal/provider/opencode_go.go),
// that a model known only from the go-models.json snapshot — never
// hand-configured — reaches the Anthropic /v1/messages endpoint once the
// snapshot records it as natively supporting the Messages wire format
// (native_messages "yes" -> wire_format "anthropic", set by
// internal/gomodels.Sync).
func TestLoadFromPath_SnapshotOnlyNativeModelRoutesToMessagesEndpoint(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"api_key": "test-key"}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Mirrors the shape internal/gomodels.Snapshot marshals for a model that
	// checkNativeMessages found to accept /v1/messages directly.
	snapshotJSON := `{"fetched_at":"2026-09-15T00:00:00Z","models":[
		{"id":"newly-native-model","name":"Newly Native","wire_format":"anthropic","in_docs":false,"context_window":64000,"native_messages":"yes","native_checked_at":"2026-09-15T00:00:00Z"}
	]}`
	if err := os.WriteFile(filepath.Join(dir, "go-models.json"), []byte(snapshotJSON), 0644); err != nil {
		t.Fatal(err)
	}
	// Sanity: this really is the shape internal/gomodels writes.
	var sanity struct {
		Models []struct{ ID string } `json:"models"`
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &sanity); err != nil || len(sanity.Models) != 1 {
		t.Fatalf("fixture setup is broken: %v", err)
	}

	cfg, err := config.LoadFromPath(cfgPath)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}

	mc, ok := cfg.Models["newly-native-model"]
	if !ok {
		t.Fatalf("cfg.Models missing the snapshot-only model: %+v", cfg.Models)
	}
	if mc.Provider != config.ProviderOpenCodeGo {
		t.Fatalf("Provider = %q, want %q", mc.Provider, config.ProviderOpenCodeGo)
	}

	atomicCfg := config.NewAtomicConfig(cfg, cfgPath)
	p := provider.NewOpenCodeGoProvider(atomicCfg)
	if got := p.WireFormat(mc); got != core.WireFormatAnthropic {
		t.Errorf("OpenCodeGoProvider.WireFormat(%+v) = %v, want %v (the Anthropic Messages endpoint)", mc, got, core.WireFormatAnthropic)
	}
}
