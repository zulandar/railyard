package config

import (
	"strings"
	"testing"
)

// TestParse_MCPServers verifies a global mcp_servers block parses into
// Config.MCPServers with command, args, and env preserved.
func TestParse_MCPServers(t *testing.T) {
	yaml := `
owner: bob
repo: git@github.com:org/app.git
tracks:
  - name: api
    language: go
mcp_servers:
  internal_repo:
    command: /usr/local/bin/internal-mcp
    args: ["--repo", "platform"]
    env:
      INTERNAL_TOKEN: abc123
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srv, ok := cfg.MCPServers["internal_repo"]
	if !ok {
		t.Fatalf("MCPServers missing %q entry: %#v", "internal_repo", cfg.MCPServers)
	}
	if srv.Command != "/usr/local/bin/internal-mcp" {
		t.Errorf("Command = %q, want %q", srv.Command, "/usr/local/bin/internal-mcp")
	}
	if len(srv.Args) != 2 || srv.Args[0] != "--repo" || srv.Args[1] != "platform" {
		t.Errorf("Args = %#v, want [--repo platform]", srv.Args)
	}
	if srv.Env["INTERNAL_TOKEN"] != "abc123" {
		t.Errorf("Env[INTERNAL_TOKEN] = %q, want %q", srv.Env["INTERNAL_TOKEN"], "abc123")
	}
}

// TestParse_MCPServers_Empty verifies omitting the block leaves MCPServers nil
// and the config valid.
func TestParse_MCPServers_Empty(t *testing.T) {
	cfg, err := Parse([]byte(minimalYAML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MCPServers != nil {
		t.Errorf("MCPServers = %#v, want nil", cfg.MCPServers)
	}
}

// TestParse_MCPServers_EnvVarResolution verifies ${VAR} tokens in env values
// are resolved from the environment, matching the credential fields elsewhere
// in the config (server env blocks typically carry tokens).
func TestParse_MCPServers_EnvVarResolution(t *testing.T) {
	t.Setenv("RAILYARD_TEST_MCP_TOKEN", "sekrit")
	yaml := `
owner: bob
repo: git@github.com:org/app.git
tracks:
  - name: api
    language: go
mcp_servers:
  internal_repo:
    command: /usr/local/bin/internal-mcp
    env:
      INTERNAL_TOKEN: ${RAILYARD_TEST_MCP_TOKEN}
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := cfg.MCPServers["internal_repo"].Env["INTERNAL_TOKEN"]; got != "sekrit" {
		t.Errorf("Env[INTERNAL_TOKEN] = %q, want %q", got, "sekrit")
	}
}

// TestParse_MCPServers_ReservedName verifies the railyard_cocoindex server
// name is rejected — it is owned by Railyard's built-in codesearch wiring.
func TestParse_MCPServers_ReservedName(t *testing.T) {
	yaml := `
owner: bob
repo: git@github.com:org/app.git
tracks:
  - name: api
    language: go
mcp_servers:
  railyard_cocoindex:
    command: /bin/echo
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected validation error for reserved server name, got nil")
	}
	if !strings.Contains(err.Error(), "railyard_cocoindex") {
		t.Errorf("error %q does not mention reserved name", err.Error())
	}
}

// TestParse_MCPServers_MissingCommand verifies an entry without a command is
// rejected at validation.
func TestParse_MCPServers_MissingCommand(t *testing.T) {
	yaml := `
owner: bob
repo: git@github.com:org/app.git
tracks:
  - name: api
    language: go
mcp_servers:
  internal_repo:
    args: ["--repo", "platform"]
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected validation error for missing command, got nil")
	}
	if !strings.Contains(err.Error(), "internal_repo") || !strings.Contains(err.Error(), "command") {
		t.Errorf("error %q does not identify the offending entry/field", err.Error())
	}
}
