package transformer

import (
	"io"
	"strings"
	"testing"
)

func TestCollectAnthropicStream_KeepsThinkingSignature(t *testing.T) {
	sseData := `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me check."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-123"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}
`
	resp, err := CollectAnthropicStream(io.NopCloser(strings.NewReader(sseData)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Thinking != "Let me check." || resp.Content[0].Signature != "sig-123" {
		t.Errorf("thinking block not assembled with its signature: %+v", resp.Content)
	}
}
