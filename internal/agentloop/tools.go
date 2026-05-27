package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// safeJoin resolves p against root and rejects any path that escapes the
// worktree (via ".." or an absolute path outside the tree). The returned path
// is cleaned and absolute-relative to root.
func safeJoin(root, p string) (string, error) {
	root = filepath.Clean(root)
	var full string
	if filepath.IsAbs(p) {
		full = filepath.Clean(p)
	} else {
		full = filepath.Join(root, p)
	}
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", fmt.Errorf("path %q is outside the worktree", p)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the worktree (resolved outside %q)", p, root)
	}
	return full, nil
}

// --- bash ---

// BashTool runs a shell command with its working directory set to the worktree.
// It is the workhorse tool: research (grep/cat/ls), ry read+write commands,
// builds, tests, git.
type BashTool struct {
	workdir string
}

// NewBashTool builds a bash tool scoped to workdir.
func NewBashTool(workdir string) *BashTool { return &BashTool{workdir: workdir} }

func (t *BashTool) Definition() ToolDef {
	return ToolDef{
		Name:        "bash",
		Description: "Run a shell command in the working directory and return its combined stdout+stderr.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "The shell command to run."}
			},
			"required": ["command"]
		}`),
	}
}

func (t *BashTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid bash arguments: %w", err)
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", fmt.Errorf("bash: command is required")
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", a.Command)
	cmd.Dir = t.workdir
	out, err := cmd.CombinedOutput()
	result := string(out)
	// A non-zero exit is information for the model, not a tool failure: keep the
	// captured output and append the exit status rather than discarding output.
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return fmt.Sprintf("%s\n[command failed: %s]", result, err.Error()), nil
		}
		// Failure to start the process (e.g. missing shell) is a real error.
		return result, fmt.Errorf("bash: %w", err)
	}
	return result, nil
}

// --- read_file ---

// ReadFileTool reads a file within the worktree.
type ReadFileTool struct {
	root string
}

// NewReadFileTool builds a read_file tool scoped to root.
func NewReadFileTool(root string) *ReadFileTool { return &ReadFileTool{root: root} }

func (t *ReadFileTool) Definition() ToolDef {
	return ToolDef{
		Name:        "read_file",
		Description: "Read a file from the worktree and return its contents.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Path relative to the worktree."}
			},
			"required": ["path"]
		}`),
	}
}

func (t *ReadFileTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid read_file arguments: %w", err)
	}
	full, err := safeJoin(t.root, a.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read_file %q: %w", a.Path, err)
	}
	return string(data), nil
}

// --- write_file ---

// WriteFileTool writes (creating or overwriting) a file within the worktree,
// creating parent directories as needed.
type WriteFileTool struct {
	root string
}

// NewWriteFileTool builds a write_file tool scoped to root.
func NewWriteFileTool(root string) *WriteFileTool { return &WriteFileTool{root: root} }

func (t *WriteFileTool) Definition() ToolDef {
	return ToolDef{
		Name:        "write_file",
		Description: "Write (create or overwrite) a file in the worktree, creating parent directories as needed.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Path relative to the worktree."},
				"content": {"type": "string", "description": "Full file contents to write."}
			},
			"required": ["path", "content"]
		}`),
	}
}

func (t *WriteFileTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid write_file arguments: %w", err)
	}
	full, err := safeJoin(t.root, a.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("write_file %q: %w", a.Path, err)
	}
	if err := os.WriteFile(full, []byte(a.Content), 0o644); err != nil {
		return "", fmt.Errorf("write_file %q: %w", a.Path, err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path), nil
}

// --- edit_file ---

// EditFileTool performs an exact string replacement in a worktree file
// (Claude-Code Edit semantics): old_string must appear exactly once.
type EditFileTool struct {
	root string
}

// NewEditFileTool builds an edit_file tool scoped to root.
func NewEditFileTool(root string) *EditFileTool { return &EditFileTool{root: root} }

func (t *EditFileTool) Definition() ToolDef {
	return ToolDef{
		Name:        "edit_file",
		Description: "Replace an exact, unique string in a worktree file with a new string.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "Path relative to the worktree."},
				"old_string": {"type": "string", "description": "Exact text to replace; must occur exactly once."},
				"new_string": {"type": "string", "description": "Replacement text."}
			},
			"required": ["path", "old_string", "new_string"]
		}`),
	}
}

func (t *EditFileTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid edit_file arguments: %w", err)
	}
	if a.OldString == "" {
		return "", fmt.Errorf("edit_file %q: old_string must not be empty", a.Path)
	}
	full, err := safeJoin(t.root, a.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("edit_file %q: %w", a.Path, err)
	}
	content := string(data)
	switch n := strings.Count(content, a.OldString); n {
	case 0:
		return "", fmt.Errorf("edit_file %q: old_string not found", a.Path)
	case 1:
		// proceed
	default:
		return "", fmt.Errorf("edit_file %q: old_string is not unique (%d matches); include more surrounding context", a.Path, n)
	}
	updated := strings.Replace(content, a.OldString, a.NewString, 1)
	if err := os.WriteFile(full, []byte(updated), 0o644); err != nil {
		return "", fmt.Errorf("edit_file %q: %w", a.Path, err)
	}
	return fmt.Sprintf("edited %s", a.Path), nil
}

// --- toolset profiles ---

// DispatchTools is the dispatch/telegraph profile: bash + read_file.
func DispatchTools(workdir string) []Tool {
	return []Tool{NewBashTool(workdir), NewReadFileTool(workdir)}
}

// EngineTools is the engine profile: bash + read_file + write_file + edit_file.
func EngineTools(workdir string) []Tool {
	return []Tool{
		NewBashTool(workdir),
		NewReadFileTool(workdir),
		NewWriteFileTool(workdir),
		NewEditFileTool(workdir),
	}
}
