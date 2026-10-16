package qoder

import "testing"

func TestCloudToolsFromRequestRenamesConflicts(t *testing.T) {
	raw := []byte(`[
		{"type":"function","function":{"name":"Bash","description":"run","parameters":{"type":"object"}}},
		{"type":"function","function":{"name":"shell","description":"run","parameters":{"type":"object"}}},
		{"type":"function","function":{"name":"mcp__srv__tool","description":"mcp","parameters":{"type":"object"}}},
		{"type":"function","function":{"name":"Bash","description":"dup","parameters":{"type":"object"}}}
	]`)
	tools, reverse := cloudToolsFromRequest(raw)
	if len(tools) != 4 {
		t.Fatalf("want 4 tools, got %d", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[stringValue(tool["name"])] = true
	}
	if !names["cc_Bash"] {
		t.Errorf("Bash should be renamed to cc_Bash, got %v", names)
	}
	if !names["shell"] {
		t.Errorf("shell should keep its name, got %v", names)
	}
	if !names["cc_mcp__srv__tool"] {
		t.Errorf("mcp__ name should be renamed, got %v", names)
	}
	if reverse["cc_Bash"] != "Bash" {
		t.Errorf("reverse map for cc_Bash = %q", reverse["cc_Bash"])
	}
	if reverse["cc_mcp__srv__tool"] != "mcp__srv__tool" {
		t.Errorf("reverse map for mcp tool = %q", reverse["cc_mcp__srv__tool"])
	}
	if reverse["shell"] != "" {
		t.Errorf("non-renamed tool should not be in reverse map")
	}
}

func TestNormalizeModelFallsBackForUnknown(t *testing.T) {
	if got := normalizeModel("claude-sonnet-5", "auto"); got != "auto" {
		t.Errorf("unknown model should fall back to auto, got %q", got)
	}
	if got := normalizeModel("dmodel", "auto"); got != "dmodel" {
		t.Errorf("known model should pass through, got %q", got)
	}
}

func TestParseTextToolCallsAntml(t *testing.T) {
	text := "先跑一下。\n<antml:function_calls>\n<antml:invoke name=\"exec_command\">\n<antml:parameter name=\"cmd\">echo hi</antml:parameter>\n</antml:invoke>\n</antml:function_calls>"
	raw, cleaned, ok := parseTextToolCalls(text)
	if !ok {
		t.Fatal("expected parse success")
	}
	calls := ParseToolCallDeltas(raw)
	if len(calls) != 1 || calls[0].FunctionName != "exec_command" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
	if calls[0].ArgumentsFragment != `{"cmd":"echo hi"}` {
		t.Fatalf("unexpected args: %s", calls[0].ArgumentsFragment)
	}
	if cleaned != "先跑一下。" {
		t.Fatalf("leaked block should be stripped, got %q", cleaned)
	}
}

func TestParseTextToolCallsToolCallTag(t *testing.T) {
	text := "<tool_call>exec_command\n<arg_value>cmd: echo bridge-ok</arg_value>yield_time_ms: 10000</tool_call>"
	raw, cleaned, ok := parseTextToolCalls(text)
	if !ok {
		t.Fatal("expected parse success")
	}
	calls := ParseToolCallDeltas(raw)
	if len(calls) != 1 || calls[0].FunctionName != "exec_command" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
	if cleaned != "" {
		t.Fatalf("expected empty cleaned text, got %q", cleaned)
	}
	if calls[0].ArgumentsFragment != `{"cmd":"echo bridge-ok","yield_time_ms":"10000"}` {
		t.Fatalf("unexpected args: %s", calls[0].ArgumentsFragment)
	}
}

func TestParseTextToolCallsPlainText(t *testing.T) {
	if _, _, ok := parseTextToolCalls("这就是一段普通回答，没有工具调用。"); ok {
		t.Fatal("plain text should not parse as tool calls")
	}
}
