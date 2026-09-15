package gomodels

import (
	"strings"
	"testing"

	"github.com/routatic/proxy/internal/core"
)

const docsFixture = `# Go

Some intro text.

## Endpoints

You can also access Go models through the following API endpoints.

| Model          | Model ID       | Endpoint                                         | AI SDK Package              |
| -------------- | -------------- | ------------------------------------------------ | --------------------------- |
| Grok 4.6       | grok-4.6       | ` + "`https://opencode.ai/zen/go/v1/responses`" + `        | ` + "`@ai-sdk/openai`" + `            |
| GLM-5.3        | glm-5.3        | ` + "`https://opencode.ai/zen/go/v1/chat/completions`" + ` | ` + "`@ai-sdk/openai-compatible`" + ` |
| MiniMax M3     | minimax-m3     | ` + "`https://opencode.ai/zen/go/v1/messages`" + `         | ` + "`@ai-sdk/anthropic`" + `         |

The model id in your OpenCode config uses the format ` + "`opencode-go/<model-id>`" + `.

## Usage limits

| Limit | Value |
| ----- | ----- |
| Daily | 100   |
`

func TestParseDocsTable_ReadsModelsAndWireFormats(t *testing.T) {
	models, err := ParseDocsTable(docsFixture)
	if err != nil {
		t.Fatalf("ParseDocsTable: %v", err)
	}
	want := []DocsModel{
		{ID: "grok-4.6", Name: "Grok 4.6", WireFormat: core.WireFormatOpenAIResponses},
		{ID: "glm-5.3", Name: "GLM-5.3", WireFormat: core.WireFormatOpenAIChat},
		{ID: "minimax-m3", Name: "MiniMax M3", WireFormat: core.WireFormatAnthropic},
	}
	if len(models) != len(want) {
		t.Fatalf("got %d models %+v, want %d", len(models), models, len(want))
	}
	for i := range want {
		if models[i] != want[i] {
			t.Errorf("model %d = %+v, want %+v", i, models[i], want[i])
		}
	}
}

func TestParseDocsTable_FailsLoudlyOnFormatChanges(t *testing.T) {
	cases := map[string]string{
		"missing section":       strings.Replace(docsFixture, "## Endpoints", "## Access", 1),
		"renamed column":        strings.Replace(docsFixture, "| Model ID ", "| Identifier ", 1),
		"unknown endpoint":      strings.Replace(docsFixture, "/v1/messages", "/v2/generate", 1),
		"table without rows":    docsFixture[:strings.Index(docsFixture, "| Grok")] + "\n## Usage limits\n",
		"section with no table": "## Endpoints\n\nNothing here.\n\n## Usage limits\n",
	}
	for name, markdown := range cases {
		t.Run(name, func(t *testing.T) {
			if models, err := ParseDocsTable(markdown); err == nil {
				t.Fatalf("expected an error, got %d models: %+v", len(models), models)
			}
		})
	}
}
