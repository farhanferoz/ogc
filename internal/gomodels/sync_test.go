package gomodels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// docsRow renders one Endpoints table row in the shape ParseDocsTable
// expects (see docs_table_test.go's docsFixture).
func docsRow(name, id, endpointPath string) string {
	return fmt.Sprintf("| %s | `%s` | `https://opencode.ai/zen/go/v1/%s` | `@ai-sdk/x` |", name, id, endpointPath)
}

func buildDocsMarkdown(rows ...string) string {
	return "# Go\n\n## Endpoints\n\n" +
		"| Model | Model ID | Endpoint | AI SDK Package |\n" +
		"| --- | --- | --- | --- |\n" +
		strings.Join(rows, "\n") + "\n"
}

func liveModelsJSON(ids ...string) string {
	type entry struct {
		ID string `json:"id"`
	}
	entries := make([]entry, len(ids))
	for i, id := range ids {
		entries[i] = entry{ID: id}
	}
	body, err := json.Marshal(map[string]any{"data": entries})
	if err != nil {
		panic(err)
	}
	return string(body)
}

func metadataJSON(t *testing.T, models map[string]modelMetadata) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		modelsDevProvider: map[string]any{"models": models},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// staticServer returns an httptest server that always serves body with a 200.
func staticServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func failingServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func modelByID(t *testing.T, snap *Snapshot, id string) Model {
	t.Helper()
	for _, m := range snap.Models {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("snapshot has no model %q: %+v", id, snap.Models)
	return Model{}
}

func TestSync_FormatPrecedenceAndFallbacks(t *testing.T) {
	live := staticServer(t, liveModelsJSON("glm-5.3", "minimax-m3", "mystery-model"))
	docs := staticServer(t, buildDocsMarkdown(
		docsRow("GLM-5.3", "glm-5.3", "chat/completions"),
		docsRow("MiniMax M3", "minimax-m3", "messages"),
		// mystery-model intentionally absent: a live model missing from docs.
	))
	meta := staticServer(t, metadataJSON(t, map[string]modelMetadata{
		"glm-5.3": {Name: "GLM 5.3 (models.dev)", Limit: struct {
			Context int `json:"context"`
			Output  int `json:"output"`
		}{Context: 128000, Output: 8000}},
		"mystery-model": {Name: "Mystery", Limit: struct {
			Context int `json:"context"`
			Output  int `json:"output"`
		}{Context: 32000}},
		// minimax-m3 intentionally absent from metadata too.
	}))

	deps := Deps{HTTPClient: live.Client(), LiveURL: live.URL, DocsURL: docs.URL, MetadataURL: meta.URL}
	snap, err := Sync(context.Background(), deps, nil)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(snap.Models) != 3 {
		t.Fatalf("got %d models, want 3: %+v", len(snap.Models), snap.Models)
	}

	glm := modelByID(t, snap, "glm-5.3")
	if !glm.InDocs || glm.WireFormat != WireFormatOpenAI {
		t.Errorf("glm-5.3 = %+v, want in_docs=true wire_format=openai", glm)
	}
	if glm.Name != "GLM-5.3" {
		t.Errorf("glm-5.3 name = %q, want docs name %q (docs beats models.dev)", glm.Name, "GLM-5.3")
	}
	if glm.ContextWindow != 128000 {
		t.Errorf("glm-5.3 context = %d, want 128000", glm.ContextWindow)
	}

	minimax := modelByID(t, snap, "minimax-m3")
	if !minimax.InDocs || minimax.WireFormat != WireFormatAnthropic {
		t.Errorf("minimax-m3 = %+v, want in_docs=true wire_format=anthropic", minimax)
	}
	if minimax.ContextWindow != 0 {
		t.Errorf("minimax-m3 context = %d, want 0 (no metadata entry)", minimax.ContextWindow)
	}

	mystery := modelByID(t, snap, "mystery-model")
	if mystery.InDocs {
		t.Errorf("mystery-model should not be in_docs")
	}
	if mystery.WireFormat != WireFormatOpenAI {
		t.Errorf("mystery-model wire_format = %q, want openai default for a live model missing from docs", mystery.WireFormat)
	}
	if mystery.Name != "Mystery" {
		t.Errorf("mystery-model name = %q, want models.dev name %q (no docs name, falls back to models.dev)", mystery.Name, "Mystery")
	}
	if mystery.ContextWindow != 32000 {
		t.Errorf("mystery-model context = %d, want 32000", mystery.ContextWindow)
	}
}

func TestSync_NoNameAnywhereFallsBackToID(t *testing.T) {
	live := staticServer(t, liveModelsJSON("no-info-model"))
	docs := staticServer(t, buildDocsMarkdown(docsRow("Unrelated", "other-model", "chat/completions")))
	meta := staticServer(t, metadataJSON(t, map[string]modelMetadata{}))

	deps := Deps{HTTPClient: live.Client(), LiveURL: live.URL, DocsURL: docs.URL, MetadataURL: meta.URL}
	snap, err := Sync(context.Background(), deps, nil)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	m := modelByID(t, snap, "no-info-model")
	if m.Name != "no-info-model" {
		t.Errorf("name = %q, want the id as a last resort", m.Name)
	}
	if m.InDocs || m.WireFormat != WireFormatOpenAI || m.ContextWindow != 0 {
		t.Errorf("no-info-model = %+v, want in_docs=false wire_format=openai context=0", m)
	}
}

func TestSync_AnyFailingSourceReturnsErrorAndNoSnapshot(t *testing.T) {
	live := staticServer(t, liveModelsJSON("glm-5.3"))
	docs := staticServer(t, buildDocsMarkdown(docsRow("GLM-5.3", "glm-5.3", "chat/completions")))
	badMeta := failingServer(t)

	deps := Deps{HTTPClient: live.Client(), LiveURL: live.URL, DocsURL: docs.URL, MetadataURL: badMeta.URL}
	snap, err := Sync(context.Background(), deps, nil)
	if err == nil {
		t.Fatal("expected an error when the metadata source fails")
	}
	if snap != nil {
		t.Errorf("expected a nil snapshot on failure, got %+v", snap)
	}
}

func TestSyncAndWrite_ResultReportsDeltaWithoutASecondFileLoad(t *testing.T) {
	dir := t.TempDir()
	path := SnapshotPath(dir)

	firstLive := staticServer(t, liveModelsJSON("glm-5.3"))
	docs := staticServer(t, buildDocsMarkdown(docsRow("GLM-5.3", "glm-5.3", "chat/completions")))
	meta := staticServer(t, metadataJSON(t, map[string]modelMetadata{}))
	deps := Deps{HTTPClient: firstLive.Client(), LiveURL: firstLive.URL, DocsURL: docs.URL, MetadataURL: meta.URL}

	first, err := SyncAndWrite(context.Background(), deps, path)
	if err != nil {
		t.Fatalf("first SyncAndWrite: %v", err)
	}
	if len(first.Added) != 1 || first.Added[0] != "glm-5.3" || len(first.Removed) != 0 {
		t.Errorf("first sync result = %+v, want added=[glm-5.3] removed=[]", first)
	}

	secondLive := staticServer(t, liveModelsJSON("minimax-m3"))
	deps.LiveURL = secondLive.URL
	deps.HTTPClient = secondLive.Client()

	second, err := SyncAndWrite(context.Background(), deps, path)
	if err != nil {
		t.Fatalf("second SyncAndWrite: %v", err)
	}
	if len(second.Added) != 1 || second.Added[0] != "minimax-m3" {
		t.Errorf("second sync Added = %v, want [minimax-m3]", second.Added)
	}
	if len(second.Removed) != 1 || second.Removed[0] != "glm-5.3" {
		t.Errorf("second sync Removed = %v, want [glm-5.3]", second.Removed)
	}
}

func TestSyncAndWrite_FailingSourceLeavesOldFileUntouched(t *testing.T) {
	dir := t.TempDir()
	path := SnapshotPath(dir)

	live := staticServer(t, liveModelsJSON("glm-5.3"))
	docs := staticServer(t, buildDocsMarkdown(docsRow("GLM-5.3", "glm-5.3", "chat/completions")))
	meta := staticServer(t, metadataJSON(t, map[string]modelMetadata{}))
	goodDeps := Deps{HTTPClient: live.Client(), LiveURL: live.URL, DocsURL: docs.URL, MetadataURL: meta.URL}

	if _, err := SyncAndWrite(context.Background(), goodDeps, path); err != nil {
		t.Fatalf("initial SyncAndWrite: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	badDocs := failingServer(t)
	badDeps := goodDeps
	badDeps.DocsURL = badDocs.URL
	if _, err := SyncAndWrite(context.Background(), badDeps, path); err == nil {
		t.Fatal("expected SyncAndWrite to fail when a source fails")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("snapshot file changed after a failed sync:\nbefore: %s\nafter:  %s", before, after)
	}
}

// nativeCheckRouter dispatches native-check probes by the "model" field in
// the request body, so each model can be given its own verdict in one
// server. It also counts calls per model so a test can prove a model was
// (or was not) reprobed.
type nativeCheckRouter struct {
	mu       sync.Mutex
	statuses map[string]int // model -> HTTP status this endpoint returns for it
	calls    map[string]int
}

func newNativeCheckRouter(statuses map[string]int) *nativeCheckRouter {
	return &nativeCheckRouter{statuses: statuses, calls: make(map[string]int)}
}

func (r *nativeCheckRouter) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body nativeCheckRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode probe body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.calls[body.Model]++
		status, ok := r.statuses[body.Model]
		r.mu.Unlock()
		if !ok {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
	}
}

func (r *nativeCheckRouter) callCount(model string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[model]
}

func TestSync_NativeCheckOutcomesAndCarryForward(t *testing.T) {
	live := staticServer(t, liveModelsJSON("yes-model", "no-model", "unknown-model"))
	// None of the three appear in the docs table, so all default to
	// wire_format openai and are eligible for the native check.
	docs := staticServer(t, buildDocsMarkdown(docsRow("Unrelated", "other-model", "chat/completions")))
	meta := staticServer(t, metadataJSON(t, map[string]modelMetadata{}))

	anthropicRouter := newNativeCheckRouter(map[string]int{"yes-model": http.StatusOK})
	anthropicSrv := httptest.NewServer(anthropicRouter.handler(t))
	defer anthropicSrv.Close()

	chatRouter := newNativeCheckRouter(map[string]int{"no-model": http.StatusOK})
	chatSrv := httptest.NewServer(chatRouter.handler(t))
	defer chatSrv.Close()

	deps := Deps{
		HTTPClient: live.Client(), LiveURL: live.URL, DocsURL: docs.URL, MetadataURL: meta.URL,
		CheckNative: true, AnthropicURL: anthropicSrv.URL, ChatURL: chatSrv.URL, APIKey: "key",
	}

	snap, err := Sync(context.Background(), deps, nil)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	yes := modelByID(t, snap, "yes-model")
	// wire_format stays the docs/default truth ("openai" here, since
	// yes-model isn't in the docs table); native_messages alone carries the
	// check's verdict. The config loader — not the snapshot — derives
	// "anthropic" routing from native_messages: "yes".
	if yes.NativeMessages != NativeSupportYes || yes.WireFormat != WireFormatOpenAI {
		t.Errorf("yes-model = %+v, want native=yes wire_format=openai (unchanged)", yes)
	}
	if yes.NativeCheckedAt == nil {
		t.Error("yes-model NativeCheckedAt should be set")
	}

	no := modelByID(t, snap, "no-model")
	if no.NativeMessages != NativeSupportNo || no.WireFormat != WireFormatOpenAI {
		t.Errorf("no-model = %+v, want native=no wire_format=openai", no)
	}

	unknown := modelByID(t, snap, "unknown-model")
	if unknown.NativeMessages != NativeSupportUnknown || unknown.WireFormat != WireFormatOpenAI {
		t.Errorf("unknown-model = %+v, want native=unknown wire_format=openai", unknown)
	}

	for _, model := range []string{"yes-model", "no-model", "unknown-model"} {
		if got := anthropicRouter.callCount(model); got != 1 {
			t.Errorf("anthropic probe count for %s = %d, want 1", model, got)
		}
	}
	if got := chatRouter.callCount("yes-model"); got != 0 {
		t.Errorf("chat/completions should not be probed for yes-model, got %d calls", got)
	}

	// A second sync, carrying the first snapshot forward: yes/no verdicts
	// must not be reprobed at all (checked once), and the still-fresh
	// unknown verdict must not be reprobed either (retried only once stale).
	snap2, err := Sync(context.Background(), deps, snap)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	yes2 := modelByID(t, snap2, "yes-model")
	if yes2.WireFormat != WireFormatOpenAI || yes2.NativeMessages != NativeSupportYes {
		t.Errorf("carried-forward yes-model = %+v", yes2)
	}
	if !yes2.NativeCheckedAt.Equal(*yes.NativeCheckedAt) {
		t.Errorf("yes-model NativeCheckedAt changed on a carry-forward sync: %v -> %v", yes.NativeCheckedAt, yes2.NativeCheckedAt)
	}

	for _, model := range []string{"yes-model", "no-model", "unknown-model"} {
		if got := anthropicRouter.callCount(model); got != 1 {
			t.Errorf("anthropic probe count for %s after second sync = %d, want still 1 (no reprobe)", model, got)
		}
	}
}

func TestDelta_AddedAndRemoved(t *testing.T) {
	previous := &Snapshot{Models: []Model{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	current := &Snapshot{Models: []Model{{ID: "b"}, {ID: "c"}, {ID: "d"}}}

	added, removed := Delta(previous, current)
	if len(added) != 1 || added[0] != "d" {
		t.Errorf("added = %v, want [d]", added)
	}
	if len(removed) != 1 || removed[0] != "a" {
		t.Errorf("removed = %v, want [a]", removed)
	}
}

func TestDelta_NilPreviousMeansEverythingIsAdded(t *testing.T) {
	current := &Snapshot{Models: []Model{{ID: "a"}, {ID: "b"}}}
	added, removed := Delta(nil, current)
	if len(added) != 2 {
		t.Errorf("added = %v, want 2 entries", added)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want none", removed)
	}
}
