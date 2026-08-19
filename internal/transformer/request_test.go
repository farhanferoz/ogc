package transformer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xynogen/ogc/internal/config"
	"github.com/xynogen/ogc/pkg/types"
)

func TestTransformRequest_ZeroTemperature(t *testing.T) {
	transformer := NewRequestTransformer()
	zeroTemp := 0.0
	cfg := config.ModelConfig{
		ModelID:     "deepseek-v4-flash",
		Temperature: &zeroTemp,
	}

	req := &types.MessageRequest{
		Model: "deepseek-v4-flash",
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"hello"`)},
		},
	}

	openaiReq, err := transformer.TransformRequest(req, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if openaiReq.Temperature == nil {
		t.Fatalf("expected Temperature to be set, got nil")
	}
	if *openaiReq.Temperature != 0.0 {
		t.Errorf("expected Temperature 0.0, got %f", *openaiReq.Temperature)
	}
}

func TestTransformRequest_ToolDirectiveInjection(t *testing.T) {
	transformer := NewRequestTransformer()
	cfg := config.ModelConfig{
		ModelID: "glm-5.3",
	}

	req := &types.MessageRequest{
		Model:  "glm-5.3",
		System: json.RawMessage(`"Base system prompt."`),
		Messages: []types.Message{
			{Role: "user", Content: json.RawMessage(`"run bash"`)},
		},
		Tools: []types.Tool{
			{
				Name:        "Bash",
				Description: "Run bash command",
			},
		},
	}

	openaiReq, err := transformer.TransformRequest(req, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(openaiReq.Messages) == 0 || openaiReq.Messages[0].Role != "system" {
		t.Fatalf("expected first message to be system, got %+v", openaiReq.Messages)
	}

	sysContent := openaiReq.Messages[0].Content
	if !strings.Contains(sysContent, "Base system prompt.") {
		t.Errorf("expected base system prompt to be preserved, got: %s", sysContent)
	}
	if !strings.Contains(sysContent, "Tool Use Directive") {
		t.Errorf("expected Tool Use Directive in system prompt, got: %s", sysContent)
	}
}
