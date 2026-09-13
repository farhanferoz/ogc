package token

import (
	"encoding/json"
	"testing"

	"github.com/xynogen/ogc/pkg/types"
)

func TestCounter_CountRequest(t *testing.T) {
	c, err := NewCounter()
	if err != nil {
		t.Fatalf("failed to create counter: %v", err)
	}

	req := &types.MessageRequest{
		Model:  "claude-sonnet-4-6",
		System: json.RawMessage(`"You are an assistant."`),
		Messages: []types.Message{
			{
				Role:    "user",
				Content: json.RawMessage(`"Hello, how are you?"`),
			},
			{
				Role:    "assistant",
				Content: json.RawMessage(`[{"type":"text","text":"I am doing well."},{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"echo hello"}}]`),
			},
			{
				Role:    "user",
				Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"call_1","content":"hello\n"}]`),
			},
		},
		Tools: []types.Tool{
			{
				Name:        "Bash",
				Description: "Execute a bash command",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
			},
		},
	}

	count, err := c.CountRequest(req)
	if err != nil {
		t.Fatalf("CountRequest failed: %v", err)
	}

	if count <= 0 {
		t.Errorf("expected positive token count, got %d", count)
	}
}
