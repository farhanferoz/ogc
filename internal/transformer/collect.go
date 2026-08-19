package transformer

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/xynogen/ogc/pkg/types"
)

// CollectAnthropicStream reads an Anthropic-format SSE stream and assembles a MessageResponse.
// Used when the client requests non-streaming but we force streaming upstream.
func CollectAnthropicStream(body io.ReadCloser) (*types.MessageResponse, error) {
	defer body.Close()

	resp := types.MessageResponse{
		Type: "message",
		Role: "assistant",
	}

	var blocks []types.ContentBlock
	type acc struct {
		text      strings.Builder
		thinking  strings.Builder
		inputJSON strings.Builder
	}
	var accs []acc

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "" {
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(data), &raw); err != nil {
			continue
		}
		var eventType string
		json.Unmarshal(raw["type"], &eventType) //nolint:errcheck

		switch eventType {
		case "message_start":
			var e struct {
				Message types.MessageResponse `json:"message"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil {
				resp.ID = e.Message.ID
				resp.Model = e.Message.Model
				resp.Usage = e.Message.Usage
			}

		case "content_block_start":
			var e struct {
				Index        int                `json:"index"`
				ContentBlock types.ContentBlock `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil {
				for len(blocks) <= e.Index {
					blocks = append(blocks, types.ContentBlock{})
					accs = append(accs, acc{})
				}
				blocks[e.Index] = e.ContentBlock
				accs[e.Index] = acc{}
			}

		case "content_block_delta":
			var e struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					Thinking    string `json:"thinking"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil && e.Index < len(blocks) {
				switch e.Delta.Type {
				case "text_delta":
					accs[e.Index].text.WriteString(e.Delta.Text)
				case "thinking_delta":
					accs[e.Index].thinking.WriteString(e.Delta.Thinking)
				case "input_json_delta":
					accs[e.Index].inputJSON.WriteString(e.Delta.PartialJSON)
				}
			}

		case "content_block_stop":
			var e struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil && e.Index < len(blocks) {
				switch blocks[e.Index].Type {
				case "text":
					blocks[e.Index].Text = accs[e.Index].text.String()
				case "thinking":
					blocks[e.Index].Thinking = accs[e.Index].thinking.String()
				case "tool_use":
					if s := accs[e.Index].inputJSON.String(); s != "" {
						blocks[e.Index].Input = json.RawMessage(s)
					}
				}
			}

		case "message_delta":
			var e struct {
				Delta struct {
					StopReason   string `json:"stop_reason"`
					StopSequence string `json:"stop_sequence"`
				} `json:"delta"`
				Usage types.Usage `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &e); err == nil {
				resp.StopReason = e.Delta.StopReason
				resp.StopSequence = e.Delta.StopSequence
				if e.Usage.OutputTokens > 0 {
					resp.Usage.OutputTokens = e.Usage.OutputTokens
				}
				if e.Usage.InputTokens > 0 {
					resp.Usage.InputTokens = e.Usage.InputTokens
				}
			}
		}
	}

	resp.Content = blocks
	return &resp, scanner.Err()
}

// CollectOpenAIStream reads an OpenAI-format SSE stream and assembles a ChatCompletionResponse.
// Used when the client requests non-streaming but we force streaming upstream.
func CollectOpenAIStream(body io.ReadCloser) (*types.ChatCompletionResponse, error) {
	defer body.Close()

	var resp types.ChatCompletionResponse
	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	var finishReason string
	toolCallMap := map[int]*types.ToolCall{}
	var toolCallOrder []int

	type streamToolCall struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	type streamChunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Created int64  `json:"created"`
		Choices []struct {
			Delta struct {
				Content          string           `json:"content"`
				Reasoning        string           `json:"reasoning"`
				ReasoningContent string           `json:"reasoning_content"`
				ToolCalls        []streamToolCall `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *types.UsageInfo `json:"usage"`
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" || data == "" {
			continue
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if resp.ID == "" {
			resp.ID = chunk.ID
			resp.Model = chunk.Model
			resp.Created = chunk.Created
		}

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			contentBuf.WriteString(choice.Delta.Content)
			reasoningBuf.WriteString(choice.Delta.Reasoning)
			reasoningBuf.WriteString(choice.Delta.ReasoningContent)
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
			for _, tc := range choice.Delta.ToolCalls {
				if _, exists := toolCallMap[tc.Index]; !exists {
					toolCallMap[tc.Index] = &types.ToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: types.FunctionCall{
							Name: tc.Function.Name,
						},
					}
					toolCallOrder = append(toolCallOrder, tc.Index)
				} else {
					if tc.ID != "" {
						toolCallMap[tc.Index].ID = tc.ID
					}
					if tc.Function.Name != "" {
						toolCallMap[tc.Index].Function.Name = tc.Function.Name
					}
				}
				toolCallMap[tc.Index].Function.Arguments += tc.Function.Arguments
			}
		}

		if chunk.Usage != nil {
			resp.Usage = *chunk.Usage
		}
	}

	var toolCalls []types.ToolCall
	for _, idx := range toolCallOrder {
		if tc, ok := toolCallMap[idx]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	// Use reasoning as fallback when content is empty (extended-thinking models like Kimi).
	finalContent := contentBuf.String()
	if finalContent == "" {
		finalContent = reasoningBuf.String()
	}

	resp.Choices = []types.Choice{{
		Message: types.ChatMessage{
			Role:      "assistant",
			Content:   finalContent,
			ToolCalls: toolCalls,
		},
		FinishReason: finishReason,
	}}

	return &resp, scanner.Err()
}
