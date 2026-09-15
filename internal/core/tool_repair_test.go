package core

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/routatic/proxy/pkg/types"
)

func repairMsg(role, content string) types.Message {
	return types.Message{Role: role, Content: json.RawMessage(content)}
}

// placeholderResult is the tool_result block the repair synthesizes for id.
func placeholderResult(id string) string {
	content, _ := json.Marshal(InterruptedToolCallPlaceholder)
	return `{"type":"tool_result","tool_use_id":"` + id + `","content":` + string(content) + `}`
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return reflect.DeepEqual(av, bv)
}

// assertOneAnswerPerCall checks the invariant every translation relies on:
// each tool_use is answered by exactly one tool_result in the history, and
// that result sits in the user message directly after the call.
func assertOneAnswerPerCall(t *testing.T, messages []types.Message) {
	t.Helper()
	answers := make(map[string]int)
	for _, m := range messages {
		for _, b := range m.ContentBlocks() {
			if b.Type == "tool_result" {
				answers[b.ToolUseID]++
			}
		}
	}
	for i, m := range messages {
		if m.Role != "assistant" {
			continue
		}
		for _, b := range m.ContentBlocks() {
			if b.Type != "tool_use" {
				continue
			}
			if answers[b.ID] != 1 {
				t.Errorf("tool_use %s answered %d times, want 1", b.ID, answers[b.ID])
			}
			answeredNext := false
			if i+1 < len(messages) && messages[i+1].Role == "user" {
				for _, r := range messages[i+1].ContentBlocks() {
					answeredNext = answeredNext || (r.Type == "tool_result" && r.ToolUseID == b.ID)
				}
			}
			if !answeredNext {
				t.Errorf("tool_use %s is not answered by the message directly after it", b.ID)
			}
		}
	}
}

// TestRepairDanglingToolCalls covers the interrupted-tool-call scenarios once,
// on the Anthropic history both the chat-completions and Responses
// translations are built from. Both upstream formats reject a tool call left
// without an answer (e.g. the user hit Ctrl+C before the result came back),
// and a call answered twice.
func TestRepairDanglingToolCalls(t *testing.T) {
	tests := []struct {
		name string
		in   []types.Message
		// want is nil when the history must come back byte-for-byte unchanged.
		want []types.Message
	}{
		{
			name: "all calls answered, no change",
			in: []types.Message{
				repairMsg("user", `"list the files in /tmp"`),
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"ls /tmp"}}]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_01","content":"file1\nfile2"}]`),
			},
		},
		{
			name: "no calls answered, both synthesized ahead of the user text",
			in: []types.Message{
				repairMsg("user", `"read two files"`),
				repairMsg("assistant", `[
					{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}},
					{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}}
				]`),
				repairMsg("user", `"actually never mind, do something else"`),
			},
			want: []types.Message{
				repairMsg("user", `"read two files"`),
				repairMsg("assistant", `[
					{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}},
					{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}}
				]`),
				repairMsg("user", `[`+placeholderResult("toolu_a")+`,`+placeholderResult("toolu_b")+`,
					{"type":"text","text":"actually never mind, do something else"}]`),
			},
		},
		{
			name: "some of several parallel calls answered, real result first",
			in: []types.Message{
				repairMsg("assistant", `[
					{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}},
					{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}},
					{"type":"tool_use","id":"toolu_c","name":"Read","input":{"path":"/c"}}
				]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_b","content":"B contents"}]`),
			},
			want: []types.Message{
				repairMsg("assistant", `[
					{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}},
					{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}},
					{"type":"tool_use","id":"toolu_c","name":"Read","input":{"path":"/c"}}
				]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_b","content":"B contents"},`+
					placeholderResult("toolu_a")+`,`+placeholderResult("toolu_c")+`]`),
			},
		},
		{
			name: "results arrive out of call order, arrival order kept",
			in: []types.Message{
				repairMsg("assistant", `[
					{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}},
					{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}}
				]`),
				repairMsg("user", `[
					{"type":"tool_result","tool_use_id":"toolu_b","content":"B contents"},
					{"type":"tool_result","tool_use_id":"toolu_a","content":"A contents"}
				]`),
			},
		},
		{
			name: "multiple calls in one message, one answered",
			in: []types.Message{
				repairMsg("assistant", `[
					{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}},
					{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}}
				]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_a","content":"A contents"}]`),
			},
			want: []types.Message{
				repairMsg("assistant", `[
					{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}},
					{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}}
				]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_a","content":"A contents"},`+
					placeholderResult("toolu_b")+`]`),
			},
		},
		{
			name: "real result after a later tool call is matched, not doubled",
			in: []types.Message{
				repairMsg("user", `"read /a"`),
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}}]`),
				repairMsg("user", `"wait, read /b first"`),
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}}]`),
				repairMsg("user", `[
					{"type":"tool_result","tool_use_id":"toolu_b","content":"B contents"},
					{"type":"tool_result","tool_use_id":"toolu_a","content":"A contents"}
				]`),
			},
			want: []types.Message{
				repairMsg("user", `"read /a"`),
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}}]`),
				repairMsg("user", `[
					{"type":"tool_result","tool_use_id":"toolu_a","content":"A contents"},
					{"type":"text","text":"wait, read /b first"}
				]`),
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"/b"}}]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_b","content":"B contents"}]`),
			},
		},
		{
			name: "result after user text in the same message moves ahead of it",
			in: []types.Message{
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"Write","input":{"name":"draft"}}]`),
				repairMsg("user", `[
					{"type":"text","text":"now continue"},
					{"type":"tool_result","tool_use_id":"toolu_a","content":"created"}
				]`),
			},
			want: []types.Message{
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"Write","input":{"name":"draft"}}]`),
				repairMsg("user", `[
					{"type":"tool_result","tool_use_id":"toolu_a","content":"created"},
					{"type":"text","text":"now continue"}
				]`),
			},
		},
		{
			name: "result in a second user message is pulled forward, emptied message dropped",
			in: []types.Message{
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}}]`),
				repairMsg("user", `"stop"`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_a","content":"A contents"}]`),
			},
			want: []types.Message{
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}}]`),
				repairMsg("user", `[
					{"type":"tool_result","tool_use_id":"toolu_a","content":"A contents"},
					{"type":"text","text":"stop"}
				]`),
			},
		},
		{
			name: "call ending the history gets an appended answer",
			in: []types.Message{
				repairMsg("user", `"weather in Kigali?"`),
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"get_weather","input":{"city":"Kigali"}}]`),
			},
			want: []types.Message{
				repairMsg("user", `"weather in Kigali?"`),
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"get_weather","input":{"city":"Kigali"}}]`),
				repairMsg("user", `[`+placeholderResult("toolu_a")+`]`),
			},
		},
		{
			name: "call followed by a non-user message gets an inserted answer",
			in: []types.Message{
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}}]`),
				repairMsg("system", `"reminder"`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_a","content":"A contents"}]`),
			},
			want: []types.Message{
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"/a"}}]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_a","content":"A contents"}]`),
				repairMsg("system", `"reminder"`),
			},
		},
		{
			name: "string content messages untouched",
			in: []types.Message{
				repairMsg("user", `"hi"`),
				repairMsg("assistant", `"hello"`),
				repairMsg("user", `"bye"`),
			},
		},
		{
			name: "no tool calls, identical output",
			in: []types.Message{
				repairMsg("user", `[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]`),
				repairMsg("assistant", `[{"type":"thinking","thinking":"hm","signature":"sig"},{"type":"text","text":"hello"}]`),
				repairMsg("user", `[{"type":"custom_provider_block","payload":{"value":42}}]`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := make([]json.RawMessage, len(tt.in))
			for i, m := range tt.in {
				before[i] = append(json.RawMessage(nil), m.Content...)
			}

			got := RepairDanglingToolCalls(tt.in)

			for i, m := range tt.in {
				if !bytes.Equal(m.Content, before[i]) {
					t.Fatalf("input message %d was mutated: %s", i, m.Content)
				}
			}

			if tt.want == nil {
				if len(got) != len(tt.in) {
					t.Fatalf("len = %d, want %d unchanged messages", len(got), len(tt.in))
				}
				for i := range got {
					if got[i].Role != tt.in[i].Role || !bytes.Equal(got[i].Content, tt.in[i].Content) {
						t.Errorf("message %d = %s %s, want unchanged %s %s",
							i, got[i].Role, got[i].Content, tt.in[i].Role, tt.in[i].Content)
					}
				}
			} else {
				if len(got) != len(tt.want) {
					t.Fatalf("len = %d, want %d; got %+v", len(got), len(tt.want), got)
				}
				for i := range got {
					if got[i].Role != tt.want[i].Role || !jsonEqual(t, got[i].Content, tt.want[i].Content) {
						t.Errorf("message %d = %s %s, want %s %s",
							i, got[i].Role, got[i].Content, tt.want[i].Role, tt.want[i].Content)
					}
				}
			}

			assertOneAnswerPerCall(t, got)
		})
	}
}

// TestRepairDanglingToolCallsMalformedHistories covers histories the upstream
// would reject anyway: a tool_use id reused by a later turn, and a result
// stranded after a text-only assistant turn. The repair must not make them
// worse — taking one turn's result for another, or dropping a message so that
// roles stop alternating or the history ends on the assistant (which an
// Anthropic-format upstream reads as a prefill to continue).
func TestRepairDanglingToolCallsMalformedHistories(t *testing.T) {
	tests := []struct {
		name string
		in   []types.Message
		want []types.Message
	}{
		{
			name: "a tool_use id reused by a later turn keeps each turn's own result",
			in: []types.Message{
				repairMsg("assistant", `[{"type":"tool_use","id":"X","name":"Read","input":{"path":"/a"}}]`),
				repairMsg("user", `"interrupted, moving on"`),
				repairMsg("assistant", `[{"type":"tool_use","id":"X","name":"Read","input":{"path":"/b"}}]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"X","content":"B contents"}]`),
			},
			want: []types.Message{
				repairMsg("assistant", `[{"type":"tool_use","id":"X","name":"Read","input":{"path":"/a"}}]`),
				repairMsg("user", `[`+placeholderResult("X")+`,{"type":"text","text":"interrupted, moving on"}]`),
				repairMsg("assistant", `[{"type":"tool_use","id":"X","name":"Read","input":{"path":"/b"}}]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"X","content":"B contents"}]`),
			},
		},
		{
			name: "result stranded after a text-only assistant stays, history still ends on the user",
			in: []types.Message{
				repairMsg("user", `"check the weather"`),
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"get_weather","input":{}}]`),
				repairMsg("user", `[{"type":"text","text":"stop"}]`),
				repairMsg("assistant", `[{"type":"text","text":"ok"}]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_a","content":"A"}]`),
			},
			want: []types.Message{
				repairMsg("user", `"check the weather"`),
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"get_weather","input":{}}]`),
				repairMsg("user", `[`+placeholderResult("toolu_a")+`,{"type":"text","text":"stop"}]`),
				repairMsg("assistant", `[{"type":"text","text":"ok"}]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_a","content":"A"}]`),
			},
		},
		{
			name: "result stranded after a text-only assistant stays, no adjacent assistants",
			in: []types.Message{
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"get_weather","input":{}}]`),
				repairMsg("user", `[{"type":"text","text":"stop"}]`),
				repairMsg("assistant", `[{"type":"text","text":"ok"}]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_a","content":"A"}]`),
				repairMsg("assistant", `[{"type":"text","text":"more"}]`),
				repairMsg("user", `"continue"`),
			},
			want: []types.Message{
				repairMsg("assistant", `[{"type":"tool_use","id":"toolu_a","name":"get_weather","input":{}}]`),
				repairMsg("user", `[`+placeholderResult("toolu_a")+`,{"type":"text","text":"stop"}]`),
				repairMsg("assistant", `[{"type":"text","text":"ok"}]`),
				repairMsg("user", `[{"type":"tool_result","tool_use_id":"toolu_a","content":"A"}]`),
				repairMsg("assistant", `[{"type":"text","text":"more"}]`),
				repairMsg("user", `"continue"`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairDanglingToolCalls(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d; got %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i].Role != tt.want[i].Role || !jsonEqual(t, got[i].Content, tt.want[i].Content) {
					t.Errorf("message %d = %s %s, want %s %s",
						i, got[i].Role, got[i].Content, tt.want[i].Role, tt.want[i].Content)
				}
			}
		})
	}
}
