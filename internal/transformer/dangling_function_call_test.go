package transformer

import (
	"encoding/json"
	"testing"

	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/internal/core"
	"github.com/routatic/proxy/pkg/types"
)

// outputText decodes a ResponsesInput.Output/Content json.RawMessage string
// value back to plain text for assertions.
func outputText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("failed to decode output text %q: %v", raw, err)
	}
	return s
}

// answeredCallIDs returns, for the run of consecutive "function_call" items
// starting at index i, the call_id of every "function_call_output" item that
// follows before the next "function_call" item, in the order they appear.
func answeredCallIDs(items []types.ResponsesInput, i int) []string {
	for i < len(items) && items[i].Type == "function_call" {
		i++
	}
	var ids []string
	for ; i < len(items) && items[i].Type != "function_call"; i++ {
		if items[i].Type == "function_call_output" {
			ids = append(ids, items[i].CallID)
		}
	}
	return ids
}

func firstFunctionCallIndex(items []types.ResponsesInput) int {
	for i, item := range items {
		if item.Type == "function_call" {
			return i
		}
	}
	return -1
}

// TestNormalizedToResponses_DanglingCall exercises the same interrupted-
// tool-call scenarios ported to the chat-completions translation
// (TestTransformRequest_ToolMessageOrdering, internal/transformer/request.go)
// against the Responses translation. The Responses API has the same
// requirement as chat-completions: a "function_call" item must be answered
// by a "function_call_output" item with a matching call_id, or upstream
// (gpt-5.6-luna, grok-4.6, muse-spark) rejects the request.
func TestNormalizedToResponses_DanglingCall(t *testing.T) {
	model := config.ModelConfig{ModelID: "gpt-5.6-luna", WireFormat: "responses"}

	t.Run("all calls answered, no change", func(t *testing.T) {
		req := &core.NormalizedRequest{
			Model: "gpt-5.6-luna",
			Messages: []core.NormalizedMessage{
				{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "list the files in /tmp"}}},
				{Role: "assistant", Blocks: []core.NormalizedContentBlock{
					{Type: "tool_use", ID: "toolu_01", Name: "Bash", Input: json.RawMessage(`{"command":"ls /tmp"}`)},
				}},
				{Role: "user", Blocks: []core.NormalizedContentBlock{
					{Type: "tool_result", ToolUseID: "toolu_01", Content: json.RawMessage(`"file1\nfile2"`)},
				}},
			},
		}

		resp := NormalizedToResponses(req, model)

		i := firstFunctionCallIndex(resp.Input)
		if i == -1 {
			t.Fatalf("no function_call found in %+v", resp.Input)
		}
		got := answeredCallIDs(resp.Input, i)
		want := []string{"toolu_01"}
		if len(got) != len(want) || got[0] != want[0] {
			t.Errorf("answered call_ids = %v, want %v", got, want)
		}
		for _, item := range resp.Input {
			if item.Type == "function_call_output" && outputText(t, item.Output) == interruptedToolCallPlaceholder {
				t.Errorf("no synthetic output should have been inserted, got one: %+v", item)
			}
		}
	})

	t.Run("no calls answered, both synthesized", func(t *testing.T) {
		req := &core.NormalizedRequest{
			Model: "gpt-5.6-luna",
			Messages: []core.NormalizedMessage{
				{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "read two files"}}},
				{Role: "assistant", Blocks: []core.NormalizedContentBlock{
					{Type: "tool_use", ID: "toolu_a", Name: "Read", Input: json.RawMessage(`{"path":"/a"}`)},
					{Type: "tool_use", ID: "toolu_b", Name: "Read", Input: json.RawMessage(`{"path":"/b"}`)},
				}},
				// Interrupted before either tool returned a result.
				{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "actually never mind, do something else"}}},
			},
		}

		resp := NormalizedToResponses(req, model)

		i := firstFunctionCallIndex(resp.Input)
		if i == -1 {
			t.Fatalf("no function_call found in %+v", resp.Input)
		}
		got := answeredCallIDs(resp.Input, i)
		want := []string{"toolu_a", "toolu_b"}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("answered call_ids = %v, want %v", got, want)
		}
		for _, id := range want {
			found := false
			for _, item := range resp.Input {
				if item.Type == "function_call_output" && item.CallID == id {
					found = true
					if text := outputText(t, item.Output); text != interruptedToolCallPlaceholder {
						t.Errorf("synthetic output for %s = %q, want %q", id, text, interruptedToolCallPlaceholder)
					}
				}
			}
			if !found {
				t.Errorf("no synthetic function_call_output found for %s", id)
			}
		}
		// The dangling user text must still survive after the synthesized outputs.
		last := resp.Input[len(resp.Input)-1]
		if last.Role != "user" || outputText(t, last.Content) != "actually never mind, do something else" {
			t.Errorf("expected trailing user text to survive after synthesis, got %+v", last)
		}
	})

	t.Run("some of several parallel calls answered", func(t *testing.T) {
		req := &core.NormalizedRequest{
			Model: "gpt-5.6-luna",
			Messages: []core.NormalizedMessage{
				{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "read three files"}}},
				{Role: "assistant", Blocks: []core.NormalizedContentBlock{
					{Type: "tool_use", ID: "toolu_a", Name: "Read", Input: json.RawMessage(`{"path":"/a"}`)},
					{Type: "tool_use", ID: "toolu_b", Name: "Read", Input: json.RawMessage(`{"path":"/b"}`)},
					{Type: "tool_use", ID: "toolu_c", Name: "Read", Input: json.RawMessage(`{"path":"/c"}`)},
				}},
				// Only b answered before the interrupt.
				{Role: "user", Blocks: []core.NormalizedContentBlock{
					{Type: "tool_result", ToolUseID: "toolu_b", Content: json.RawMessage(`"B contents"`)},
				}},
			},
		}

		resp := NormalizedToResponses(req, model)

		i := firstFunctionCallIndex(resp.Input)
		got := answeredCallIDs(resp.Input, i)
		// Real output (toolu_b) is placed first, then synthetic outputs for
		// the unanswered calls in their original function_call order.
		want := []string{"toolu_b", "toolu_a", "toolu_c"}
		if len(got) != len(want) {
			t.Fatalf("answered call_ids = %v, want %v", got, want)
		}
		for idx := range want {
			if got[idx] != want[idx] {
				t.Fatalf("answered call_ids = %v, want %v", got, want)
			}
		}
		for _, item := range resp.Input {
			if item.Type != "function_call_output" {
				continue
			}
			switch item.CallID {
			case "toolu_b":
				if text := outputText(t, item.Output); text != "B contents" {
					t.Errorf("toolu_b output = %q, want real result %q", text, "B contents")
				}
			case "toolu_a", "toolu_c":
				if text := outputText(t, item.Output); text != interruptedToolCallPlaceholder {
					t.Errorf("%s output = %q, want synthetic placeholder", item.CallID, text)
				}
			}
		}
	})

	t.Run("tool result arrives out of order, order preserved not resorted", func(t *testing.T) {
		req := &core.NormalizedRequest{
			Model: "gpt-5.6-luna",
			Messages: []core.NormalizedMessage{
				{Role: "user", Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "read two files"}}},
				{Role: "assistant", Blocks: []core.NormalizedContentBlock{
					{Type: "tool_use", ID: "toolu_a", Name: "Read", Input: json.RawMessage(`{"path":"/a"}`)},
					{Type: "tool_use", ID: "toolu_b", Name: "Read", Input: json.RawMessage(`{"path":"/b"}`)},
				}},
				// B's result arrives before A's, e.g. B finished first.
				{Role: "user", Blocks: []core.NormalizedContentBlock{
					{Type: "tool_result", ToolUseID: "toolu_b", Content: json.RawMessage(`"B contents"`)},
					{Type: "tool_result", ToolUseID: "toolu_a", Content: json.RawMessage(`"A contents"`)},
				}},
			},
		}

		resp := NormalizedToResponses(req, model)

		i := firstFunctionCallIndex(resp.Input)
		got := answeredCallIDs(resp.Input, i)
		// fixResponsesCallOrdering does not resort matched outputs to follow
		// function_call order — it preserves the order they arrived in.
		want := []string{"toolu_b", "toolu_a"}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("answered call_ids = %v, want %v (arrival order preserved)", got, want)
		}
		for _, item := range resp.Input {
			if item.Type == "function_call_output" && outputText(t, item.Output) == interruptedToolCallPlaceholder {
				t.Errorf("both calls were answered; no synthetic output expected, got one: %+v", item)
			}
		}
	})
}
