// Package transformer handles request/response transformation and token counting.
package transformer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xynogen/ogc/pkg/types"
)

// ErrClientDisconnected is returned when the client disconnects during streaming.
var ErrClientDisconnected = fmt.Errorf("client disconnected")

// SSEWriter handles thread-safe writing of SSE events to an HTTP response writer.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
	closed  bool
}

// NewSSEWriter creates a new synchronized SSE writer.
func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	flusher, _ := w.(http.Flusher)
	return &SSEWriter{
		w:       w,
		flusher: flusher,
	}
}

// WriteEvent thread-safely marshals and writes an SSE event.
func (s *SSEWriter) WriteEvent(event types.MessageEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrClientDisconnected
	}

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event.Type, string(data)); err != nil {
		return ErrClientDisconnected
	}

	if s.flusher != nil {
		s.flusher.Flush()
	}
	return nil
}

// Ping sends an SSE ping event to keep the connection alive.
func (s *SSEWriter) Ping() error {
	return s.WriteEvent(types.MessageEvent{Type: "ping"})
}

// Close marks the SSE writer as closed.
func (s *SSEWriter) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

// StreamHandler handles streaming SSE transformation from OpenAI to Anthropic format.
type StreamHandler struct {
	responseTransformer *ResponseTransformer
}

// NewStreamHandler creates a new stream handler.
func NewStreamHandler() *StreamHandler {
	return &StreamHandler{
		responseTransformer: NewResponseTransformer(),
	}
}

type streamToolCallState struct {
	contentIndex int
	id           string
	name         string
}

type streamSession struct {
	writer              *SSEWriter
	originalModel       string
	responseTransformer *ResponseTransformer
	nextIndex           int
	textStarted         bool
	textClosed          bool
	textIndex           int
	toolCalls           map[int]*streamToolCallState
	seenToolIndices     []int
	closed              bool
}

func newStreamSession(writer *SSEWriter, originalModel string, transformer *ResponseTransformer) (*streamSession, error) {
	s := &streamSession{
		writer:              writer,
		originalModel:       originalModel,
		responseTransformer: transformer,
		toolCalls:           make(map[int]*streamToolCallState),
	}

	msgStart := types.MessageEvent{
		Type: "message_start",
		Message: &types.MessageResponse{
			ID:      "msg_" + generateID(),
			Type:    "message",
			Role:    "assistant",
			Content: []types.ContentBlock{},
			Model:   originalModel,
			Usage: types.Usage{
				InputTokens:  0,
				OutputTokens: 0,
			},
		},
	}
	if err := writer.WriteEvent(msgStart); err != nil {
		return nil, ErrClientDisconnected
	}

	return s, nil
}

func (s *streamSession) emitText(text string) error {
	if text == "" {
		return nil
	}

	if !s.textStarted {
		s.textStarted = true
		s.textIndex = s.nextIndex
		s.nextIndex++

		startEvent := types.MessageEvent{
			Type:  "content_block_start",
			Index: &s.textIndex,
			ContentBlock: &types.ContentBlock{
				Type: "text",
				Text: "",
			},
		}
		if err := s.writer.WriteEvent(startEvent); err != nil {
			return ErrClientDisconnected
		}
	}

	delta := types.Delta{
		Type: "text_delta",
		Text: text,
	}
	event := types.MessageEvent{
		Type:  "content_block_delta",
		Index: &s.textIndex,
		Delta: &delta,
	}
	if err := s.writer.WriteEvent(event); err != nil {
		return ErrClientDisconnected
	}
	return nil
}

func (s *streamSession) emitToolCallChunk(tc types.ToolCall, openaiIndex int) error {
	// If text block is still open, close it before tool use begins
	if s.textStarted && !s.textClosed {
		s.textClosed = true
		stopEvent := types.MessageEvent{
			Type:  "content_block_stop",
			Index: &s.textIndex,
		}
		if err := s.writer.WriteEvent(stopEvent); err != nil {
			return ErrClientDisconnected
		}
	}

	ts, exists := s.toolCalls[openaiIndex]
	if !exists {
		toolID := tc.ID
		if toolID == "" {
			toolID = fmt.Sprintf("toolu_%s_%d", generateID(), openaiIndex)
		}
		toolName := tc.Function.Name
		ts = &streamToolCallState{
			contentIndex: s.nextIndex,
			id:           toolID,
			name:         toolName,
		}
		s.toolCalls[openaiIndex] = ts
		s.seenToolIndices = append(s.seenToolIndices, openaiIndex)
		s.nextIndex++

		startEvent := types.MessageEvent{
			Type:  "content_block_start",
			Index: &ts.contentIndex,
			ContentBlock: &types.ContentBlock{
				Type:  "tool_use",
				ID:    ts.id,
				Name:  ts.name,
				Input: json.RawMessage("{}"),
			},
		}
		if err := s.writer.WriteEvent(startEvent); err != nil {
			return ErrClientDisconnected
		}
	} else if ts.name == "" && tc.Function.Name != "" {
		ts.name = tc.Function.Name
	}

	if tc.Function.Arguments != "" {
		delta := types.Delta{
			Type:        "input_json_delta",
			PartialJSON: tc.Function.Arguments,
		}
		event := types.MessageEvent{
			Type:  "content_block_delta",
			Index: &ts.contentIndex,
			Delta: &delta,
		}
		if err := s.writer.WriteEvent(event); err != nil {
			return ErrClientDisconnected
		}
	}

	return nil
}

func (s *streamSession) close(finishReason string, usage *types.UsageInfo) error {
	if s.closed {
		return nil
	}
	s.closed = true

	// Close text block if open
	if s.textStarted && !s.textClosed {
		s.textClosed = true
		stopEvent := types.MessageEvent{
			Type:  "content_block_stop",
			Index: &s.textIndex,
		}
		if err := s.writer.WriteEvent(stopEvent); err != nil {
			return ErrClientDisconnected
		}
	}

	// Close all open tool calls
	for _, idx := range s.seenToolIndices {
		ts := s.toolCalls[idx]
		stopEvent := types.MessageEvent{
			Type:  "content_block_stop",
			Index: &ts.contentIndex,
		}
		if err := s.writer.WriteEvent(stopEvent); err != nil {
			return ErrClientDisconnected
		}
	}

	// Map stop reason
	stopReason := "end_turn"
	if len(s.seenToolIndices) > 0 {
		stopReason = "tool_use"
	} else if finishReason != "" {
		stopReason = s.responseTransformer.mapFinishReason(finishReason)
	}

	var msgUsage *types.Usage
	if usage != nil {
		msgUsage = &types.Usage{
			InputTokens:  usage.PromptTokens,
			OutputTokens: usage.CompletionTokens,
		}
	}

	msgDelta := types.MessageEvent{
		Type: "message_delta",
		Delta: &types.Delta{
			StopReason: stopReason,
		},
		Usage: msgUsage,
	}
	if err := s.writer.WriteEvent(msgDelta); err != nil {
		return ErrClientDisconnected
	}

	stopMsg := types.MessageEvent{
		Type: "message_stop",
	}
	if err := s.writer.WriteEvent(stopMsg); err != nil {
		return ErrClientDisconnected
	}

	return nil
}

// ProxyStream takes an OpenAI streaming response and writes Anthropic-format SSE to the writer.
func (h *StreamHandler) ProxyStream(
	sseWriter *SSEWriter,
	openaiResp io.ReadCloser,
	originalModel string,
	clientCtx context.Context,
) error {
	session, err := newStreamSession(sseWriter, originalModel, h.responseTransformer)
	if err != nil {
		return err
	}

	var lineBuf bytes.Buffer
	readBuf := make([]byte, 4096)
	var lastFinishReason string
	var lastUsage *types.UsageInfo

	for {
		select {
		case <-clientCtx.Done():
			return ErrClientDisconnected
		default:
		}

		n, err := openaiResp.Read(readBuf)
		if n > 0 {
			for i := 0; i < n; i++ {
				b := readBuf[i]
				if b == '\n' {
					line := lineBuf.String()
					lineBuf.Reset()

					if err := h.processSSELine(session, line, &lastFinishReason, &lastUsage); err != nil {
						return err
					}
				} else {
					lineBuf.WriteByte(b)
				}
			}
		}

		if err == io.EOF {
			if lineBuf.Len() > 0 {
				line := lineBuf.String()
				h.processSSELine(session, line, &lastFinishReason, &lastUsage)
			}
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read stream: %w", err)
		}
	}

	return session.close(lastFinishReason, lastUsage)
}

func (h *StreamHandler) processSSELine(
	session *streamSession,
	line string,
	lastFinishReason *string,
	lastUsage **types.UsageInfo,
) error {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "data: ") {
		return nil
	}

	data := strings.TrimPrefix(line, "data: ")
	if data == "" || data == "[DONE]" {
		return nil
	}

	var chunk types.ChatCompletionChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil
	}

	if chunk.Usage != nil {
		*lastUsage = chunk.Usage
	}

	if len(chunk.Choices) == 0 {
		return nil
	}

	choice := chunk.Choices[0]
	if choice.FinishReason != "" {
		*lastFinishReason = choice.FinishReason
	}

	// Handle text deltas (including thinking/reasoning deltas)
	textToken := choice.Delta.Content
	if textToken == "" {
		textToken = choice.Delta.Reasoning
	}
	if textToken == "" {
		textToken = choice.Delta.ReasoningContent
	}
	if textToken != "" {
		if err := session.emitText(textToken); err != nil {
			return err
		}
	}

	// Handle tool call deltas
	if len(choice.Delta.ToolCalls) > 0 {
		for i, tc := range choice.Delta.ToolCalls {
			openaiIdx := i
			if tc.Index != nil {
				openaiIdx = *tc.Index
			}
			if err := session.emitToolCallChunk(tc, openaiIdx); err != nil {
				return err
			}
		}
	}

	return nil
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
