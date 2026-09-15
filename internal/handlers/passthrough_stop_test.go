package handlers

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestProxyAnthropicPassthroughStream_MessageStopMustBeComplete checks that a
// native stream counts as finished only once a whole message_stop event,
// through its terminating blank line, has been relayed.
func TestProxyAnthropicPassthroughStream_MessageStopMustBeComplete(t *testing.T) {
	const deltaEvent = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"

	cases := []struct {
		name    string
		stream  string
		wantErr bool
	}{
		{
			name:    "cut after the message_stop event line",
			stream:  deltaEvent + "event: message_stop\n",
			wantErr: true,
		},
		{
			name:    "cut after the message_stop data line, before the blank line",
			stream:  deltaEvent + "event: message_stop\ndata: {\"type\":\"message_stop\"}\n",
			wantErr: true,
		},
		{
			name:    "complete message_stop event",
			stream:  deltaEvent + "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			wantErr: false,
		},
		{
			name:    "complete message_stop without the optional spaces",
			stream:  deltaEvent + "event:message_stop\ndata:{\"type\":\"message_stop\"}\n\n",
			wantErr: false,
		},
		{
			name:    "data-only message_stop with spaced JSON",
			stream:  deltaEvent + "data: {\"type\": \"message_stop\"}\n\n",
			wantErr: false,
		},
		{
			name:    "complete message_stop with CRLF line endings",
			stream:  strings.ReplaceAll(deltaEvent+"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "\n", "\r\n"),
			wantErr: false,
		},
		{
			name:    "no message_stop at all",
			stream:  deltaEvent,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			err := (&StreamProxy{}).proxyAnthropicPassthroughStream(
				rec, io.NopCloser(strings.NewReader(tc.stream)), time.Minute, ctx, cancel)

			if tc.wantErr && err == nil {
				t.Fatalf("expected an error for a cut-off stream, got nil; client received %q", rec.Body.String())
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for a complete stream: %v", err)
			}
			if !tc.wantErr && rec.Body.String() != tc.stream {
				t.Fatalf("relayed bytes changed:\n got %q\nwant %q", rec.Body.String(), tc.stream)
			}
		})
	}
}
