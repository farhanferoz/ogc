package router

import (
	"testing"

	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/internal/gomodels"
)

func TestResolveRequestedModel_GoModelsSnapshotFillsGap(t *testing.T) {
	cfg := &config.Config{
		RespectRequestedModel: boolPtr(true),
		Models: map[string]config.ModelConfig{
			"default": {Provider: "opencode-go", ModelID: "glm-5.3", Temperature: 0.5, MaxTokens: 4096},
		},
	}
	atomic := newTestAtomicConfig(cfg)
	r := NewModelRouter(atomic)
	r.SetGoModelsSnapshot(&gomodels.Snapshot{Models: []gomodels.Model{
		{ID: "minimax-m3", WireFormat: gomodels.WireFormatAnthropic, ContextWindow: 1000000, NativeMessages: gomodels.NativeSupportNotChecked},
	}})

	result, ok, err := r.resolveRequestedModel(cfg, "minimax-m3", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected resolveRequestedModel to match the snapshot-only model")
	}
	primary := result.Primary
	if primary.Provider != "opencode-go" || primary.ModelID != "minimax-m3" || primary.WireFormat != "anthropic" || primary.ContextWindow != 1000000 {
		t.Errorf("primary = %+v, want provider=opencode-go model_id=minimax-m3 wire_format=anthropic context_window=1000000", primary)
	}
	// Temperature/MaxTokens inherited from cfg.Models["default"].
	if primary.Temperature != 0.5 || primary.MaxTokens != 4096 {
		t.Errorf("primary temperature/max_tokens = %v/%v, want inherited 0.5/4096", primary.Temperature, primary.MaxTokens)
	}
}

func TestResolveRequestedModel_GoModelsSnapshotNativeYesRoutesAnthropic(t *testing.T) {
	cfg := &config.Config{RespectRequestedModel: boolPtr(true)}
	atomic := newTestAtomicConfig(cfg)
	r := NewModelRouter(atomic)
	r.SetGoModelsSnapshot(&gomodels.Snapshot{Models: []gomodels.Model{
		// wire_format stays the docs/default truth ("openai"); native_messages
		// "yes" is what the router must translate into "anthropic" routing.
		{ID: "chat-only-model", WireFormat: gomodels.WireFormatOpenAI, NativeMessages: gomodels.NativeSupportYes},
	}})

	result, ok, _ := r.resolveRequestedModel(cfg, "chat-only-model", false)
	if !ok {
		t.Fatal("expected a match")
	}
	if result.Primary.WireFormat != "anthropic" {
		t.Errorf("WireFormat = %q, want %q (derived from native_messages=yes)", result.Primary.WireFormat, "anthropic")
	}
}

func TestResolveRequestedModel_GoModelsSnapshotResponsesFormatPreserved(t *testing.T) {
	cfg := &config.Config{RespectRequestedModel: boolPtr(true)}
	atomic := newTestAtomicConfig(cfg)
	r := NewModelRouter(atomic)
	r.SetGoModelsSnapshot(&gomodels.Snapshot{Models: []gomodels.Model{
		{ID: "grok-4.6", WireFormat: gomodels.WireFormatResponses, NativeMessages: gomodels.NativeSupportNotChecked},
	}})

	result, ok, _ := r.resolveRequestedModel(cfg, "grok-4.6", false)
	if !ok {
		t.Fatal("expected a match")
	}
	if result.Primary.WireFormat != "responses" {
		t.Errorf("WireFormat = %q, want %q", result.Primary.WireFormat, "responses")
	}
}

func TestResolveRequestedModel_HandConfigWinsOverGoModelsSnapshot(t *testing.T) {
	cfg := &config.Config{
		RespectRequestedModel: boolPtr(true),
		Models: map[string]config.ModelConfig{
			"glm-5.3": {Provider: "opencode-go", ModelID: "glm-5.3", WireFormat: "responses", ContextWindow: 999},
		},
	}
	atomic := newTestAtomicConfig(cfg)
	r := NewModelRouter(atomic)
	// The snapshot disagrees with the hand-written entry; hand config must win.
	r.SetGoModelsSnapshot(&gomodels.Snapshot{Models: []gomodels.Model{
		{ID: "glm-5.3", WireFormat: gomodels.WireFormatOpenAI, ContextWindow: 111},
	}})

	result, ok, err := r.resolveRequestedModel(cfg, "glm-5.3", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected a match")
	}
	if result.Primary.WireFormat != "responses" || result.Primary.ContextWindow != 999 {
		t.Errorf("primary = %+v, want the hand-written wire_format=responses context_window=999 (snapshot must not override it)", result.Primary)
	}
}

func TestResolveRequestedModel_UnknownModelNotInSnapshotFallsThrough(t *testing.T) {
	catalogPath := writeTestCatalog(t)
	cfg := &config.Config{
		RespectRequestedModel: boolPtr(true),
		Models: map[string]config.ModelConfig{
			"default": {Provider: "opencode-go", ModelID: "kimi-k2.6", Temperature: 0.5, MaxTokens: 8192},
		},
	}
	atomic := newTestAtomicConfig(cfg)
	r := NewModelRouterWithCatalog(atomic, catalogPath)
	r.SetGoModelsSnapshot(&gomodels.Snapshot{Models: []gomodels.Model{
		{ID: "minimax-m3", WireFormat: gomodels.WireFormatAnthropic},
	}})

	// Not in cfg.Models, not in the snapshot, not in the catalog: existing
	// legacy-unknown-model behavior must still apply unchanged.
	result, ok, err := r.resolveRequestedModel(cfg, "totally-unknown-short-id", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected resolveRequestedModel to match (legacy fallback)")
	}
	if result.Primary.Provider != "opencode-go" || result.Primary.ModelID != "totally-unknown-short-id" {
		t.Errorf("primary = %+v, want provider=opencode-go model_id=totally-unknown-short-id", result.Primary)
	}
}

func TestSetGoModelsSnapshot_UpdatesRoutingImmediately(t *testing.T) {
	cfg := &config.Config{RespectRequestedModel: boolPtr(true)}
	atomic := newTestAtomicConfig(cfg)
	r := NewModelRouter(atomic)

	// Before any snapshot is set, the model is unknown to config/catalog but
	// still resolves via the legacy fallback (no snapshot fallback yet).
	if _, ok, _ := r.resolveRequestedModel(cfg, "minimax-m3", false); !ok {
		t.Fatal("expected the legacy fallback to match even with no snapshot set")
	}

	r.SetGoModelsSnapshot(&gomodels.Snapshot{Models: []gomodels.Model{
		{ID: "minimax-m3", WireFormat: gomodels.WireFormatAnthropic, ContextWindow: 1000000},
	}})
	result, ok, _ := r.resolveRequestedModel(cfg, "minimax-m3", false)
	if !ok || result.Primary.ContextWindow != 1000000 {
		t.Fatalf("expected the new snapshot to take effect immediately, got %+v ok=%v", result.Primary, ok)
	}

	// Swap again — no config reload involved anywhere in this test.
	r.SetGoModelsSnapshot(&gomodels.Snapshot{Models: []gomodels.Model{
		{ID: "minimax-m3", WireFormat: gomodels.WireFormatAnthropic, ContextWindow: 2000000},
	}})
	result, ok, _ = r.resolveRequestedModel(cfg, "minimax-m3", false)
	if !ok || result.Primary.ContextWindow != 2000000 {
		t.Fatalf("expected the second snapshot swap to take effect immediately, got %+v ok=%v", result.Primary, ok)
	}
}

// A "yes" verdict is carried forward across syncs while wire_format is
// re-read from the docs, so a model probed while absent from the docs can
// later be listed as a Responses model with a stale "yes". The docs format
// must win: only a plain OpenAI chat model is upgraded to Anthropic routing.
func TestResolveRequestedModel_GoModelsSnapshotNativeYesIgnoredForResponses(t *testing.T) {
	cfg := &config.Config{RespectRequestedModel: boolPtr(true)}
	r := NewModelRouter(newTestAtomicConfig(cfg))
	r.SetGoModelsSnapshot(&gomodels.Snapshot{Models: []gomodels.Model{
		{ID: "grok-4.6", WireFormat: gomodels.WireFormatResponses, NativeMessages: gomodels.NativeSupportYes},
	}})

	result, ok, _ := r.resolveRequestedModel(cfg, "grok-4.6", false)
	if !ok {
		t.Fatal("expected a match")
	}
	if result.Primary.WireFormat != "responses" {
		t.Errorf("WireFormat = %q, want %q (docs format beats a stale native verdict)", result.Primary.WireFormat, "responses")
	}
}

// go_models.enabled=false (e.g. after a hot reload) must stop the fallback at
// once, without waiting for the refresher's next tick.
func TestResolveRequestedModel_GoModelsSnapshotIgnoredWhenDisabled(t *testing.T) {
	cfg := &config.Config{RespectRequestedModel: boolPtr(true)}
	cfg.GoModels.Enabled = boolPtr(false)
	r := NewModelRouter(newTestAtomicConfig(cfg))
	r.SetGoModelsSnapshot(&gomodels.Snapshot{Models: []gomodels.Model{
		{ID: "snapshot-only", WireFormat: gomodels.WireFormatAnthropic},
	}})

	result, ok, err := r.resolveRequestedModel(cfg, "snapshot-only", false)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want the legacy unknown-model route", ok, err)
	}
	if result.Primary.WireFormat == string(gomodels.WireFormatAnthropic) {
		t.Errorf("primary = %+v, want the snapshot ignored while go_models is disabled", result.Primary)
	}
}

// A snapshot-only model that accepts images must not be rejected for an
// image turn: resolveRequestedModel refuses needsVision when Vision is false,
// and the static registry does not know newly synced models.
func TestResolveRequestedModel_GoModelsSnapshotVision(t *testing.T) {
	cfg := &config.Config{RespectRequestedModel: boolPtr(true)}
	r := NewModelRouter(newTestAtomicConfig(cfg))
	r.SetGoModelsSnapshot(&gomodels.Snapshot{Models: []gomodels.Model{
		{ID: "new-vision-model", WireFormat: gomodels.WireFormatOpenAI, Vision: true},
		{ID: "new-text-model", WireFormat: gomodels.WireFormatOpenAI},
	}})

	if _, ok, err := r.resolveRequestedModel(cfg, "new-vision-model", true); err != nil || !ok {
		t.Errorf("vision model with image turn: ok=%v err=%v, want ok=true err=nil", ok, err)
	}
	if _, _, err := r.resolveRequestedModel(cfg, "new-text-model", true); err == nil {
		t.Error("text-only model with image turn: want a vision error")
	}
}
