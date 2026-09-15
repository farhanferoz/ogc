package transformer

import (
	"context"
	"strings"
	"testing"
)

// TestProxyStream_FastPathUnescapesContent checks that the content fast path
// decodes JSON escapes. It slices the raw chunk bytes, so an escaped quote,
// newline or unicode escape used to reach the client as literal backslashes.
func TestProxyStream_FastPathUnescapesContent(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"content":"say \"hi\""}}]}`,
		`{"choices":[{"delta":{"content":"\nsecond line\ttabbed"}}]}`,
		`{"choices":[{"delta":{"content":"café \\ done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "glm-5.3", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	var text strings.Builder
	for _, event := range parseSSEEvents(t, w.buf.String()) {
		if event.Delta != nil && event.Delta.Type == "text_delta" {
			text.WriteString(event.Delta.Text)
		}
	}

	want := "say \"hi\"\nsecond line\ttabbed" + "café \\ done"
	if got := text.String(); got != want {
		t.Errorf("streamed text = %q, want %q", got, want)
	}
}
