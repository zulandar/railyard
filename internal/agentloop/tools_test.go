package agentloop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

func TestBashTool_RunsAndCapturesOutput(t *testing.T) {
	dir := t.TempDir()
	bash := NewBashTool(dir)

	out, err := bash.Execute(context.Background(), mustJSON(t, map[string]string{"command": "echo hello"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("output = %q, want to contain hello", out)
	}
}

func TestBashTool_RunsInWorktree(t *testing.T) {
	dir := t.TempDir()
	bash := NewBashTool(dir)

	out, err := bash.Execute(context.Background(), mustJSON(t, map[string]string{"command": "pwd"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// macOS /tmp is a symlink to /private/tmp; compare basenames to stay robust.
	if !strings.Contains(out, filepath.Base(dir)) {
		t.Errorf("pwd output = %q, want to contain worktree %q", out, dir)
	}
}

func TestBashTool_CapturesStderr(t *testing.T) {
	dir := t.TempDir()
	bash := NewBashTool(dir)

	out, err := bash.Execute(context.Background(), mustJSON(t, map[string]string{"command": "echo oops >&2"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "oops") {
		t.Errorf("output = %q, want to contain stderr 'oops'", out)
	}
}

func TestBashTool_NonZeroExitIsResultNotError(t *testing.T) {
	dir := t.TempDir()
	bash := NewBashTool(dir)

	// A failing command must surface its output + exit status as the tool
	// result (so the model can react), not as a Go error that drops output.
	out, err := bash.Execute(context.Background(), mustJSON(t, map[string]string{"command": "echo before; exit 3"}))
	if err != nil {
		t.Fatalf("Execute returned Go error for non-zero exit: %v", err)
	}
	if !strings.Contains(out, "before") {
		t.Errorf("output = %q, want to retain stdout before failure", out)
	}
	if !strings.Contains(out, "3") {
		t.Errorf("output = %q, want to mention exit status 3", out)
	}
}

func TestBashTool_BadArgs(t *testing.T) {
	bash := NewBashTool(t.TempDir())
	if _, err := bash.Execute(context.Background(), json.RawMessage(`{not json`)); err == nil {
		t.Error("expected error for malformed args, got nil")
	}
}

func TestBashTool_Definition(t *testing.T) {
	if NewBashTool(t.TempDir()).Definition().Name != "bash" {
		t.Error("bash tool name mismatch")
	}
}

func TestReadFileTool(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("contents here"), 0o644); err != nil {
		t.Fatal(err)
	}
	rf := NewReadFileTool(dir)

	out, err := rf.Execute(context.Background(), mustJSON(t, map[string]string{"path": "a.txt"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "contents here" {
		t.Errorf("read = %q, want %q", out, "contents here")
	}
}

func TestReadFileTool_Missing(t *testing.T) {
	rf := NewReadFileTool(t.TempDir())
	if _, err := rf.Execute(context.Background(), mustJSON(t, map[string]string{"path": "nope.txt"})); err == nil {
		t.Error("expected error reading missing file, got nil")
	}
}

func TestWriteFileTool_CreatesNestedDirs(t *testing.T) {
	dir := t.TempDir()
	wf := NewWriteFileTool(dir)

	_, err := wf.Execute(context.Background(), mustJSON(t, map[string]string{
		"path": "sub/dir/b.txt", "content": "new body",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sub", "dir", "b.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "new body" {
		t.Errorf("file = %q, want %q", got, "new body")
	}
}

func TestEditFileTool(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("hello OLD world"), 0o644); err != nil {
		t.Fatal(err)
	}
	ef := NewEditFileTool(dir)

	_, err := ef.Execute(context.Background(), mustJSON(t, map[string]string{
		"path": "c.txt", "old_string": "OLD", "new_string": "NEW",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "c.txt"))
	if string(got) != "hello NEW world" {
		t.Errorf("edited file = %q, want %q", got, "hello NEW world")
	}
}

func TestEditFileTool_NotFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	ef := NewEditFileTool(dir)
	_, err := ef.Execute(context.Background(), mustJSON(t, map[string]string{
		"path": "c.txt", "old_string": "zzz", "new_string": "q",
	}))
	if err == nil {
		t.Error("expected error when old_string absent, got nil")
	}
}

func TestEditFileTool_AmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("x x x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ef := NewEditFileTool(dir)
	_, err := ef.Execute(context.Background(), mustJSON(t, map[string]string{
		"path": "c.txt", "old_string": "x", "new_string": "y",
	}))
	if err == nil {
		t.Error("expected error for non-unique old_string, got nil")
	}
}

func TestFileTools_RejectPathEscape(t *testing.T) {
	dir := t.TempDir()
	// Seed a file OUTSIDE the worktree that an escape attempt would target.
	outside := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(outside, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(outside) }()

	cases := []struct {
		name string
		tool Tool
		args map[string]string
	}{
		{"read_dotdot", NewReadFileTool(dir), map[string]string{"path": "../secret.txt"}},
		{"read_abs_outside", NewReadFileTool(dir), map[string]string{"path": outside}},
		{"write_dotdot", NewWriteFileTool(dir), map[string]string{"path": "../escape.txt", "content": "x"}},
		{"edit_dotdot", NewEditFileTool(dir), map[string]string{"path": "../secret.txt", "old_string": "top", "new_string": "no"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.tool.Execute(context.Background(), mustJSON(t, tc.args))
			if err == nil {
				t.Fatalf("%s: expected path-escape rejection, got nil", tc.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "escape") &&
				!strings.Contains(strings.ToLower(err.Error()), "outside") {
				t.Errorf("%s: error = %q, want to explain the path is outside the worktree", tc.name, err.Error())
			}
		})
	}
	// The outside file must be untouched.
	if got, _ := os.ReadFile(outside); string(got) != "top secret" {
		t.Errorf("outside file was modified: %q", got)
	}
}

func TestProfileToolSets(t *testing.T) {
	dir := t.TempDir()
	// Without CocoIndex params, codesearch is NOT registered (gating).
	if names := toolNames(DispatchTools(dir, nil)); names != "bash,read_file" {
		t.Errorf("DispatchTools(nil) = %s, want bash,read_file", names)
	}
	if names := toolNames(EngineTools(dir, nil)); names != "bash,read_file,write_file,edit_file" {
		t.Errorf("EngineTools(nil) = %s, want bash,read_file,write_file,edit_file", names)
	}
	// With CocoIndex params, codesearch is appended to each profile.
	cs := &CodeSearchParams{PythonPath: "/x/python", ScriptPath: "/x/mcp_server.py"}
	if names := toolNames(DispatchTools(dir, cs)); names != "bash,read_file,codesearch" {
		t.Errorf("DispatchTools(params) = %s, want bash,read_file,codesearch", names)
	}
	if names := toolNames(EngineTools(dir, cs)); names != "bash,read_file,write_file,edit_file,codesearch" {
		t.Errorf("EngineTools(params) = %s, want bash,read_file,write_file,edit_file,codesearch", names)
	}
}

func TestReadOnlyToolSet(t *testing.T) {
	dir := t.TempDir()
	// Triage/review profile: read_file only (no bash/write/edit), plus codesearch
	// when configured. It must never expose a tool that can mutate the tree.
	if names := toolNames(ReadOnlyTools(dir, nil)); names != "read_file" {
		t.Errorf("ReadOnlyTools(nil) = %s, want read_file", names)
	}
	cs := &CodeSearchParams{PythonPath: "/x/python", ScriptPath: "/x/mcp_server.py"}
	if names := toolNames(ReadOnlyTools(dir, cs)); names != "read_file,codesearch" {
		t.Errorf("ReadOnlyTools(params) = %s, want read_file,codesearch", names)
	}
}

func toolNames(tools []Tool) string {
	var ns []string
	for _, t := range tools {
		ns = append(ns, t.Definition().Name)
	}
	return strings.Join(ns, ",")
}
