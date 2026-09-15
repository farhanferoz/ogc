package transformer

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/pkg/types"
)

// answeredToolCallIDs returns, for the assistant message at index i, the
// tool_call_id of every "tool" role message immediately following it (up to
// the next assistant message), in the order they appear.
func answeredToolCallIDs(messages []types.ChatMessage, i int) []string {
	var ids []string
	for j := i + 1; j < len(messages) && messages[j].Role != "assistant"; j++ {
		if messages[j].Role == "tool" {
			ids = append(ids, messages[j].ToolCallID)
		}
	}
	return ids
}

// lastAssistantIndex returns the index of the last assistant message, failing
// the test when there is none.
func lastAssistantIndex(t *testing.T, messages []types.ChatMessage) int {
	t.Helper()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return i
		}
	}
	t.Fatalf("no assistant message found in %+v", messages)
	return -1
}

// TestTransformRequest_ToolMessageOrdering exercises
// RequestTransformer.fixToolMessageOrdering (invoked through the public
// TransformRequest entry point) against the scenarios that motivated it:
// OpenAI's chat-completions API requires every tool_calls entry on an
// assistant message to be answered by an immediately-following "tool"
// message with a matching tool_call_id — a call left unanswered (e.g. the
// user hit Ctrl+C before a result came back) otherwise gets a 400 from
// upstream. Ported from ogc's fixToolMessageOrdering
// (internal/transformer/request.go), which has no test coverage of its own
// in the fork; cases below are original to this port.
func TestTransformRequest_ToolMessageOrdering(t *testing.T) {
	transformer := NewRequestTransformer()
	cfg := config.ModelConfig{ModelID: "glm-5.3"}

	t.Run("all calls answered, no change", func(t *testing.T) {
		req := &types.MessageRequest{
			Model: "glm-5.3",
			Messages: []types.Message{
				{Role: "user", Content: json.RawMessage(`"list the files in /tmp"`)},
				{Role: "assistant", Content: json.RawMessage(`[
					{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"ls /tmp"}}
				]`)},
				{Role: "user", Content: json.RawMessage(`[
					{"type":"tool_result","tool_use_id":"toolu_01","content":"file1\nfile2"}
				]`)},
			},
		}

		openaiReq, err := transformer.TransformRequest(req, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		asstIdx := lastAssistantIndex(t, openaiReq.Messages)
		got := answeredToolCallIDs(openaiReq.Messages, asstIdx)
		want := []string{"toolu_01"}
		if !slices.Equal(got, want) {
			t.Errorf("answered tool_call_ids = %v, want %v (message order should be unchanged)", got, want)
		}
		for _, m := range openaiReq.Messages {
			if m.Role == "tool" && m.ContentText() == interruptedToolCallPlaceholder {
				t.Errorf("no synthetic response should have been inserted, got one: %+v", m)
			}
		}
	})

	t.Run("no calls answered, both synthesized", func(t *testing.T) {
		req := &types.MessageRequest{
			Model: "glm-5.3",
			Messages: []types.Message{
				{Role: "user", Content: json.RawMessage(`"read two files"`)},
				{Role: "assistant", Content: json.RawMessage(`[
					{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}},
					{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}}
				]`)},
				// Interrupted before either tool returned a result.
				{Role: "user", Content: json.RawMessage(`"actually never mind, do something else"`)},
			},
		}

		openaiReq, err := transformer.TransformRequest(req, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		asstIdx := lastAssistantIndex(t, openaiReq.Messages)
		got := answeredToolCallIDs(openaiReq.Messages, asstIdx)
		want := []string{"toolu_a", "toolu_b"}
		if !slices.Equal(got, want) {
			t.Fatalf("answered tool_call_ids = %v, want %v", got, want)
		}
		for _, id := range want {
			found := false
			for j := asstIdx + 1; j < len(openaiReq.Messages) && openaiReq.Messages[j].Role != "assistant"; j++ {
				m := openaiReq.Messages[j]
				if m.Role == "tool" && m.ToolCallID == id {
					found = true
					if got := m.ContentText(); got != interruptedToolCallPlaceholder {
						t.Errorf("synthetic content for %s = %q, want %q", id, got, interruptedToolCallPlaceholder)
					}
				}
			}
			if !found {
				t.Errorf("no synthetic tool response found for %s", id)
			}
		}
		// The dangling user text must still land after the tool responses.
		last := openaiReq.Messages[len(openaiReq.Messages)-1]
		if last.Role != "user" || last.ContentText() != "actually never mind, do something else" {
			t.Errorf("expected trailing user text to survive after synthesis, got %+v", last)
		}
	})

	t.Run("some of several parallel calls answered", func(t *testing.T) {
		req := &types.MessageRequest{
			Model: "glm-5.3",
			Messages: []types.Message{
				{Role: "user", Content: json.RawMessage(`"read three files"`)},
				{Role: "assistant", Content: json.RawMessage(`[
					{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}},
					{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}},
					{"type":"tool_use","id":"toolu_c","name":"Read","input":{"path":"/c"}}
				]`)},
				// Only b answered before the interrupt.
				{Role: "user", Content: json.RawMessage(`[
					{"type":"tool_result","tool_use_id":"toolu_b","content":"B contents"}
				]`)},
			},
		}

		openaiReq, err := transformer.TransformRequest(req, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		asstIdx := lastAssistantIndex(t, openaiReq.Messages)
		got := answeredToolCallIDs(openaiReq.Messages, asstIdx)
		// Real response (toolu_b) is placed first, then synthetic responses
		// for the unanswered calls in their original tool_calls order.
		want := []string{"toolu_b", "toolu_a", "toolu_c"}
		if !slices.Equal(got, want) {
			t.Fatalf("answered tool_call_ids = %v, want %v", got, want)
		}
		for j := asstIdx + 1; j < len(openaiReq.Messages) && openaiReq.Messages[j].Role != "assistant"; j++ {
			m := openaiReq.Messages[j]
			switch m.ToolCallID {
			case "toolu_b":
				if got := m.ContentText(); got != "B contents" {
					t.Errorf("toolu_b content = %q, want real result %q", got, "B contents")
				}
			case "toolu_a", "toolu_c":
				if got := m.ContentText(); got != interruptedToolCallPlaceholder {
					t.Errorf("%s content = %q, want synthetic placeholder", m.ToolCallID, got)
				}
			}
		}
	})

	t.Run("tool result arrives out of order, order preserved not resorted", func(t *testing.T) {
		req := &types.MessageRequest{
			Model: "glm-5.3",
			Messages: []types.Message{
				{Role: "user", Content: json.RawMessage(`"read two files"`)},
				{Role: "assistant", Content: json.RawMessage(`[
					{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}},
					{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}}
				]`)},
				// B's result arrives before A's, e.g. B finished first.
				{Role: "user", Content: json.RawMessage(`[
					{"type":"tool_result","tool_use_id":"toolu_b","content":"B contents"},
					{"type":"tool_result","tool_use_id":"toolu_a","content":"A contents"}
				]`)},
			},
		}

		openaiReq, err := transformer.TransformRequest(req, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		asstIdx := lastAssistantIndex(t, openaiReq.Messages)
		got := answeredToolCallIDs(openaiReq.Messages, asstIdx)
		// fixToolMessageOrdering does not resort matched tool responses to
		// follow tool_calls order — it preserves the order they arrived in.
		want := []string{"toolu_b", "toolu_a"}
		if !slices.Equal(got, want) {
			t.Fatalf("answered tool_call_ids = %v, want %v (arrival order preserved)", got, want)
		}
		for _, m := range openaiReq.Messages {
			if m.Role == "tool" && m.ContentText() == interruptedToolCallPlaceholder {
				t.Errorf("both calls were answered; no synthetic response expected, got one: %+v", m)
			}
		}
	})
}
