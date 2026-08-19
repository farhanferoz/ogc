// Package transformer handles request and response format conversion
// between Anthropic Messages API and OpenAI Chat Completions API.
package transformer

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xynogen/ogc/internal/config"
	"github.com/xynogen/ogc/pkg/types"
)

// RequestTransformer converts Anthropic requests to OpenAI format.
type RequestTransformer struct{}

// NewRequestTransformer creates a new request transformer.
func NewRequestTransformer() *RequestTransformer {
	return &RequestTransformer{}
}

// TransformRequest converts an Anthropic MessageRequest to OpenAI ChatCompletionRequest.
func (t *RequestTransformer) TransformRequest(
	anthropicReq *types.MessageRequest,
	model config.ModelConfig,
) (*types.ChatCompletionRequest, error) {
	// Transform messages
	messages, err := t.transformMessages(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("failed to transform messages: %w", err)
	}

	// Build OpenAI request
	openaiReq := &types.ChatCompletionRequest{
		Model:    model.ModelID,
		Messages: messages,
		Stream:   anthropicReq.Stream,
	}

	// Copy optional parameters from Anthropic request
	if anthropicReq.Temperature != nil {
		openaiReq.Temperature = anthropicReq.Temperature
	}
	if anthropicReq.TopP != nil {
		openaiReq.TopP = anthropicReq.TopP
	}

	// Map max_tokens
	if anthropicReq.MaxTokens > 0 {
		maxTokens := anthropicReq.MaxTokens
		openaiReq.MaxTokens = &maxTokens
	}

	// Apply model-specific overrides
	if model.Temperature != nil {
		openaiReq.Temperature = model.Temperature
	}
	if model.MaxTokens > 0 {
		maxTokens := model.MaxTokens
		openaiReq.MaxTokens = &maxTokens
	}

	// Transform tools if present
	if len(anthropicReq.Tools) > 0 {
		openaiReq.Tools = t.transformTools(anthropicReq.Tools)
	}

	return openaiReq, nil
}

// transformMessages converts Anthropic messages to OpenAI format.
func (t *RequestTransformer) transformMessages(anthropicReq *types.MessageRequest) ([]types.ChatMessage, error) {
	var raw []types.ChatMessage

	// Add system message if present
	systemText := anthropicReq.SystemText()
	if len(anthropicReq.Tools) > 0 {
		const toolDirective = "\n\n[Tool Use Directive: When you need to read, write, search, or edit files, or execute commands, you MUST directly invoke the appropriate tool call. Do NOT output conversational narration describing the action without emitting the tool call in the same turn.]"
		if systemText != "" {
			systemText += toolDirective
		} else {
			systemText = strings.TrimPrefix(toolDirective, "\n\n")
		}
	}

	if systemText != "" {
		raw = append(raw, types.ChatMessage{
			Role:    "system",
			Content: systemText,
		})
	}

	// Transform each message
	for _, msg := range anthropicReq.Messages {
		openaiMsgs, err := t.transformMessage(msg)
		if err != nil {
			return nil, err
		}
		raw = append(raw, openaiMsgs...)
	}

	return t.fixToolMessageOrdering(raw), nil
}

// fixToolMessageOrdering ensures tool messages strictly follow assistant tool_calls
// and inserts synthetic tool responses for any dangling/interrupted tool calls.
func (t *RequestTransformer) fixToolMessageOrdering(messages []types.ChatMessage) []types.ChatMessage {
	var result []types.ChatMessage

	i := 0
	for i < len(messages) {
		msg := messages[i]
		result = append(result, msg)
		i++

		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			toolCallsNeeded := make(map[string]bool)
			for _, tc := range msg.ToolCalls {
				toolCallsNeeded[tc.ID] = true
			}

			var toolMsgs []types.ChatMessage
			var nonToolMsgs []types.ChatMessage

			for i < len(messages) && messages[i].Role != "assistant" {
				next := messages[i]
				if next.Role == "tool" && toolCallsNeeded[next.ToolCallID] {
					toolMsgs = append(toolMsgs, next)
					delete(toolCallsNeeded, next.ToolCallID)
				} else {
					nonToolMsgs = append(nonToolMsgs, next)
				}
				i++
			}

			result = append(result, toolMsgs...)

			for _, tc := range msg.ToolCalls {
				if toolCallsNeeded[tc.ID] {
					result = append(result, types.ChatMessage{
						Role:       "tool",
						Content:    "[Operation interrupted by user]",
						ToolCallID: tc.ID,
					})
				}
			}

			result = append(result, nonToolMsgs...)
		}
	}

	return result
}

// transformMessage converts a single Anthropic message to one or more OpenAI messages.
// Tool_use and tool_result require special handling to map to OpenAI's function calling format.
func (t *RequestTransformer) transformMessage(msg types.Message) ([]types.ChatMessage, error) {
	blocks := msg.ContentBlocks()

	switch msg.Role {
	case "user":
		return t.transformUserMessage(blocks)
	case "assistant":
		return t.transformAssistantMessage(blocks)
	default:
		// Fallback: concatenate all text
		var text string
		for _, b := range blocks {
			if b.Type == "text" {
				text += b.Text
			}
		}
		return []types.ChatMessage{{Role: msg.Role, Content: text}}, nil
	}
}

// transformUserMessage converts a user message with potential tool_result blocks.
func (t *RequestTransformer) transformUserMessage(blocks []types.ContentBlock) ([]types.ChatMessage, error) {
	var result []types.ChatMessage
	var textParts []string

	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_result":
			// In OpenAI, tool results are separate messages with role "tool"
			toolContent := block.TextContent()
			result = append(result, types.ChatMessage{
				Role:       "tool",
				Content:    toolContent,
				ToolCallID: block.GetToolID(),
			})
		case "image":
			// Images not supported in text-only models, skip
			textParts = append(textParts, "[Image]")
		}
	}

	// If there's text content, append after tool results
	if len(textParts) > 0 {
		text := ""
		for _, p := range textParts {
			text += p
		}
		result = append(result, types.ChatMessage{Role: "user", Content: text})
	}

	return result, nil
}

// transformAssistantMessage converts an assistant message with potential tool_use blocks.
func (t *RequestTransformer) transformAssistantMessage(blocks []types.ContentBlock) ([]types.ChatMessage, error) {
	var textParts []string
	var toolCalls []types.ToolCall

	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "thinking":
			// Skip thinking blocks — OpenAI doesn't have an equivalent
			// (some models support reasoning_effort but not raw thinking blocks)
		case "tool_use":
			// Map to OpenAI function call format
			arguments := "{}"
			if len(block.Input) > 0 {
				arguments = string(block.Input)
			}
			toolCalls = append(toolCalls, types.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: types.FunctionCall{
					Name:      block.Name,
					Arguments: arguments,
				},
			})
		}
	}

	// Build the assistant message
	content := ""
	for _, p := range textParts {
		content += p
	}

	msg := types.ChatMessage{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
	}

	return []types.ChatMessage{msg}, nil
}

// transformTools converts Anthropic tools to OpenAI tools.
func (t *RequestTransformer) transformTools(tools []types.Tool) []types.ToolDef {
	var result []types.ToolDef

	for _, tool := range tools {
		// InputSchema is already json.RawMessage, use it directly
		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = []byte(`{"type":"object","properties":{}}`)
		}

		result = append(result, types.ToolDef{
			Type: "function",
			Function: types.FunctionDef{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  json.RawMessage(schema),
			},
		})
	}

	return result
}
