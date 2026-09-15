package handlers

import (
	"bytes"
	"encoding/json"
)

const messageStopEventType = "message_stop"

// isMessageStopEvent reports whether a complete SSE event is Anthropic's
// message_stop, going by either its event field or the type in its data.
// Per the SSE spec a single space after the field colon is optional, and
// data JSON may be formatted with or without spaces.
func isMessageStopEvent(event []byte) bool {
	for _, rawLine := range bytes.Split(event, []byte("\n")) {
		line := bytes.TrimSuffix(rawLine, []byte("\r"))
		if value, ok := sseFieldValue(line, "event"); ok && string(value) == messageStopEventType {
			return true
		}
		if value, ok := sseFieldValue(line, "data"); ok {
			var payload struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(value, &payload) == nil && payload.Type == messageStopEventType {
				return true
			}
		}
	}
	return false
}

// sseFieldValue returns the value of an SSE "name: value" line, removing the
// single optional space after the colon.
func sseFieldValue(line []byte, name string) ([]byte, bool) {
	prefix := []byte(name + ":")
	if !bytes.HasPrefix(line, prefix) {
		return nil, false
	}
	return bytes.TrimPrefix(line[len(prefix):], []byte(" ")), true
}
