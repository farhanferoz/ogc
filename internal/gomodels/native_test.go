package gomodels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fixedStatusServer returns a server that always answers with status,
// recording every request path it received.
func fixedStatusServer(t *testing.T, status int) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if got := r.Header.Get("x-opencode-session"); got != nativeCheckSessionID {
			t.Errorf("x-opencode-session = %q, want %q", got, nativeCheckSessionID)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func TestCheckNativeMessages_MessagesEndpointAccepts(t *testing.T) {
	anthropic, _ := fixedStatusServer(t, http.StatusOK)
	chat, chatPaths := fixedStatusServer(t, http.StatusInternalServerError)

	got := checkNativeMessages(context.Background(), anthropic.Client(), anthropic.URL, chat.URL, "key", "minimax-m3")
	if got != NativeSupportYes {
		t.Errorf("got %q, want %q", got, NativeSupportYes)
	}
	if len(*chatPaths) != 0 {
		t.Errorf("chat/completions should not be probed once /messages succeeds, got %v", *chatPaths)
	}
}

func TestCheckNativeMessages_ChatOnlyModelFallsBackToChat(t *testing.T) {
	// Verified behavior (2026-09-15): a chat-only model returns 500 on
	// /messages but 200 on /chat/completions.
	anthropic, _ := fixedStatusServer(t, http.StatusInternalServerError)
	chat, _ := fixedStatusServer(t, http.StatusOK)

	got := checkNativeMessages(context.Background(), anthropic.Client(), anthropic.URL, chat.URL, "key", "glm-5.3")
	if got != NativeSupportNo {
		t.Errorf("got %q, want %q", got, NativeSupportNo)
	}
}

func TestCheckNativeMessages_BothEndpointsFailIsUnknown(t *testing.T) {
	anthropic, _ := fixedStatusServer(t, http.StatusBadRequest)
	chat, _ := fixedStatusServer(t, http.StatusBadRequest)

	got := checkNativeMessages(context.Background(), anthropic.Client(), anthropic.URL, chat.URL, "key", "some-model")
	if got != NativeSupportUnknown {
		t.Errorf("got %q, want %q", got, NativeSupportUnknown)
	}
}

func TestCheckNativeMessages_SendsCorrectAuthHeaders(t *testing.T) {
	var gotAPIKey, gotVersion, gotAuth string
	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		w.WriteHeader(http.StatusOK)
	}))
	defer anthropic.Close()
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer chat.Close()

	checkNativeMessages(context.Background(), anthropic.Client(), anthropic.URL, chat.URL, "secret-key", "m")

	if gotAPIKey != "secret-key" {
		t.Errorf("x-api-key = %q, want %q", gotAPIKey, "secret-key")
	}
	if gotVersion != anthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", gotVersion, anthropicVersion)
	}
	if gotAuth != "" {
		t.Errorf("chat/completions should not be reached when /messages succeeds, got Authorization=%q", gotAuth)
	}
}

func TestNeedsNativeCheck_NotCheckedAlwaysNeedsProbe(t *testing.T) {
	m := Model{NativeMessages: NativeSupportNotChecked}
	if !needsNativeCheck(m, time.Now()) {
		t.Error("not_checked should always need a probe")
	}
}

func TestNeedsNativeCheck_YesAndNoAreNeverReprobed(t *testing.T) {
	for _, support := range []NativeSupport{NativeSupportYes, NativeSupportNo} {
		old := time.Now().Add(-365 * 24 * time.Hour)
		m := Model{NativeMessages: support, NativeCheckedAt: &old}
		if needsNativeCheck(m, time.Now()) {
			t.Errorf("%q should never be reprobed, even when old", support)
		}
	}
}

func TestNeedsNativeCheck_UnknownRetriesAfter24Hours(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-1 * time.Hour)
	stale := now.Add(-25 * time.Hour)

	if needsNativeCheck(Model{NativeMessages: NativeSupportUnknown, NativeCheckedAt: &fresh}, now) {
		t.Error("a fresh unknown verdict should not be reprobed yet")
	}
	if !needsNativeCheck(Model{NativeMessages: NativeSupportUnknown, NativeCheckedAt: &stale}, now) {
		t.Error("a stale (>24h) unknown verdict should be reprobed")
	}
	if !needsNativeCheck(Model{NativeMessages: NativeSupportUnknown, NativeCheckedAt: nil}, now) {
		t.Error("an unknown verdict with no timestamp should be reprobed")
	}
}

// An unanswered, rate-limited or timed-out /messages probe must not become a
// permanent "no" just because /chat/completions answered: "no" is never
// re-probed.
func TestCheckNativeMessages_UnansweredMessagesIsUnknown(t *testing.T) {
	chat, _ := fixedStatusServer(t, http.StatusOK)
	for _, status := range []int{http.StatusTooManyRequests, http.StatusRequestTimeout} {
		anthropic, _ := fixedStatusServer(t, status)
		if got := checkNativeMessages(context.Background(), anthropic.Client(), anthropic.URL, chat.URL, "key", "m"); got != NativeSupportUnknown {
			t.Errorf("/messages %d, chat 200: got %q, want %q", status, got, NativeSupportUnknown)
		}
	}

	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	t.Cleanup(hanging.Close)
	hc := &http.Client{Timeout: 100 * time.Millisecond}
	if got := checkNativeMessages(context.Background(), hc, hanging.URL, chat.URL, "key", "m"); got != NativeSupportUnknown {
		t.Errorf("/messages client timeout, chat 200: got %q, want %q", got, NativeSupportUnknown)
	}
}
