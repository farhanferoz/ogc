package transformer

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
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

	err := handler.ProxyStream(writer, body, "glm-5.3", context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := rec.Body.String()
	if !strings.Contains(out, "message_start") {
		t.Errorf("missing message_start in output: %s", out)
	}
	if !strings.Contains(out, "Thinking about 2+2...") {
		t.Errorf("missing reasoning delta in output: %s", out)
	}
	if !strings.Contains(out, `"text":"4"`) {
		t.Errorf("missing text delta in output: %s", out)
	}
	if !strings.Contains(out, "content_block_stop") {
		t.Errorf("missing content_block_stop in output: %s", out)
	}
	if !strings.Contains(out, "message_stop") {
		t.Errorf("missing message_stop in output: %s", out)
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

	err := handler.ProxyStream(writer, body, "qwen3.8-max", context.Background())
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

	err := handler.ProxyStream(writer, body, "glm-5.3", context.Background())
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

	err := handler.ProxyStream(writer, body, "glm-5.3", context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := rec.Body.String()
	if !strings.Contains(out, `he said \"hello world\"\nand goodbye.`) {
		t.Errorf("expected full escaped string with quotes preserved, got: %s", out)
	}
}
