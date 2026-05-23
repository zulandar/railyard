package cli

import (
	"bytes"
	"fmt"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestVersionCmd(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	out := buf.String()
	// With the build-info fallback, "dev" and "none" may be replaced by
	// values from runtime/debug.ReadBuildInfo when the test binary is built
	// inside the repo. Assert structure rather than literal defaults.
	if !strings.HasPrefix(out, "ry ") || !strings.Contains(out, "(commit: ") || !strings.Contains(out, ", built: ") {
		t.Errorf("unexpected version output format: %s", out)
	}
}

func TestVersionCmdWithCustomValues(t *testing.T) {
	origVersion, origCommit, origDate := Version, Commit, Date
	Version, Commit, Date = "1.0.0", "abc123", "2026-01-01"
	defer func() { Version, Commit, Date = origVersion, origCommit, origDate }()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "ry 1.0.0") {
		t.Errorf("expected output to contain 'ry 1.0.0', got: %s", out)
	}
	if !strings.Contains(out, "commit: abc123") {
		t.Errorf("expected output to contain 'commit: abc123', got: %s", out)
	}
	if !strings.Contains(out, "built: 2026-01-01") {
		t.Errorf("expected output to contain 'built: 2026-01-01', got: %s", out)
	}
}

func TestRootCmdHelp(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("help command failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Railyard") {
		t.Errorf("expected help output to contain 'Railyard', got: %s", out)
	}
	if !strings.Contains(out, "version") {
		t.Errorf("expected help output to list 'version' subcommand, got: %s", out)
	}
}

func TestRootCmdNoArgs(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})

	// Root command with no args should print help (not error)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("root command with no args failed: %v", err)
	}
}

func TestExecuteSuccess(t *testing.T) {
	code := execute(newRootCmd())
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

func TestExecuteError(t *testing.T) {
	cmd := &cobra.Command{
		Use:           "failing",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("intentional error")
		},
	}
	code := execute(cmd)
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

func TestNewVersionCmdOutput(t *testing.T) {
	cmd := newVersionCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	cmd.Run(cmd, nil)

	// The exact substitutions depend on whether the test binary was built
	// with VCS info available; what matters is the framing.
	out := buf.String()
	if !strings.HasPrefix(out, "ry ") || !strings.Contains(out, "(commit: ") || !strings.Contains(out, ", built: ") {
		t.Errorf("unexpected version output format: %q", out)
	}
}

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name                 string
		ldVer, ldCom, ldDate string
		info                 *debug.BuildInfo
		infoOK               bool
		wantVer              string
		wantCom              string
		wantDate             string
	}{
		{
			name:    "ldflags win over build info",
			ldVer:   "v1.2.3",
			ldCom:   "abc12345",
			ldDate:  "2026-01-01T00:00:00Z",
			info:    &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "deadbeefdeadbeef"}}},
			infoOK:  true,
			wantVer: "v1.2.3", wantCom: "abc12345", wantDate: "2026-01-01T00:00:00Z",
		},
		{
			name:  "no build info: defaults pass through",
			ldVer: "dev", ldCom: "none", ldDate: "unknown",
			info: nil, infoOK: false,
			wantVer: "dev", wantCom: "none", wantDate: "unknown",
		},
		{
			name:  "go install: module version + vcs settings fill in",
			ldVer: "dev", ldCom: "none", ldDate: "unknown",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v0.9.10"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "aed919d0aed919d0"},
					{Key: "vcs.time", Value: "2026-05-23T17:37:40Z"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			infoOK:  true,
			wantVer: "v0.9.10", wantCom: "aed919d0", wantDate: "2026-05-23T17:37:40Z",
		},
		{
			name:  "dirty worktree appends -dirty",
			ldVer: "dev", ldCom: "none", ldDate: "unknown",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abcdef1234567890"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			infoOK:  true,
			wantVer: "(devel)-dirty", wantCom: "abcdef12", wantDate: "unknown",
		},
		{
			name:  "pseudo-version already carries +dirty: no double-tag",
			ldVer: "dev", ldCom: "none", ldDate: "unknown",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v0.9.10-0.20260523173839-aed919dcb6da+dirty"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "aed919dcb6da0000"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			infoOK:  true,
			wantVer: "v0.9.10-0.20260523173839-aed919dcb6da+dirty",
			wantCom: "aed919dc", wantDate: "unknown",
		},
		{
			name:  "short revision: no slice panic",
			ldVer: "dev", ldCom: "none", ldDate: "unknown",
			info: &debug.BuildInfo{
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}},
			},
			infoOK:  true,
			wantVer: "dev", wantCom: "abc", wantDate: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, c, d := resolveVersion(tt.ldVer, tt.ldCom, tt.ldDate, tt.info, tt.infoOK)
			if v != tt.wantVer || c != tt.wantCom || d != tt.wantDate {
				t.Errorf("got (%q, %q, %q), want (%q, %q, %q)", v, c, d, tt.wantVer, tt.wantCom, tt.wantDate)
			}
		})
	}
}
