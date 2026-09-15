package gomodels

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/routatic/proxy/internal/core"
)

// nativeCheckMaxAge is how long an "unknown" native-support verdict is
// trusted before a subsequent Sync retries the probe.
const nativeCheckMaxAge = 24 * time.Hour

// Deps are the injectable inputs to Sync: the HTTP client and the URLs of
// the three upstream sources, plus what's needed to run the optional native
// wire-format check. Tests substitute httptest servers for every URL so
// Sync never touches the real network.
type Deps struct {
	HTTPClient *http.Client

	LiveURL     string // GET LiveURL -> {"data":[{"id":...}, ...]}
	DocsURL     string // GET DocsURL -> OpenCode Go docs markdown
	MetadataURL string // GET MetadataURL -> models.dev api.json

	// CheckNative enables the opt-in native wire-format probe (see
	// checkNativeMessages). AnthropicURL and ChatURL are the OpenCode Go
	// upstream's own /v1/messages and /v1/chat/completions endpoints.
	CheckNative  bool
	AnthropicURL string
	ChatURL      string
	APIKey       string
}

// Sync builds a fresh Snapshot from OpenCode Go's live model list, the Go
// docs Endpoints table, and models.dev's metadata, in that dependency
// order: the live list decides which models exist, the docs table decides
// their wire format, and models.dev fills in display names and context
// windows.
//
// If any source fails, Sync returns the error and a nil Snapshot — callers
// must not treat a partial result as usable, and must not overwrite a good
// previous snapshot with it (see SyncAndWrite).
//
// previous, when non-nil, supplies native-check results to carry forward so
// each model is probed at most once until its verdict goes stale.
func Sync(ctx context.Context, deps Deps, previous *Snapshot) (*Snapshot, error) {
	liveIDs, err := fetchLiveModels(ctx, deps.HTTPClient, deps.LiveURL)
	if err != nil {
		return nil, err
	}
	docsModels, err := fetchDocsTable(ctx, deps.HTTPClient, deps.DocsURL)
	if err != nil {
		return nil, err
	}
	metadata, err := fetchMetadata(ctx, deps.HTTPClient, deps.MetadataURL)
	if err != nil {
		return nil, err
	}

	docsByID := make(map[string]DocsModel, len(docsModels))
	for _, m := range docsModels {
		docsByID[m.ID] = m
	}
	previousByID := make(map[string]Model)
	if previous != nil {
		for _, m := range previous.Models {
			previousByID[m.ID] = m
		}
	}

	now := time.Now().UTC()
	models := make([]Model, 0, len(liveIDs))
	for _, id := range liveIDs {
		model := Model{ID: id, NativeMessages: NativeSupportNotChecked}
		if prev, ok := previousByID[id]; ok {
			model.NativeMessages = prev.NativeMessages
			model.NativeCheckedAt = prev.NativeCheckedAt
		}

		if docsModel, ok := docsByID[id]; ok {
			model.InDocs = true
			model.Name = docsModel.Name
			model.WireFormat = wireFormatFromCore(docsModel.WireFormat)
		} else {
			model.WireFormat = WireFormatOpenAI
		}

		if meta, ok := metadata[id]; ok {
			if model.Name == "" {
				model.Name = meta.Name
			}
			model.ContextWindow = meta.Limit.Context
		}
		if model.Name == "" {
			model.Name = id
		}

		// The native check only applies to models the docs (or the default,
		// for models missing from the docs) classify as plain OpenAI chat —
		// a docs-listed anthropic or responses model is never probed.
		if model.WireFormat == WireFormatOpenAI {
			if deps.CheckNative && needsNativeCheck(model, now) {
				model.NativeMessages = checkNativeMessages(ctx, deps.HTTPClient, deps.AnthropicURL, deps.ChatURL, deps.APIKey, id)
				model.NativeCheckedAt = &now
			}
			if model.NativeMessages == NativeSupportYes {
				model.WireFormat = WireFormatAnthropic
			}
		}

		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })

	return &Snapshot{FetchedAt: now, Models: models}, nil
}

// needsNativeCheck reports whether model's native-support verdict should be
// (re)probed: never checked, or "unknown" and stale. "yes" and "no" are
// carried forward indefinitely — each model is checked at most once for a
// definitive verdict.
func needsNativeCheck(model Model, now time.Time) bool {
	switch model.NativeMessages {
	case NativeSupportNotChecked:
		return true
	case NativeSupportUnknown:
		return model.NativeCheckedAt == nil || now.Sub(*model.NativeCheckedAt) >= nativeCheckMaxAge
	default:
		return false
	}
}

// wireFormatFromCore converts a docs-table wire format (an internal/core
// type, since ParseDocsTable already returns one) to the string-based
// WireFormat this package stores in the snapshot. WireFormatGemini never
// appears here — the Go docs Endpoints table has no Gemini endpoint — so it
// falls through to WireFormatOpenAI like any other unrecognised value.
func wireFormatFromCore(w core.WireFormat) WireFormat {
	switch w {
	case core.WireFormatAnthropic:
		return WireFormatAnthropic
	case core.WireFormatOpenAIResponses:
		return WireFormatResponses
	default:
		return WireFormatOpenAI
	}
}

// SyncAndWrite loads the previous snapshot at snapshotPath (if any), builds
// a fresh one via Sync, and atomically writes it back to snapshotPath — but
// only when Sync succeeds, so a bad fetch never clobbers a good snapshot.
//
// A corrupt previous snapshot does not block a fresh sync: it is logged and
// treated as "nothing to carry forward" (every model starts as
// not_checked again).
func SyncAndWrite(ctx context.Context, deps Deps, snapshotPath string) (*Snapshot, error) {
	previous, err := LoadSnapshot(snapshotPath)
	if err != nil {
		slog.Warn("gomodels: ignoring corrupt previous snapshot", "path", snapshotPath, "error", err)
		previous = nil
	}

	snap, err := Sync(ctx, deps, previous)
	if err != nil {
		return nil, err
	}
	if err := WriteSnapshot(snapshotPath, snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// Delta compares two snapshots' model id sets and reports which ids are new
// in current and which have dropped out since previous. previous may be
// nil (first sync ever), in which case every id in current counts as added.
func Delta(previous, current *Snapshot) (added, removed []string) {
	prevIDs := make(map[string]struct{})
	if previous != nil {
		for _, m := range previous.Models {
			prevIDs[m.ID] = struct{}{}
		}
	}
	currIDs := make(map[string]struct{}, len(current.Models))
	for _, m := range current.Models {
		currIDs[m.ID] = struct{}{}
		if _, ok := prevIDs[m.ID]; !ok {
			added = append(added, m.ID)
		}
	}
	for id := range prevIDs {
		if _, ok := currIDs[id]; !ok {
			removed = append(removed, id)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}
