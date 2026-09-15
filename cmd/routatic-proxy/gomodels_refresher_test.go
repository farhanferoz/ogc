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

// TestStartGoModelsRefresher_PublishesSyncedSnapshot checks the refresher's
// reason to exist: a successful sync reaches the router and the disk.
func TestStartGoModelsRefresher_PublishesSyncedSnapshot(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.3"}]}`))
	})
	mux.HandleFunc("/docs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# Go\n\n## Endpoints\n\n" +
			"| Model | Model ID | Endpoint | AI SDK Package |\n| --- | --- | --- | --- |\n" +
			"| GLM-5.3 | `glm-5.3` | `https://opencode.ai/zen/go/v1/chat/completions` | `@ai-sdk/x` |\n"))
	})
	mux.HandleFunc("/api.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	source := httptest.NewServer(mux)
	t.Cleanup(source.Close)

	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := &config.Config{
		GoModels: config.GoModelsConfig{
			RefreshHours: 1,
			ModelsURL:    source.URL + "/models",
			DocsURL:      source.URL + "/docs",
			MetadataURL:  source.URL + "/api.json",
		},
	}
	atomicCfg := config.NewAtomicConfig(cfg, configPath)

	published := make(chan *gomodels.Snapshot, 1)
	stop := startGoModelsRefresher(atomicCfg, func(snap *gomodels.Snapshot) {
		select {
		case published <- snap:
		default:
		}
	})
	defer stop()

	select {
	case snap := <-published:
		if snap == nil || len(snap.Models) != 1 || snap.Models[0].ID != "glm-5.3" {
			t.Fatalf("published snapshot = %+v, want one model glm-5.3", snap)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refresher never published a snapshot")
	}

	onDisk, err := gomodels.LoadSnapshot(gomodels.SnapshotPath(filepath.Dir(configPath)))
	if err != nil || onDisk == nil || len(onDisk.Models) != 1 {
		t.Errorf("snapshot on disk = %+v, err = %v, want the synced model", onDisk, err)
	}
}
