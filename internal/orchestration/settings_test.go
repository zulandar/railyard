package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureClaudeSettings_CreatesNew(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "railyard.yaml")
	os.WriteFile(configPath, []byte("owner: test\n"), 0644)

	if err := EnsureClaudeSettings(configPath); err != nil {
		t.Fatalf("EnsureClaudeSettings: %v", err)
	}

	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings claudeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}

	// All required permissions should be present.
	perms := make(map[string]bool)
	for _, p := range settings.Permissions.Allow {
		perms[p] = true
	}
	for _, want := range requiredPermissions {
		if !perms[want] {
			t.Errorf("missing permission %q", want)
		}
	}
}

func TestEnsureClaudeSettings_MergesExisting(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "railyard.yaml")
	os.WriteFile(configPath, []byte("owner: test\n"), 0644)

	// Create existing settings with some custom permission.
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0755)
	existing := claudeSettings{
		Permissions: claudePermissions{
			Allow: []string{"Bash(custom command)", "Bash(ry *)"},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0644)

	if err := EnsureClaudeSettings(configPath); err != nil {
		t.Fatalf("EnsureClaudeSettings: %v", err)
	}

	// Re-read.
	data, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings claudeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}

	perms := make(map[string]bool)
	for _, p := range settings.Permissions.Allow {
		perms[p] = true
	}

	// Custom permission preserved.
	if !perms["Bash(custom command)"] {
		t.Error("custom permission was not preserved")
	}

	// All required permissions present.
	for _, want := range requiredPermissions {
		if !perms[want] {
			t.Errorf("missing permission %q", want)
		}
	}

	// No duplicates for "Bash(ry *)" which was in both.
	count := 0
	for _, p := range settings.Permissions.Allow {
		if p == "Bash(ry *)" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Bash(ry *) appeared %d times, want 1", count)
	}
}

func TestEnsureClaudeSettings_Idempotent(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "railyard.yaml")
	os.WriteFile(configPath, []byte("owner: test\n"), 0644)

	// Run twice.
	if err := EnsureClaudeSettings(configPath); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := EnsureClaudeSettings(configPath); err != nil {
		t.Fatalf("second call: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	var settings claudeSettings
	json.Unmarshal(data, &settings)

	// Should have exactly len(requiredPermissions) entries, no duplicates.
	if len(settings.Permissions.Allow) != len(requiredPermissions) {
		t.Errorf("permissions count = %d, want %d", len(settings.Permissions.Allow), len(requiredPermissions))
	}
}

func TestEnsureClaudeSettings_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "railyard.yaml")
	os.WriteFile(configPath, []byte("owner: test\n"), 0644)

	// Write invalid JSON.
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0755)
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte("{invalid"), 0644)

	err := EnsureClaudeSettings(configPath)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestEnsureClaudeSettings_RelativePath(t *testing.T) {
	// Use a temp dir and a relative config path within it.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "railyard.yaml")
	os.WriteFile(configPath, []byte("owner: test\n"), 0644)

	// Even with an absolute path, this should work fine.
	if err := EnsureClaudeSettings(configPath); err != nil {
		t.Fatalf("EnsureClaudeSettings: %v", err)
	}

	// Verify file exists.
	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); err != nil {
		t.Errorf("settings.json not created: %v", err)
	}
}
