package config

import (
	"slices"
	"testing"
)

// TestUnknownTopLevelFields covers config keys this build does not read, such
// as ogc's `model_mapping`: json.Unmarshal ignores them, so a config carrying
// one used to load with no error and no routing.
func TestUnknownTopLevelFields(t *testing.T) {
	tests := []struct {
		name string
		data string
		want []string
	}{
		{
			name: "ogc model_mapping",
			data: `{"port":3456,"model_mapping":{"claude-opus-4-8":"qwen3.8-max"}}`,
			want: []string{"model_mapping"},
		},
		{
			name: "known fields only",
			data: `{"port":3456,"api_key":"k","models":{"default":{"provider":"opencode-go","model_id":"glm-5.3"}}}`,
			want: nil,
		},
		{
			name: "several unknown, sorted",
			data: `{"zzz":1,"aaa":2,"port":3456}`,
			want: []string{"aaa", "zzz"},
		},
		{
			name: "not an object",
			data: `[]`,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unknownTopLevelFields([]byte(tt.data))
			if !slices.Equal(got, tt.want) {
				t.Errorf("unknownTopLevelFields() = %v, want %v", got, tt.want)
			}
		})
	}
}
