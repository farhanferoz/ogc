package router

import (
	"errors"
	"strings"
	"testing"

	"github.com/routatic/proxy/internal/config"
)

// TestFilterByCapacity_NoVisionModelIsItsOwnError separates "you pasted an
// image at a model that cannot see" from a genuine capacity problem, so the
// handler can answer 400 with the reason instead of 500 "routing failed".
func TestFilterByCapacity_NoVisionModelIsItsOwnError(t *testing.T) {
	// Model ids the built-in registry does not know, so ResolveModelConfig
	// cannot fill in a Vision capability behind the test's back.
	chain := []config.ModelConfig{
		{Provider: "opencode-go", ModelID: "text-only-alpha", ContextWindow: 1_000_000},
		{Provider: "opencode-go", ModelID: "text-only-beta", ContextWindow: 1_000_000},
	}

	_, err := FilterByCapacity(chain, 100, 1024, true, false)
	if !errors.Is(err, ErrNoVisionModel) {
		t.Fatalf("error = %v, want ErrNoVisionModel", err)
	}
	for _, want := range []string{"text-only-alpha", "text-only-beta", "vision"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err.Error(), want)
		}
	}
}

// TestFilterByCapacity_ContextExhaustionIsNotAVisionError keeps the distinct
// error narrow: a chain skipped for context must not claim a vision problem.
func TestFilterByCapacity_ContextExhaustionIsNotAVisionError(t *testing.T) {
	chain := []config.ModelConfig{{Provider: "opencode-go", ModelID: "text-only-alpha", ContextWindow: 1000, Vision: true}}

	_, err := FilterByCapacity(chain, 100_000, 1024, true, false)
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrNoVisionModel) {
		t.Errorf("error = %v, want a plain capacity error", err)
	}
}
