package gomodels

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteLoadSnapshot_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := SnapshotPath(dir)

	checkedAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	want := &Snapshot{
		FetchedAt: time.Date(2026, 9, 15, 12, 5, 0, 0, time.UTC),
		Models: []Model{
			{ID: "glm-5.3", Name: "GLM-5.3", WireFormat: WireFormatOpenAI, InDocs: true, ContextWindow: 128000, NativeMessages: NativeSupportNo, NativeCheckedAt: &checkedAt},
			{ID: "minimax-m3", Name: "MiniMax M3", WireFormat: WireFormatAnthropic, InDocs: true, ContextWindow: 1000000, NativeMessages: NativeSupportNotChecked},
		},
	}

	if err := WriteSnapshot(path, want); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}

	got, err := LoadSnapshot(path)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if !got.FetchedAt.Equal(want.FetchedAt) {
		t.Errorf("FetchedAt = %v, want %v", got.FetchedAt, want.FetchedAt)
	}
	if len(got.Models) != len(want.Models) {
		t.Fatalf("got %d models, want %d", len(got.Models), len(want.Models))
	}
	for i := range want.Models {
		g, w := got.Models[i], want.Models[i]
		if g.ID != w.ID || g.Name != w.Name || g.WireFormat != w.WireFormat || g.InDocs != w.InDocs ||
			g.ContextWindow != w.ContextWindow || g.NativeMessages != w.NativeMessages {
			t.Errorf("model %d = %+v, want %+v", i, g, w)
		}
	}
	if got.Models[0].NativeCheckedAt == nil || !got.Models[0].NativeCheckedAt.Equal(checkedAt) {
		t.Errorf("model 0 NativeCheckedAt = %v, want %v", got.Models[0].NativeCheckedAt, checkedAt)
	}
	if got.Models[1].NativeCheckedAt != nil {
		t.Errorf("model 1 NativeCheckedAt = %v, want nil", got.Models[1].NativeCheckedAt)
	}

	// No leftover temp file after a successful write.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file still exists after WriteSnapshot: err=%v", err)
	}
}

func TestLoadSnapshot_MissingFileIsNotAnError(t *testing.T) {
	got, err := LoadSnapshot(filepath.Join(t.TempDir(), "go-models.json"))
	if err != nil {
		t.Fatalf("LoadSnapshot on missing file: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestLoadSnapshot_CorruptFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := SnapshotPath(dir)
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadSnapshot(path); err == nil {
		t.Fatal("expected an error for a corrupt snapshot file")
	}
}

func TestWriteSnapshot_OverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := SnapshotPath(dir)

	first := &Snapshot{FetchedAt: time.Now().UTC(), Models: []Model{{ID: "a"}}}
	if err := WriteSnapshot(path, first); err != nil {
		t.Fatal(err)
	}
	second := &Snapshot{FetchedAt: time.Now().UTC(), Models: []Model{{ID: "a"}, {ID: "b"}}}
	if err := WriteSnapshot(path, second); err != nil {
		t.Fatal(err)
	}

	got, err := LoadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 {
		t.Errorf("got %d models, want 2", len(got.Models))
	}
}
