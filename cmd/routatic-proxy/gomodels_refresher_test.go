package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/internal/gomodels"
)

// TestStartGoModelsRefresher_StopCancelsInFlightSync checks that stopping the
// refresher does not wait for a sync that is stuck on a slow source: shutdown
// must stay within the service manager's stop timeout.
func TestStartGoModelsRefresher_StopCancelsInFlightSync(t *testing.T) {
	requested := make(chan struct{}, 1)
	release := make(chan struct{})
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requested <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		source.Close()
	})

	cfg := &config.Config{
		GoModels: config.GoModelsConfig{
			ModelsURL:   source.URL + "/models",
			DocsURL:     source.URL + "/docs",
			MetadataURL: source.URL + "/api.json",
		},
	}
	atomicCfg := config.NewAtomicConfig(cfg, filepath.Join(t.TempDir(), "config.json"))

	stop := startGoModelsRefresher(atomicCfg, func(*gomodels.Snapshot) {})
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		t.Fatal("refresher never requested the model list")
	}

	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop waited for the in-flight sync instead of cancelling it")
	}
}
