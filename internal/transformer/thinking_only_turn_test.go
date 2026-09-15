package transformer

import (
	"context"
	"testing"
)

// TestProxyStream_ThinkingOnlyTurnStillHasATextBlock covers a turn whose whole
// content is reasoning: ogc closed such a stream with an empty visible text
// block so Claude Code did not render "[Your previous response had no visible
// output]". Nothing carried that over, and a tool-less thinking-only turn is
// exactly what several OpenCode Go models produce.
func TestProxyStream_ThinkingOnlyTurnStillHasATextBlock(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"reasoning_content":"thinking it through"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "deepseek-v4-flash", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	events := parseSSEEvents(t, w.buf.String())
	var textBlocks, thinkingBlocks int
	for _, event := range events {
		if event.Type != "content_block_start" || event.ContentBlock == nil {
			continue
		}
		switch event.ContentBlock.Type {
		case "text":
			textBlocks++
		case "thinking":
			thinkingBlocks++
		}
	}
	if thinkingBlocks != 1 {
		t.Errorf("thinking blocks = %d, want 1", thinkingBlocks)
	}
	if textBlocks != 1 {
		t.Errorf("text blocks = %d, want 1 (the visible-output fallback)", textBlocks)
	}

	// Every started block must be closed, and the stream must still end.
	starts, stops := 0, 0
	for _, event := range events {
		switch event.Type {
		case "content_block_start":
			starts++
		case "content_block_stop":
			stops++
		}
	}
	if starts != stops {
		t.Errorf("content_block_start = %d, content_block_stop = %d, want equal", starts, stops)
	}
	if last := events[len(events)-1]; last.Type != "message_stop" {
		t.Errorf("last event = %q, want message_stop", last.Type)
	}
}

// TestProxyStream_ToolOnlyTurnGetsNoFallbackText keeps the fallback narrow: a
// turn that called a tool is visible in Claude Code already.
func TestProxyStream_ToolOnlyTurnGetsNoFallbackText(t *testing.T) {
	handler := NewStreamHandler()
	w := newMockResponseWriter()
	body := sseLines(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"Read","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := handler.ProxyStream(w, body, "glm-5.3", ctx, 0, cancel); err != nil {
		t.Fatalf("ProxyStream error: %v", err)
	}

	for _, event := range parseSSEEvents(t, w.buf.String()) {
		if event.Type == "content_block_start" && event.ContentBlock != nil && event.ContentBlock.Type == "text" {
			t.Errorf("tool-only turn got a fallback text block: %s", w.buf.String())
		}
	}
}
