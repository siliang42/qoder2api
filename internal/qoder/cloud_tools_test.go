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
