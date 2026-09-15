package core

import "encoding/json"

// SetTopLevelFields returns raw with each named top-level field set to the JSON
// encoding of its value. Every other field keeps its original value, so fields
// the proxy has no model of (thinking.display, output_config,
// context_management, cache_control.ttl) reach the upstream unchanged.
func SetTopLevelFields(raw json.RawMessage, fields map[string]any) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	for key, value := range fields {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		obj[key] = encoded
	}
	return json.Marshal(obj)
}
