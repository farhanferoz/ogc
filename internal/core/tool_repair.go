package core

import (
	"encoding/json"
	"slices"

	"github.com/routatic/proxy/pkg/types"
)

// InterruptedToolCallPlaceholder is the synthetic answer inserted for a tool
// call that never got a real result (e.g. the user interrupted it with
// Ctrl+C).
const InterruptedToolCallPlaceholder = "[Operation interrupted by user]"

// RepairDanglingToolCalls returns a copy of messages in which every tool_use
// block is answered by exactly one tool_result in the user message directly
// after it. The chat-completions and Responses APIs both reject a tool call
// left unanswered, which happens when the user interrupts a tool before its
// result comes back.
//
// For each assistant message with tool_use blocks, the following user message
// (inserted when the next message is not a user message or the history ends)
// is rebuilt as: the results it already holds for those calls, in arrival
// order; then, per still-unanswered call in call order, the first matching
// result found later in the history (moved, not copied; see takeToolResult for
// where the search stops) or a placeholder;
// then its remaining blocks in their original order. A later message emptied
// by such a move is dropped. Messages the repair does not change keep their
// original content bytes.
func RepairDanglingToolCalls(messages []types.Message) []types.Message {
	blocks := make([][]types.ContentBlock, len(messages))
	rewritten := make([]bool, len(messages))
	for i := range messages {
		blocks[i] = messages[i].ContentBlocks()
	}

	result := make([]types.Message, 0, len(messages))
	for i, msg := range messages {
		if rewritten[i] {
			if len(blocks[i]) == 0 {
				continue
			}
			repaired, err := messageWithBlocks(msg.Role, blocks[i])
			if err != nil {
				return slices.Clone(messages)
			}
			result = append(result, repaired)
		} else {
			result = append(result, msg)
		}

		if msg.Role != "assistant" {
			continue
		}
		pending := make(map[string]bool)
		var calls []string
		for _, b := range blocks[i] {
			if b.Type == "tool_use" {
				calls = append(calls, b.ID)
				pending[b.ID] = true
			}
		}
		if len(calls) == 0 {
			continue
		}

		hasAnswerMessage := i+1 < len(messages) && messages[i+1].Role == "user"
		searchFrom := i + 1
		var current []types.ContentBlock
		if hasAnswerMessage {
			current = blocks[i+1]
			searchFrom = i + 2
		}

		var answers, others []types.ContentBlock
		reordered := false
		for _, b := range current {
			if b.Type == "tool_result" && pending[b.ToolUseID] {
				reordered = reordered || len(others) > 0
				answers = append(answers, b)
				delete(pending, b.ToolUseID)
			} else {
				others = append(others, b)
			}
		}
		for _, id := range calls {
			if pending[id] {
				delete(pending, id)
				answers = append(answers, takeToolResult(id, messages, blocks, rewritten, searchFrom))
			}
		}

		switch {
		case !hasAnswerMessage:
			inserted, err := messageWithBlocks("user", answers)
			if err != nil {
				return slices.Clone(messages)
			}
			result = append(result, inserted)
		case reordered || len(answers)+len(others) != len(current):
			blocks[i+1] = append(answers, others...)
			rewritten[i+1] = true
		}
	}

	return result
}

// takeToolResult removes and returns the first tool_result for id in the user
// messages at index from onward, or returns a placeholder result when the
// call was never answered.
//
// The search stops at a later assistant message that calls a tool with the
// same id: results after it answer that turn, not this one. A result that is
// the last block of its message is taken only when that message would be
// refilled or can be dropped safely — when the message before it is not an
// assistant, or is an assistant with tool calls of its own. Dropping a message
// that follows a text-only assistant turn would put two assistant messages
// together or end the history on the assistant, so such a result stays where
// it is and the call gets a placeholder.
func takeToolResult(id string, messages []types.Message, blocks [][]types.ContentBlock, rewritten []bool, from int) types.ContentBlock {
	callsTool := func(b types.ContentBlock) bool { return b.Type == "tool_use" }
	for j := from; j < len(messages); j++ {
		if messages[j].Role == "assistant" {
			if slices.ContainsFunc(blocks[j], func(b types.ContentBlock) bool { return callsTool(b) && b.ID == id }) {
				break
			}
			continue
		}
		if messages[j].Role != "user" {
			continue
		}
		if len(blocks[j]) == 1 && messages[j-1].Role == "assistant" && !slices.ContainsFunc(blocks[j-1], callsTool) {
			continue
		}
		for k, b := range blocks[j] {
			if b.Type == "tool_result" && b.ToolUseID == id {
				blocks[j] = slices.Delete(blocks[j], k, k+1)
				rewritten[j] = true
				return b
			}
		}
	}
	content, _ := json.Marshal(InterruptedToolCallPlaceholder)
	return types.ContentBlock{Type: "tool_result", ToolUseID: id, Content: content}
}

func messageWithBlocks(role string, blocks []types.ContentBlock) (types.Message, error) {
	content, err := json.Marshal(blocks)
	if err != nil {
		return types.Message{}, err
	}
	return types.Message{Role: role, Content: content}, nil
}
