package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// CodeSearchToolName is the model-facing name of the semantic codesearch tool.
// It is a single shared constant so observability (railyard-cpn) can confirm
// codesearch usage per role by matching this name in tool-call logs, and so the
// native tool name stays in sync with the cocoindex MCP server it mirrors.
const CodeSearchToolName = "codesearch"

// CodeSearchParams is everything the codesearch tool needs to run a query,
// expressed as plain primitives so the agentloop package stays transport- and
// config-unaware (it imports no other Railyard package). Callers translate
// their config into these (e.g. engine.EngineCodeSearchParams,
// dispatch.DispatchCodeSearchParams), choosing the table targeting via Env —
// the same COCOINDEX_* env the .mcp.json path already uses.
type CodeSearchParams struct {
	// PythonPath is the interpreter that runs ScriptPath (the cocoindex venv).
	PythonPath string
	// ScriptPath is the cocoindex mcp_server.py, invoked in one-shot `query` mode.
	ScriptPath string
	// Env holds the COCOINDEX_* variables (database URL, main/overlay tables,
	// engine id, track) forwarded to the query subprocess. It is layered on top
	// of the parent process environment.
	Env map[string]string
}

// CodeSearchTool gives a native-loop agent semantic code search by shelling out
// to the cocoindex query CLI — mirroring the "shell out to the venv python"
// pattern already used for overlay build/cleanup, so the loop needs no MCP
// client. It returns the same ranked snippets the railyard_cocoindex MCP server
// returns to claude-based backends.
type CodeSearchTool struct {
	params CodeSearchParams
}

// NewCodeSearchTool builds a codesearch tool from the given params.
func NewCodeSearchTool(p CodeSearchParams) *CodeSearchTool { return &CodeSearchTool{params: p} }

func (t *CodeSearchTool) Definition() ToolDef {
	return ToolDef{
		Name: CodeSearchToolName,
		Description: "Semantic code search over the indexed codebase. Returns code " +
			"snippets ranked by relevance to a natural-language query. Prefer this " +
			"over ad-hoc bash grep/find when exploring how the codebase works.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "Natural-language description of the code you're looking for."},
				"top_k": {"type": "integer", "description": "Maximum number of results to return (default 10)."},
				"min_score": {"type": "number", "description": "Minimum cosine similarity 0.0-1.0 (default 0.0)."}
			},
			"required": ["query"]
		}`),
	}
}

// codeSearchResult is one ranked snippet from the query CLI's JSON output.
type codeSearchResult struct {
	Filename string  `json:"filename"`
	Code     string  `json:"code"`
	Location string  `json:"location"`
	Score    float64 `json:"score"`
}

func (t *CodeSearchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Query    string   `json:"query"`
		TopK     *int     `json:"top_k"`
		MinScore *float64 `json:"min_score"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid codesearch arguments: %w", err)
	}
	if strings.TrimSpace(a.Query) == "" {
		return "", fmt.Errorf("codesearch: query is required")
	}

	cmdArgs := []string{t.params.ScriptPath, "query", "--query", a.Query}
	if a.TopK != nil {
		cmdArgs = append(cmdArgs, "--top-k", strconv.Itoa(*a.TopK))
	}
	if a.MinScore != nil {
		cmdArgs = append(cmdArgs, "--min-score", strconv.FormatFloat(*a.MinScore, 'f', -1, 64))
	}

	cmd := exec.CommandContext(ctx, t.params.PythonPath, cmdArgs...)
	cmd.Env = os.Environ()
	for k, v := range t.params.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Surface the subprocess stderr as the tool error (fed back to the model
		// by the loop, which continues rather than aborting).
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("codesearch: %s", msg)
		}
		return "", fmt.Errorf("codesearch: %w", err)
	}

	var results []codeSearchResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		return "", fmt.Errorf("codesearch: parse results: %w", err)
	}
	return formatCodeSearchResults(a.Query, results), nil
}

// formatCodeSearchResults renders ranked snippets into a compact, model-readable
// block. An empty result set returns an explicit "no results" message so the
// model gets a clear signal rather than a blank tool result.
func formatCodeSearchResults(query string, results []codeSearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results found for query: %q", query)
	}
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[%d] %s %s (score %.2f)\n%s\n", i+1, r.Filename, r.Location, r.Score, r.Code)
	}
	return strings.TrimRight(b.String(), "\n")
}
