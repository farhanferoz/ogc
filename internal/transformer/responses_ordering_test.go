package transformer

import (
	"encoding/json"
	"testing"

	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/internal/core"
	"github.com/routatic/proxy/pkg/types"
)

func TestTransformToResponses_PreservesContentBlockOrder(t *testing.T) {
	req := &types.MessageRequest{
		Messages: []types.Message{{
			Role: "assistant",
			Content: json.RawMessage(`[
				{"type":"text","text":"Before"},
				{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"one"}},
				{"type":"text","text":"After"}
			]`),
		}},
	}

	responsesReq, err := NewRequestTransformer().TransformToResponses(
		req,
		config.ModelConfig{ModelID: "gpt-5"},
	)
	if err != nil {
		t.Fatalf("TransformToResponses error: %v", err)
	}

	if len(responsesReq.Input) != 3 {
		t.Fatalf("input count = %d, want 3", len(responsesReq.Input))
	}
	if got := responsesInputText(t, responsesReq.Input[0]); got != "Before" {
		t.Errorf("input[0] text = %q, want Before", got)
	}
	if got := responsesReq.Input[1]; got.Type != "function_call" || got.CallID != "call_1" {
		t.Errorf("input[1] = %+v, want function_call call_1", got)
	}
	if got := responsesInputText(t, responsesReq.Input[2]); got != "After" {
		t.Errorf("input[2] text = %q, want After", got)
	}
}

func TestNormalizedToResponses_PreservesContentBlockOrder(t *testing.T) {
	req := &core.NormalizedRequest{
		Messages: []core.NormalizedMessage{{
			Role: "user",
			Blocks: []core.NormalizedContentBlock{
				{
					Type:      "tool_result",
					ToolUseID: "call_1",
					Content:   json.RawMessage(`"result"`),
				},
				{Type: "text", Text: "After result"},
			},
		}},
	}

	responsesReq := NormalizedToResponses(req, config.ModelConfig{ModelID: "gpt-5"})

	if len(responsesReq.Input) != 2 {
		t.Fatalf("input count = %d, want 2", len(responsesReq.Input))
	}
	if got := responsesReq.Input[0]; got.Type != "function_call_output" || got.CallID != "call_1" {
		t.Errorf("input[0] = %+v, want function_call_output call_1", got)
	}
	if got := responsesInputText(t, responsesReq.Input[1]); got != "After result" {
		t.Errorf("input[1] text = %q, want After result", got)
	}
}

// TestNormalizedToResponses_RepairedHistoryAnswersCallFirst pins the ordering
// the Responses translation takes from the repair: NormalizedToResponses emits
// blocks in order, so a user message [text, tool_result] yields the output
// directly after its call only because core.RepairDanglingToolCalls moves the
// tool_result ahead of the text.
func TestNormalizedToResponses_RepairedHistoryAnswersCallFirst(t *testing.T) {
	req := &types.MessageRequest{
		Model: "gpt-5.6-luna",
		Messages: []types.Message{
			{Role: "assistant", Content: json.RawMessage(`[{"type":"tool_use","id":"call_a","name":"lookup","input":{"q":"one"}}]`)},
			{Role: "user", Content: json.RawMessage(`[
				{"type":"text","text":"now continue"},
				{"type":"tool_result","tool_use_id":"call_a","content":"result"}
			]`)},
		},
	}
	req.Messages = core.RepairDanglingToolCalls(req.Messages)

	input := NormalizedToResponses(core.NormalizeRequest(req), config.ModelConfig{ModelID: "gpt-5.6-luna"}).Input

	if len(input) != 3 {
		t.Fatalf("input count = %d, want 3: %+v", len(input), input)
	}
	if got := input[0]; got.Type != "function_call" || got.CallID != "call_a" {
		t.Errorf("input[0] = %+v, want function_call call_a", got)
	}
	if got := input[1]; got.Type != "function_call_output" || got.CallID != "call_a" {
		t.Errorf("input[1] = %+v, want function_call_output call_a", got)
	}
	if got := responsesInputText(t, input[2]); got != "now continue" {
		t.Errorf("input[2] text = %q, want now continue", got)
	}
}

func responsesInputText(t *testing.T, input types.ResponsesInput) string {
	t.Helper()
	var text string
	if err := json.Unmarshal(input.Content, &text); err != nil {
		t.Fatalf("unmarshal input content %s: %v", input.Content, err)
	}
	return text
}
