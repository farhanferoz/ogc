// Package token provides token counting utilities using tiktoken encoding.
package token

import (
	"fmt"

	"github.com/pkoukk/tiktoken-go"
	"github.com/xynogen/ogc/pkg/types"
)

// Counter handles token counting for text and message arrays.
type Counter struct {
	tiktoken *tiktoken.Tiktoken
}

// NewCounter creates a new token counter with cl100k_base encoding.
func NewCounter() (*Counter, error) {
	enc, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		return nil, fmt.Errorf("failed to get encoding: %w", err)
	}
	return &Counter{tiktoken: enc}, nil
}

// CountTokens counts tokens in a string.
func (c *Counter) CountTokens(text string) (int, error) {
	tokens := c.tiktoken.Encode(text, nil, nil)
	return len(tokens), nil
}

// MessageContent represents a single message in a conversation.
type MessageContent struct {
	Role    string
	Content string
}

// CountMessages counts tokens in a message array.
// Estimates tokens for system prompt + messages with formatting overhead.
func (c *Counter) CountMessages(system string, messages []MessageContent) (int, error) {
	// Base tokens for message formatting
	total := 3 // Start token

	if system != "" {
		sysTokens, err := c.CountTokens(system)
		if err != nil {
			return 0, err
		}
		total += sysTokens + 5 // System prompt overhead
	}

	for _, msg := range messages {
		msgTokens, err := c.CountTokens(msg.Content)
		if err != nil {
			return 0, err
		}
		total += msgTokens + 5 // Per-message overhead
	}

	return total, nil
}

// CountRequest counts tokens in an Anthropic MessageRequest.
// It accounts for system prompt, messages (including text, tool_use, tool_result blocks),
// tool definitions, and message framing tokens.
func (c *Counter) CountRequest(req *types.MessageRequest) (int, error) {
	if req == nil {
		return 0, nil
	}

	// CountTokens never returns an error.
	count := func(s string) int {
		n, _ := c.CountTokens(s)
		return n
	}

	total := 3 // Base prompt overhead

	if sys := req.SystemText(); sys != "" {
		total += count(sys) + 5
	}

	for _, msg := range req.Messages {
		total += 3 // Message header overhead
		for _, b := range msg.ContentBlocks() {
			switch b.Type {
			case "text":
				total += count(b.Text)
			case "tool_use":
				total += count(b.Name) + count(string(b.Input)) + 6
			case "tool_result":
				if text := b.TextContent(); text != "" {
					total += count(text) + 4
				}
			case "thinking":
				total += count(b.Thinking)
			case "image":
				total += 85
			}
		}
	}

	for _, tool := range req.Tools {
		total += count(tool.Name) + count(tool.Description) + count(string(tool.InputSchema)) + 12
	}

	return total, nil
}
