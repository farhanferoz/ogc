package gomodels

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SnapshotFileName is the file Sync writes, beside the proxy's config file.
const SnapshotFileName = "go-models.json"

// WireFormat is the wire format a Go model speaks, stored as a string so the
// snapshot on disk is self-describing; the values match what
// config.ModelConfig.WireFormat accepts (core.ParseWireFormat).
type WireFormat string

const (
	WireFormatOpenAI    WireFormat = "openai"
	WireFormatAnthropic WireFormat = "anthropic"
	WireFormatResponses WireFormat = "responses"
)

// NativeSupport records whether a model has been observed to accept the
// Anthropic Messages wire format directly, for models whose docs-listed
// format is "openai" (see checkNativeMessages).
type NativeSupport string

const (
	NativeSupportYes        NativeSupport = "yes"
	NativeSupportNo         NativeSupport = "no"
	NativeSupportUnknown    NativeSupport = "unknown"
	NativeSupportNotChecked NativeSupport = "not_checked"
)

// Model is one live OpenCode Go model as recorded in a Snapshot.
type Model struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	WireFormat      WireFormat    `json:"wire_format"`
	InDocs          bool          `json:"in_docs"`
	ContextWindow   int           `json:"context_window"`
	Vision          bool          `json:"vision"`
	NativeMessages  NativeSupport `json:"native_messages"`
	NativeCheckedAt *time.Time    `json:"native_checked_at,omitempty"`
}

// Snapshot is the full set of live OpenCode Go models as of FetchedAt.
type Snapshot struct {
	FetchedAt time.Time `json:"fetched_at"`
	Models    []Model   `json:"models"`
}

// SnapshotPath returns the snapshot file path for a proxy config stored in
// configDir.
func SnapshotPath(configDir string) string {
	return filepath.Join(configDir, SnapshotFileName)
}

// LoadSnapshot reads a previously written snapshot from path.
//
// A missing file is not an error: it returns (nil, nil) so callers can treat
// "never synced" the same as "nothing to carry forward".
func LoadSnapshot(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshot %s: %w", path, err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parse snapshot %s: %w", path, err)
	}
	return &snap, nil
}

// WriteSnapshot writes snap to path atomically (temp file + rename),
// creating the parent directory if needed. A reader can never observe a
// partially written snapshot.
func WriteSnapshot(path string, snap *Snapshot) error {
	if snap == nil {
		return fmt.Errorf("snapshot is nil")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	data = append(data, '\n')

	// A unique temp name keeps a CLI "go-models sync" and the serve
	// refresher from writing through the same temp file at once.
	tmpFile, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create snapshot temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	// Removing a path that was already renamed into place is a no-op error.
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write snapshot temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("write snapshot temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return fmt.Errorf("chmod snapshot temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename snapshot file: %w", err)
	}
	return nil
}
