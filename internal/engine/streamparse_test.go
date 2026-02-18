package engine

import (
	"testing"
)

func TestParseUsageFromContent_Empty(t *testing.T) {
	stats := ParseUsageFromContent("")
	if stats.InputTokens != 0 || stats.OutputTokens != 0 || stats.Model != "" {
		t.Errorf("empty content: got %+v, want zero", stats)
	}
}

func TestParseUsageFromContent_SingleResult(t *testing.T) {
	content := `{"type":"result","subtype":"success","usage":{"input_tokens":1234,"output_tokens":567}}`
	stats := ParseUsageFromContent(content)
	if stats.InputTokens != 1234 {
		t.Errorf("InputTokens = %d, want 1234", stats.InputTokens)
	}
	if stats.OutputTokens != 567 {
		t.Errorf("OutputTokens = %d, want 567", stats.OutputTokens)
	}
}

func TestParseUsageFromContent_MultipleResults(t *testing.T) {
	content := `{"type":"result","subtype":"success","usage":{"input_tokens":100,"output_tokens":50}}
{"type":"result","subtype":"success","usage":{"input_tokens":200,"output_tokens":75}}`
	stats := ParseUsageFromContent(content)
	if stats.InputTokens != 300 {
		t.Errorf("InputTokens = %d, want 300", stats.InputTokens)
	}
	if stats.OutputTokens != 125 {
		t.Errorf("OutputTokens = %d, want 125", stats.OutputTokens)
	}
}

func TestParseUsageFromContent_ModelExtraction(t *testing.T) {
	content := `{"type":"assistant","message":{"model":"claude-sonnet-4-5-20250514","id":"msg_123"}}`
	stats := ParseUsageFromContent(content)
	if stats.Model != "claude-sonnet-4-5-20250514" {
		t.Errorf("Model = %q, want %q", stats.Model, "claude-sonnet-4-5-20250514")
	}
}

func TestParseUsageFromContent_MixedContent(t *testing.T) {
	content := `{"type":"assistant","message":{"model":"claude-opus-4-6","id":"msg_1"}}
{"type":"content_block_start","index":0}
some non-json debug line
{"type":"content_block_delta","index":0,"delta":{"text":"hello"}}
{"type":"result","subtype":"success","usage":{"input_tokens":5000,"output_tokens":2000}}`
	stats := ParseUsageFromContent(content)
	if stats.InputTokens != 5000 {
		t.Errorf("InputTokens = %d, want 5000", stats.InputTokens)
	}
	if stats.OutputTokens != 2000 {
		t.Errorf("OutputTokens = %d, want 2000", stats.OutputTokens)
	}
	if stats.Model != "claude-opus-4-6" {
		t.Errorf("Model = %q, want %q", stats.Model, "claude-opus-4-6")
	}
}

func TestParseUsageFromContent_MalformedJSON(t *testing.T) {
	content := `{not valid json}
{"type":"result","subtype":"success","usage":{"input_tokens":100,"output_tokens":50}}
{also broken`
	stats := ParseUsageFromContent(content)
	if stats.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", stats.InputTokens)
	}
	if stats.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", stats.OutputTokens)
	}
}

func TestParseUsageFromContent_NoUsageInResult(t *testing.T) {
	content := `{"type":"result","subtype":"success"}`
	stats := ParseUsageFromContent(content)
	if stats.InputTokens != 0 || stats.OutputTokens != 0 {
		t.Errorf("no usage field: got %+v, want zero tokens", stats)
	}
}

func TestParseUsageFromContent_SkipsNonJSONLines(t *testing.T) {
	content := `plain text line
  leading space line
{"type":"result","subtype":"success","usage":{"input_tokens":42,"output_tokens":7}}`
	stats := ParseUsageFromContent(content)
	if stats.InputTokens != 42 {
		t.Errorf("InputTokens = %d, want 42", stats.InputTokens)
	}
}
