package transformer

import (
	"encoding/json"
	"testing"

	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/internal/core"
)

// TestNormalizedToResponses_KeepsImages covers a pasted image on the Responses
// route: it used to be dropped with only a log line, so a vision-flagged model
// (gpt-5.6-luna, grok-4.6, muse-spark-*) answered about a picture it never saw.
// Shape per OpenAI's Responses docs: content is an array of input_text and
// input_image parts, the image carried as a data URL.
func TestNormalizedToResponses_KeepsImages(t *testing.T) {
	req := &core.NormalizedRequest{
		Model: "gpt-5.6-luna",
		Messages: []core.NormalizedMessage{{
			Role: "user",
			Blocks: []core.NormalizedContentBlock{
				{Type: "text", Text: "what is this?"},
				{Type: "image", Image: &core.NormalizedImage{MediaType: "image/png", Data: "aGVsbG8="}},
			},
		}},
	}

	responsesReq := NormalizedToResponses(req, config.ModelConfig{ModelID: "gpt-5.6-luna", Vision: true})

	if len(responsesReq.Input) != 1 {
		t.Fatalf("Input has %d items, want 1: %+v", len(responsesReq.Input), responsesReq.Input)
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
	}
	if err := json.Unmarshal(responsesReq.Input[0].Content, &parts); err != nil {
		t.Fatalf("content is not an array of parts (%s): %v", responsesReq.Input[0].Content, err)
	}
	if len(parts) != 2 {
		t.Fatalf("content has %d parts, want 2: %s", len(parts), responsesReq.Input[0].Content)
	}
	if parts[0].Type != "input_text" || parts[0].Text != "what is this?" {
		t.Errorf("part 0 = %+v, want input_text %q", parts[0], "what is this?")
	}
	if parts[1].Type != "input_image" {
		t.Errorf("part 1 type = %q, want input_image", parts[1].Type)
	}
	if want := "data:image/png;base64,aGVsbG8="; parts[1].ImageURL != want {
		t.Errorf("part 1 image_url = %q, want %q", parts[1].ImageURL, want)
	}
}

// TestNormalizedToResponses_TextOnlyStaysAString keeps the plain shape for the
// common case: only a message carrying an image needs the parts array.
func TestNormalizedToResponses_TextOnlyStaysAString(t *testing.T) {
	req := &core.NormalizedRequest{
		Model: "gpt-5.6-luna",
		Messages: []core.NormalizedMessage{{
			Role:   "user",
			Blocks: []core.NormalizedContentBlock{{Type: "text", Text: "hello"}},
		}},
	}

	responsesReq := NormalizedToResponses(req, config.ModelConfig{ModelID: "gpt-5.6-luna"})

	var text string
	if err := json.Unmarshal(responsesReq.Input[0].Content, &text); err != nil {
		t.Fatalf("text-only content = %s, want a JSON string: %v", responsesReq.Input[0].Content, err)
	}
	if text != "hello" {
		t.Errorf("content = %q, want %q", text, "hello")
	}
}
