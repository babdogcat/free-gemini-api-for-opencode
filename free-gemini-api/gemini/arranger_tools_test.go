package gemini

import (
	"encoding/json"
	"testing"
)

func TestParseSingleToolCallTolerant(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantOK  bool
		wantArg string
	}{
		{"well formed", `{"name":"shell","arguments":{"command":"ls -la"}}`, true, `{"command":"ls -la"}`},
		{"unescaped inner quotes", `{"name":"shell","arguments":{"command":"ffprobe "Concerning Whispering Waves.mp3" -show_format"}}`, true, `{"command":"ffprobe \"Concerning Whispering Waves.mp3\" -show_format"}`},
		{"trailing comma", `{"name":"shell","arguments":{"command":"ls -la",},}`, true, `{"command":"ls -la"}`},
		{"arguments as string", `{"name":"shell","arguments":"{\"command\":\"ls\"}"}`, true, `{"command":"ls"}`},
		{"no arguments key", `{"name":"shell"}`, true, `{}`},
		{"smart quotes", `{"name":"shell","arguments":{"command":"ls -la"}}`, true, `{"command":"ls -la"}`},
		{"function wrapper", `{"function":{"name":"shell","arguments":{"command":"ls"}}}`, true, `{"command":"ls"}`},
		{"flat arguments", `{"name":"shell","command":"ls -la"}`, true, `{"command":"ls -la"}`},
		{"no name", `{"arguments":{"command":"ls"}}`, false, ""},
	}
	for _, c := range cases {
		tc, ok := parseSingleToolCall(c.payload)
		if ok != c.wantOK {
			t.Fatalf("%s: ok=%v want %v", c.name, ok, c.wantOK)
		}
		if !ok {
			continue
		}
		var got, want any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &got); err != nil {
			t.Fatalf("%s: arguments are not valid JSON: %q", c.name, tc.Function.Arguments)
		}
		_ = json.Unmarshal([]byte(c.wantArg), &want)
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(want)
		if string(gb) != string(wb) {
			t.Errorf("%s: args=%s want %s", c.name, gb, wb)
		}
		t.Logf("✅ %-24s -> %s", c.name, tc.Function.Arguments)
	}
}

func TestBackfillEmptyToolArgs(t *testing.T) {
	raw := "Let me inspect the file.\n\n```bash\nffprobe \"Concerning Whispering Waves.mp3\" -show_format\n```\n\n" +
		"<tool_call>\n{\"name\": \"shell\"}\n</tool_call>"
	out := ArrangeGeminiOutput(raw)
	if len(out.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(out.ToolCalls))
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(out.ToolCalls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("args not JSON: %q", out.ToolCalls[0].Function.Arguments)
	}
	if args["command"] == nil || args["command"] == "" {
		t.Fatalf("command was not backfilled: %s", out.ToolCalls[0].Function.Arguments)
	}
	t.Logf("✅ backfilled -> %s", out.ToolCalls[0].Function.Arguments)
}

func TestEmptyArgsStayEmptyWithoutFence(t *testing.T) {
	out := ArrangeGeminiOutput("<tool_call>\n{\"name\": \"shell\"}\n</tool_call>")
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Function.Arguments != "{}" {
		t.Fatalf("unexpected: %+v", out.ToolCalls)
	}
}
