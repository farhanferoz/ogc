package gomodels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/routatic/proxy/internal/core"
)

func TestFetchLiveModels_ParsesDataArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"minimax-m3","object":"model","owned_by":"opencode"},{"id":"glm-5.3"}]}`))
	}))
	defer srv.Close()

	ids, err := fetchLiveModels(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetchLiveModels: %v", err)
	}
	want := []string{"minimax-m3", "glm-5.3"}
	if len(ids) != len(want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], want[i])
		}
	}
}

func TestFetchLiveModels_EmptyListIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	if _, err := fetchLiveModels(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected an error for an empty live model list")
	}
}

func TestFetchLiveModels_NonOKStatusIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := fetchLiveModels(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}

func TestFetchLiveModels_MalformedJSONIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	if _, err := fetchLiveModels(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestFetchDocsTable_ParsesEndpointsTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(docsFixture))
	}))
	defer srv.Close()

	models, err := fetchDocsTable(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetchDocsTable: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("got %d models, want 3", len(models))
	}
	if models[1].ID != "glm-5.3" || models[1].WireFormat != core.WireFormatOpenAIChat {
		t.Errorf("models[1] = %+v", models[1])
	}
}

func TestFetchDocsTable_ParseErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# Go\n\nno endpoints section here\n"))
	}))
	defer srv.Close()

	if _, err := fetchDocsTable(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected an error when the docs markdown has no Endpoints table")
	}
}

func TestFetchMetadata_ExtractsOpenCodeGoProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"opencode-go": {
				"models": {
					"glm-5.3": {"name": "GLM 5.3", "limit": {"context": 128000, "output": 8000}}
				}
			},
			"other-provider": {
				"models": {"x": {"name": "X"}}
			}
		}`))
	}))
	defer srv.Close()

	meta, err := fetchMetadata(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetchMetadata: %v", err)
	}
	got, ok := meta["glm-5.3"]
	if !ok {
		t.Fatalf("meta missing glm-5.3: %+v", meta)
	}
	if got.Name != "GLM 5.3" || got.Limit.Context != 128000 {
		t.Errorf("meta[glm-5.3] = %+v", got)
	}
	if _, ok := meta["x"]; ok {
		t.Errorf("meta should not contain other-provider's models: %+v", meta)
	}
}

func TestFetchMetadata_MissingProviderKeyIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"other-provider": {"models": {}}}`))
	}))
	defer srv.Close()

	meta, err := fetchMetadata(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetchMetadata: %v", err)
	}
	if len(meta) != 0 {
		t.Errorf("got %d entries, want 0", len(meta))
	}
}

func TestFetchMetadata_MalformedJSONIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	if _, err := fetchMetadata(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}
