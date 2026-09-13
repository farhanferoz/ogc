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

	total := 3 // Base prompt overhead

	sys := req.SystemText()
	if sys != "" {
		sysTokens, err := c.CountTokens(sys)
		if err != nil {
			return 0, err
		}
		total += sysTokens + 5
	}

	for _, msg := range req.Messages {
		total += 3 // Message header overhead
		blocks := msg.ContentBlocks()
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					cnt, err := c.CountTokens(b.Text)
					if err != nil {
						return 0, err
					}
					total += cnt
				}
			case "tool_use":
				nameCnt, _ := c.CountTokens(b.Name)
				inputCnt, _ := c.CountTokens(string(b.Input))
				total += nameCnt + inputCnt + 6
			case "tool_result":
				text := b.TextContent()
				if text != "" {
					cnt, err := c.CountTokens(text)
					if err != nil {
						return 0, err
					}
					total += cnt + 4
				}
			case "thinking":
				if b.Thinking != "" {
					cnt, _ := c.CountTokens(b.Thinking)
					total += cnt
				}
			case "image":
				total += 85
			}
		}
	}

	for _, tool := range req.Tools {
		nameCnt, _ := c.CountTokens(tool.Name)
		descCnt, _ := c.CountTokens(tool.Description)
		schemaCnt, _ := c.CountTokens(string(tool.InputSchema))
		total += nameCnt + descCnt + schemaCnt + 12
	}

	return total, nil
}
