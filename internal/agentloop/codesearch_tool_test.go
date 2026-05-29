package agentloop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakePython writes an executable stub that stands in for the venv python
// running `mcp_server.py query ...`. It records its argv and a forwarded env var
// to a side-channel file (so stdout stays pure JSON for the parser) and prints
// the given stdout. A non-empty failMsg makes it exit 1 writing failMsg to
// stderr instead.
func writeFakePython(t *testing.T, sideFile, stdout, failMsg string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fakepython")
	var script string
	if failMsg != "" {
		script = "#!/bin/sh\n" +
			"{ echo \"ARGS: $*\"; echo \"MAIN_TABLE: $COCOINDEX_MAIN_TABLE\"; } > \"$SIDECHANNEL\"\n" +
			"echo '" + failMsg + "' >&2\n" +
			"exit 1\n"
	} else {
		script = "#!/bin/sh\n" +
			"{ echo \"ARGS: $*\"; echo \"MAIN_TABLE: $COCOINDEX_MAIN_TABLE\"; } > \"$SIDECHANNEL\"\n" +
			"cat <<'JSON'\n" + stdout + "\nJSON\n"
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}
	return path
}

func TestCodeSearchToolDefinition(t *testing.T) {
	tool := NewCodeSearchTool(CodeSearchParams{})
	def := tool.Definition()
	if def.Name != CodeSearchToolName {
		t.Errorf("Definition().Name = %q, want %q", def.Name, CodeSearchToolName)
	}
	if !strings.Contains(string(def.Parameters), "query") {
		t.Errorf("Definition().Parameters missing a query field: %s", def.Parameters)
	}
}

func TestCodeSearchToolExecute(t *testing.T) {
	side := filepath.Join(t.TempDir(), "side.txt")
	results := `[{"filename":"auth.go","code":"func Login() {}","location":"[10, 20)","score":0.87},` +
		`{"filename":"user.go","code":"type User struct{}","location":"[1, 4)","score":0.71}]`
	py := writeFakePython(t, side, results, "")

	tool := NewCodeSearchTool(CodeSearchParams{
		PythonPath: py,
		ScriptPath: "/fake/mcp_server.py",
		Env: map[string]string{
			"SIDECHANNEL":            side,
			"COCOINDEX_MAIN_TABLE":   "main_backend_embeddings",
			"COCOINDEX_DATABASE_URL": "postgresql://x",
		},
	})

	out, err := tool.Execute(context.Background(), []byte(`{"query":"auth handler","top_k":3,"min_score":0.25}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	// The model-facing text must surface both ranked snippets with their files,
	// code, and scores.
	for _, want := range []string{"auth.go", "func Login() {}", "0.87", "user.go", "type User struct{}", "0.71"} {
		if !strings.Contains(out, want) {
			t.Errorf("Execute output missing %q\n--- output ---\n%s", want, out)
		}
	}

	// The subprocess must have been invoked as the one-shot query CLI with the
	// query + flags forwarded, and the COCOINDEX_* env passed through.
	rec, err := os.ReadFile(side)
	if err != nil {
		t.Fatalf("read side channel: %v", err)
	}
	recorded := string(rec)
	for _, want := range []string{"/fake/mcp_server.py", "query", "--query", "auth handler", "--top-k 3", "--min-score 0.25", "MAIN_TABLE: main_backend_embeddings"} {
		if !strings.Contains(recorded, want) {
			t.Errorf("subprocess invocation missing %q\n--- recorded ---\n%s", want, recorded)
		}
	}
}

func TestCodeSearchToolExecuteEmpty(t *testing.T) {
	side := filepath.Join(t.TempDir(), "side.txt")
	py := writeFakePython(t, side, "[]", "")
	tool := NewCodeSearchTool(CodeSearchParams{PythonPath: py, ScriptPath: "/fake/s.py", Env: map[string]string{"SIDECHANNEL": side}})

	out, err := tool.Execute(context.Background(), []byte(`{"query":"nothing matches"}`))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("Execute returned empty output for an empty result set; want a human-readable 'no results' message")
	}
}

func TestCodeSearchToolExecuteError(t *testing.T) {
	side := filepath.Join(t.TempDir(), "side.txt")
	py := writeFakePython(t, side, "", "pgvector unavailable")
	tool := NewCodeSearchTool(CodeSearchParams{PythonPath: py, ScriptPath: "/fake/s.py", Env: map[string]string{"SIDECHANNEL": side}})

	_, err := tool.Execute(context.Background(), []byte(`{"query":"boom"}`))
	if err == nil {
		t.Fatal("Execute should return an error when the query subprocess exits non-zero")
	}
	if !strings.Contains(err.Error(), "pgvector unavailable") {
		t.Errorf("error = %q, want it to surface the subprocess stderr", err.Error())
	}
}

func TestCodeSearchToolExecuteRedactsCredentials(t *testing.T) {
	side := filepath.Join(t.TempDir(), "side.txt")
	// A psycopg2-style connection failure that embeds the full DSN, including the
	// password — exactly what the codesearch subprocess can emit when the
	// CocoIndex database is unreachable or rejects the credentials.
	failMsg := "could not connect to server: FATAL connection to postgresql://coco:s3cr3tpassword@db.internal:5432/cocoindex failed"
	py := writeFakePython(t, side, "", failMsg)
	tool := NewCodeSearchTool(CodeSearchParams{PythonPath: py, ScriptPath: "/fake/s.py", Env: map[string]string{"SIDECHANNEL": side}})

	_, err := tool.Execute(context.Background(), []byte(`{"query":"boom"}`))
	if err == nil {
		t.Fatal("Execute should return an error when the query subprocess exits non-zero")
	}
	// The error is fed back to the model as a tool result: it must not leak the
	// database password.
	if strings.Contains(err.Error(), "s3cr3tpassword") {
		t.Errorf("error leaks DB credentials to the model: %q", err.Error())
	}
	// The non-secret diagnostic must still surface so the model/operator can see
	// why the search failed.
	if !strings.Contains(err.Error(), "could not connect") {
		t.Errorf("error dropped the non-secret diagnostic: %q", err.Error())
	}
}

func TestCodeSearchToolRejectsEmptyQuery(t *testing.T) {
	tool := NewCodeSearchTool(CodeSearchParams{PythonPath: "/bin/true", ScriptPath: "/fake/s.py"})
	if _, err := tool.Execute(context.Background(), []byte(`{"query":""}`)); err == nil {
		t.Error("Execute should reject an empty query without shelling out")
	}
}
