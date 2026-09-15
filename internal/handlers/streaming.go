package handlers

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/routatic/proxy/internal/core"
	"github.com/routatic/proxy/internal/transformer"
)

// StreamProxy handles SSE stream forwarding from various upstream wire formats
// to Anthropic-format SSE events. It wraps transformer.StreamHandler and
// dispatches by WireFormat.
type StreamProxy struct {
	handler *transformer.StreamHandler
}

// NewStreamProxy creates a new StreamProxy.
func NewStreamProxy() *StreamProxy {
	return &StreamProxy{
		handler: transformer.NewStreamHandler(),
	}
}

// ProxyStream proxies an upstream SSE stream to the response writer, transforming
// events from the wire format to Anthropic SSE events.
func (sp *StreamProxy) ProxyStream(
	w http.ResponseWriter,
	body io.ReadCloser,
	wireFormat core.WireFormat,
	modelID string,
	clientCtx context.Context,
	idleTimeout time.Duration,
	cancel context.CancelFunc,
) error {
	switch wireFormat {
	case core.WireFormatAnthropic:
		return sp.proxyAnthropicPassthroughStream(w, body, idleTimeout, clientCtx, cancel)
	case core.WireFormatOpenAIResponses:
		return sp.handler.ProxyResponsesStream(w, body, modelID, clientCtx, idleTimeout, cancel)
	case core.WireFormatGemini:
		return sp.handler.ProxyGeminiStream(w, body, modelID, clientCtx, idleTimeout, cancel)
	default:
		return sp.proxyOpenAIStream(w, body, modelID, clientCtx, idleTimeout, cancel)
	}
}

// proxyOpenAIStream delegates to the transformer's ProxyStream.
func (sp *StreamProxy) proxyOpenAIStream(
	w http.ResponseWriter,
	body io.ReadCloser,
	modelID string,
	clientCtx context.Context,
	idleTimeout time.Duration,
	cancel context.CancelFunc,
) error {
	return sp.handler.ProxyStream(w, body, modelID, clientCtx, idleTimeout, cancel)
}

// proxyAnthropicPassthroughStream forwards an Anthropic-format SSE stream to
// the client unchanged, with an idle watchdog. No transformation is needed
// since the upstream already speaks Anthropic format.
//
// Events are relayed whole, through their terminating blank line, and written
// to w in a single Write call per event (bufio.Reader.ReadBytes has no line
// length cap, so an event larger than any fixed buffer is still relayed
// intact). w's own Write is expected to serialize concurrent writers (e.g. a
// keepalive heartbeat sharing the same http.ResponseWriter), so a whole-event
// write is the unit that can never be split by an interleaved write from
// elsewhere — only ever inserted between two events. A stream that ends
// before a message_stop event is an error, not a clean end.
func (sp *StreamProxy) proxyAnthropicPassthroughStream(
	w http.ResponseWriter,
	body io.ReadCloser,
	idleTimeout time.Duration,
	clientCtx context.Context,
	cancel context.CancelFunc,
) error {
	defer func() { _ = body.Close() }()
	defer cancel()

	flusher, _ := w.(http.Flusher)
	reader := bufio.NewReader(body)
	var event bytes.Buffer
	sawStop := false
	ping := transformer.StartIdleWatchdog(clientCtx, cancel, idleTimeout)

	flushEvent := func() error {
		if event.Len() == 0 {
			return nil
		}
		if _, werr := w.Write(event.Bytes()); werr != nil {
			return transformer.ErrClientDisconnected
		}
		if flusher != nil {
			flusher.Flush()
		}
		event.Reset()
		return nil
	}

	for {
		select {
		case <-clientCtx.Done():
			if clientCtx.Err() == nil {
				return transformer.ErrStreamIdle
			}
			return transformer.ErrClientDisconnected
		default:
		}

		line, rerr := reader.ReadBytes('\n')
		if len(line) > 0 {
			ping()
			event.Write(line)
			if bytes.HasPrefix(line, []byte("event: message_stop")) ||
				bytes.HasPrefix(line, []byte(`data: {"type":"message_stop"`)) {
				sawStop = true
			}
		}

		// A blank line is the SSE event boundary. Flush the whole event once
		// we've reached it, or when the stream ends mid-event so nothing
		// buffered is lost.
		atBoundary := len(bytes.TrimSpace(line)) == 0 && event.Len() > len(line)
		if atBoundary || (rerr != nil && event.Len() > 0) {
			if err := flushEvent(); err != nil {
				return err
			}
		}

		if rerr == io.EOF {
			if !sawStop {
				return fmt.Errorf("upstream stream ended before message_stop")
			}
			return nil
		}
		if rerr != nil {
			if transformer.IsIdleTimeout(rerr) {
				return transformer.ErrStreamIdle
			}
			if errors.Is(rerr, context.Canceled) || errors.Is(rerr, transformer.ErrStreamReadCanceled) || clientCtx.Err() == context.Canceled {
				if clientCtx.Err() == nil {
					return transformer.ErrStreamIdle
				}
				return transformer.ErrClientDisconnected
			}
			return fmt.Errorf("failed to copy response: %w", rerr)
		}
	}
}
