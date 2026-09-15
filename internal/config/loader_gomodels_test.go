package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeGoModelsFixture writes a minimal go-models.json snapshot (in the
// shape internal/gomodels.Snapshot marshals) beside cfgPath, containing one
// model per id/wireFormat/contextWindow triple.
func writeGoModelsFixture(t *testing.T, dir string, models ...goModelsSnapshotModel) {
	t.Helper()
	snap := goModelsSnapshot{Models: models}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, goModelsSnapshotFileName)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFromPath_GoModelsSnapshotFillsGaps(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfgJSON := `{"api_key": "test-key", "models": {"default": {"provider": "opencode-go", "model_id": "glm-5.3", "temperature": 0.5, "max_tokens": 4096}}}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	writeGoModelsFixture(t, dir,
		goModelsSnapshotModel{ID: "minimax-m3", WireFormat: "anthropic", ContextWindow: 1000000},
	)

	cfg, err := LoadFromPath(cfgPath)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}

	got, ok := cfg.Models["minimax-m3"]
	if !ok {
		t.Fatalf("cfg.Models missing snapshot-only model minimax-m3: %+v", cfg.Models)
	}
	if got.Provider != ProviderOpenCodeGo || got.ModelID != "minimax-m3" || got.WireFormat != "anthropic" || got.ContextWindow != 1000000 {
		t.Errorf("cfg.Models[minimax-m3] = %+v, want provider=%s model_id=minimax-m3 wire_format=anthropic context_window=1000000", got, ProviderOpenCodeGo)
	}
	// Temperature/MaxTokens are inherited from the "default" model.
	if got.Temperature != 0.5 || got.MaxTokens != 4096 {
		t.Errorf("cfg.Models[minimax-m3] temperature/max_tokens = %v/%v, want inherited 0.5/4096", got.Temperature, got.MaxTokens)
	}
}

func TestLoadFromPath_HandWrittenConfigWinsOverSnapshot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfgJSON := `{"api_key": "test-key", "models": {
		"default": {"provider": "opencode-go", "model_id": "glm-5.3"},
		"glm-5.3": {"provider": "opencode-go", "model_id": "glm-5.3", "wire_format": "responses", "context_window": 999}
	}}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	// The snapshot disagrees with the hand-written entry for glm-5.3; hand
	// config must win, and the snapshot's value must never appear.
	writeGoModelsFixture(t, dir,
		goModelsSnapshotModel{ID: "glm-5.3", WireFormat: "openai", ContextWindow: 111},
	)

	cfg, err := LoadFromPath(cfgPath)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}

	got := cfg.Models["glm-5.3"]
	if got.WireFormat != "responses" || got.ContextWindow != 999 {
		t.Errorf("cfg.Models[glm-5.3] = %+v, want the hand-written wire_format=responses context_window=999 (snapshot must not override it)", got)
	}
}

func TestLoadFromPath_MissingSnapshotFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfgJSON := `{"api_key": "test-key", "models": {"default": {"provider": "opencode-go", "model_id": "glm-5.3"}}}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	// No go-models.json written at all.

	cfg, err := LoadFromPath(cfgPath)
	if err != nil {
		t.Fatalf("LoadFromPath should succeed with no snapshot file: %v", err)
	}
	if len(cfg.Models) != 1 {
		t.Errorf("cfg.Models = %+v, want only the hand-written entry", cfg.Models)
	}
}

func TestLoadFromPath_CorruptSnapshotFileIsIgnored(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfgJSON := `{"api_key": "test-key", "models": {"default": {"provider": "opencode-go", "model_id": "glm-5.3"}}}`
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(dir, goModelsSnapshotFileName)
	if err := os.WriteFile(snapshotPath, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromPath(cfgPath)
	if err != nil {
		t.Fatalf("LoadFromPath should tolerate a corrupt snapshot file: %v", err)
	}
	if len(cfg.Models) != 1 {
		t.Errorf("cfg.Models = %+v, want only the hand-written entry (corrupt snapshot ignored)", cfg.Models)
	}
}
