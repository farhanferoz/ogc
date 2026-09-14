package transformer

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"
)

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushRecorder) Flush() {
	f.flushed = true
}

func TestProxyStream_TextAndReasoning(t *testing.T) {
	handler := NewStreamHandler()

	sseData := `data: {"id":"1","choices":[{"delta":{"reasoning_content":"Thinking about 2+2..."}}]}

data: {"id":"1","choices":[{"delta":{"content":"4"}}]}

data: {"id":"1","choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sseData))
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := NewSSEWriter(rec)

	err := handler.ProxyStream(writer, body, "glm-5.3", context.Background(), 1250)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := rec.Body.String()
	if !strings.Contains(out, "message_start") {
		t.Errorf("missing message_start in output: %s", out)
	}
	if !strings.Contains(out, `"input_tokens":1250`) {
		t.Errorf("missing input_tokens in message_start: %s", out)
	}
	if !strings.Contains(out, `"type":"thinking"`) {
		t.Errorf("missing thinking block start in output: %s", out)
	}
	if !strings.Contains(out, `"thinking":"Thinking about 2+2..."`) {
		t.Errorf("missing thinking delta in output: %s", out)
	}
	if !strings.Contains(out, `"text":"4"`) {
		t.Errorf("missing text delta in output: %s", out)
	}
	if !strings.Contains(out, `"type":"signature_delta"`) {
		t.Errorf("missing signature_delta in output: %s", out)
	}
	if !strings.Contains(out, `"signature":"proxy-thinking-placeholder"`) {
		t.Errorf("missing placeholder signature in output: %s", out)
	}
	if !strings.Contains(out, "content_block_stop") {
		t.Errorf("missing content_block_stop in output: %s", out)
	}
	if !strings.Contains(out, "message_stop") {
		t.Errorf("missing message_stop in output: %s", out)
	}
}

func TestProxyStream_ReasoningOnlyEmitsVisibleFallbackText(t *testing.T) {
	handler := NewStreamHandler()

	sseData := `data: {"id":"1","choices":[{"delta":{"reasoning_content":"Thinking silently..."}}]}

data: {"id":"1","choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sseData))
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := NewSSEWriter(rec)

	err := handler.ProxyStream(writer, body, "deepseek-v4-flash", context.Background(), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := rec.Body.String()
	if !strings.Contains(out, `"type":"signature_delta"`) {
		t.Errorf("missing signature_delta in reasoning-only output: %s", out)
	}
	if !strings.Contains(out, `"type":"text"`) {
		t.Errorf("missing fallback text block in reasoning-only output: %s", out)
	}
}

func TestProxyStream_ToolCalling(t *testing.T) {
	handler := NewStreamHandler()

	sseData := `data: {"id":"1","choices":[{"delta":{"content":"Running command"}}]}

data: {"id":"1","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"Bash","arguments":"{\"command\":"}}]}}]}

data: {"id":"1","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}

data: {"id":"1","choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sseData))
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := NewSSEWriter(rec)

	err := handler.ProxyStream(writer, body, "qwen3.8-max", context.Background(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := rec.Body.String()
	if !strings.Contains(out, `"type":"tool_use"`) {
		t.Errorf("missing tool_use block start: %s", out)
	}
	if !strings.Contains(out, `"name":"Bash"`) {
		t.Errorf("missing tool name: %s", out)
	}
	if !strings.Contains(out, `"partial_json":"{\"command\":"`) {
		t.Errorf("missing first argument chunk: %s", out)
	}
	if !strings.Contains(out, `"partial_json":"\"ls\"}"`) {
		t.Errorf("missing second argument chunk: %s", out)
	}
	if !strings.Contains(out, `"stop_reason":"tool_use"`) {
		t.Errorf("missing tool_use stop reason: %s", out)
	}
}

func TestProxyStream_MultipleToolCalls(t *testing.T) {
	handler := NewStreamHandler()

	sseData := `data: {"id":"1","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"ReadFile","arguments":"{\"path\":\"a.txt\"}"}}]}}]}

data: {"id":"1","choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"ReadFile","arguments":"{\"path\":\"b.txt\"}"}}]}}]}

data: {"id":"1","choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sseData))
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := NewSSEWriter(rec)

	err := handler.ProxyStream(writer, body, "glm-5.3", context.Background(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := rec.Body.String()
	if !strings.Contains(out, "call_1") {
		t.Errorf("missing call_1 in output: %s", out)
	}
	if !strings.Contains(out, "call_2") {
		t.Errorf("missing call_2 in output: %s", out)
	}
	if !strings.Contains(out, `a.txt`) || !strings.Contains(out, `b.txt`) {
		t.Errorf("missing file paths in output: %s", out)
	}
}

func TestSSEWriter_PingAndConcurrency(t *testing.T) {
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := NewSSEWriter(rec)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = writer.Ping()
		}()
	}
	wg.Wait()

	out := rec.Body.String()
	count := strings.Count(out, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
	if count != 20 {
		t.Errorf("expected 20 ping events, got %d. Output: %s", count, out)
	}
}

func TestProxyStream_EscapedQuotesAndNewlines(t *testing.T) {
	handler := NewStreamHandler()

	sseData := `data: {"id":"1","choices":[{"delta":{"content":"he said \"hello world\"\nand goodbye."}}]}

data: {"id":"1","choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]
`
	body := io.NopCloser(strings.NewReader(sseData))
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := NewSSEWriter(rec)

	err := handler.ProxyStream(writer, body, "glm-5.3", context.Background(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := rec.Body.String()
	if !strings.Contains(out, `he said \"hello world\"\nand goodbye.`) {
		t.Errorf("expected full escaped string with quotes preserved, got: %s", out)
	}
}

func TestProxyStream_TruncatedStreamIsError(t *testing.T) {
	handler := NewStreamHandler()

	sseData := `data: {"id":"1","choices":[{"delta":{"reasoning_content":"Thinking..."}}]}
`
	body := io.NopCloser(strings.NewReader(sseData))
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := NewSSEWriter(rec)

	err := handler.ProxyStream(writer, body, "deepseek-v4-flash", context.Background(), 0)
	if err == nil {
		t.Fatalf("expected error for stream without finish_reason")
	}
	if strings.Contains(rec.Body.String(), "message_stop") {
		t.Errorf("truncated stream must not be closed as a complete message: %s", rec.Body.String())
	}
}

func TestProxyStream_InStreamErrorIsError(t *testing.T) {
	handler := NewStreamHandler()

	sseData := `data: {"id":"1","choices":[{"delta":{"content":"partial"}}]}

data: {"error":{"type":"api_error","message":"upstream overloaded"}}
`
	body := io.NopCloser(strings.NewReader(sseData))
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := NewSSEWriter(rec)

	err := handler.ProxyStream(writer, body, "glm-5.3", context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "upstream overloaded") {
		t.Fatalf("expected upstream error to surface, got: %v", err)
	}
}

func TestRelayAnthropicStream_EventsStayWholeUnderPings(t *testing.T) {
	var upstream strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&upstream, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"chunk %d\"}}\n\n", i)
	}
	upstream.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	writer := NewSSEWriter(rec)

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = writer.Ping()
				time.Sleep(50 * time.Microsecond)
			}
		}
	}()

	// One byte per read gives pings every chance to land mid-event.
	err := RelayAnthropicStream(writer, iotest.OneByteReader(strings.NewReader(upstream.String())))
	close(done)
	wg.Wait()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := rec.Body.String()
	if got := strings.Count(out, "event: content_block_delta"); got != 200 {
		t.Errorf("expected 200 relayed events, got %d", got)
	}
	for _, ev := range strings.Split(strings.TrimSpace(out), "\n\n") {
		lines := strings.Split(ev, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("corrupted SSE event: %q", ev)
		}
	}
}

func TestRelayAnthropicStream_TruncatedStreamIsError(t *testing.T) {
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	upstream := strings.NewReader("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")

	if err := RelayAnthropicStream(NewSSEWriter(rec), upstream); err == nil {
		t.Fatalf("expected error for stream ending before message_stop")
	}
}
