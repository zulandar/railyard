package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// claudeSettings is the structure of .claude/settings.json.
type claudeSettings struct {
	Permissions claudePermissions `json:"permissions"`
}

type claudePermissions struct {
	Allow []string `json:"allow"`
}

// requiredPermissions are the permissions engines/dispatch/yardmaster need
// to operate autonomously in tmux panes.
var requiredPermissions = []string{
	"Bash(ry *)",
	"Bash(go test *)",
	"Bash(go build *)",
	"Bash(go vet *)",
	"Bash(git *)",
	"Read",
	"Edit",
	"Write",
	"Glob",
	"Grep",
}

// EnsureClaudeSettings ensures .claude/settings.json exists in the repo root
// with the permissions needed for autonomous engine operation.
// If the file already exists, missing permissions are merged in.
// The repo root is derived from the directory containing configPath.
func EnsureClaudeSettings(configPath string) error {
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("orchestration: resolve config path: %w", err)
	}
	repoRoot := filepath.Dir(absPath)
	claudeDir := filepath.Join(repoRoot, ".claude")
	settingsPath := filepath.Join(claudeDir, "settings.json")

	// Ensure .claude/ directory exists.
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return fmt.Errorf("orchestration: create .claude dir: %w", err)
	}

	// Read existing settings or start fresh.
	settings := claudeSettings{}
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		// File exists — parse it and merge.
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("orchestration: parse %s: %w", settingsPath, err)
		}
	}

	// Merge in required permissions.
	existing := make(map[string]bool, len(settings.Permissions.Allow))
	for _, p := range settings.Permissions.Allow {
		existing[p] = true
	}
	for _, p := range requiredPermissions {
		if !existing[p] {
			settings.Permissions.Allow = append(settings.Permissions.Allow, p)
		}
	}

	// Write back.
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("orchestration: marshal settings: %w", err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(settingsPath, out, 0644); err != nil {
		return fmt.Errorf("orchestration: write %s: %w", settingsPath, err)
	}

	return nil
}
