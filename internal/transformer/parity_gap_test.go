package transformer

import (
	"context"
	"strings"
	"testing"
)

// TestProxyStream_BareReasoningFieldEmitsThinking checks ogc's other
// reasoning-token wire shape (delta.reasoning, used by Kimi/DeepSeek R1 per
// ogc's PATCHES.md #3) alongside delta.reasoning_content. A chunk carrying
// only delta.reasoning must produce a thinking block, and the reasoning text
// must never leak out as a visible text_delta.
//
// Ported from the upstream parity-check worktree's
// TestProxyStream_BareReasoningFieldIgnored, which pinned the opposite
// (pre-fix) behaviour: that a bare delta.reasoning chunk produced neither a
// thinking block nor visible text — i.e. it was silently dropped.
func TestProxyStream_BareReasoningFieldEmitsThinking(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"reasoning":"Let me think"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "kimi-k2.6", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	out := w.buf.String()
	if !strings.Contains(out, `"type":"thinking"`) {
		t.Fatalf("expected a thinking block for bare delta.reasoning; got: %s", out)
	}
	if !strings.Contains(out, "Let me think") {
		t.Fatalf("expected the bare delta.reasoning text to reach the client inside the thinking block; got: %s", out)
	}

	events := parseSSEEvents(t, out)
	for _, e := range events {
		if e.Type == "content_block_delta" && e.Delta != nil && e.Delta.Type == "text_delta" {
			t.Fatalf("bare delta.reasoning text leaked as visible text_delta: %+v", e)
		}
	}
}
