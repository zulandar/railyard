package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"migrate", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("migrate --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, ".railyard/") {
		t.Errorf("expected help to mention '.railyard/', got: %s", out)
	}
}

func TestMigrateCmd_Flags(t *testing.T) {
	cmd := newMigrateCmd()
	if cmd.Use != "migrate" {
		t.Errorf("Use = %q, want %q", cmd.Use, "migrate")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Error("expected --config flag")
	}
}

func TestRootCmd_HasMigrateSubcommand(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("root --help failed: %v", err)
	}

	if !strings.Contains(buf.String(), "migrate") {
		t.Error("root help should list 'migrate' subcommand")
	}
}

func TestRemoveGitIgnoreEntry(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, ".gitignore")

	content := "engines/\n.railyard/\n*.o\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if err := removeGitIgnoreEntry(path, "engines/"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	result := string(data)
	if strings.Contains(result, "engines/") {
		t.Errorf("engines/ should have been removed, got: %s", result)
	}
	if !strings.Contains(result, ".railyard/") {
		t.Errorf(".railyard/ should still be present, got: %s", result)
	}
	if !strings.Contains(result, "*.o") {
		t.Errorf("*.o should still be present, got: %s", result)
	}
}

func TestUpdateGitIgnoreForMigration_AlreadyUpToDate(t *testing.T) {
	tmp := t.TempDir()
	gitignorePath := filepath.Join(tmp, ".gitignore")
	os.WriteFile(gitignorePath, []byte(".railyard/\n"), 0644)

	buf := new(bytes.Buffer)
	if err := updateGitIgnoreForMigration(tmp, buf); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "already up to date") {
		t.Errorf("expected 'already up to date', got: %s", buf.String())
	}
}

func TestUpdateGitIgnoreForMigration_AddsRailyard(t *testing.T) {
	tmp := t.TempDir()
	gitignorePath := filepath.Join(tmp, ".gitignore")
	os.WriteFile(gitignorePath, []byte("*.o\n"), 0644)

	buf := new(bytes.Buffer)
	if err := updateGitIgnoreForMigration(tmp, buf); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(gitignorePath)
	if !strings.Contains(string(data), ".railyard/") {
		t.Errorf("expected .railyard/ to be added, got: %s", string(data))
	}
	if !strings.Contains(buf.String(), "Added .railyard/") {
		t.Errorf("expected 'Added .railyard/', got: %s", buf.String())
	}
}

func TestUpdateGitIgnoreForMigration_RemovesOldEngines(t *testing.T) {
	tmp := t.TempDir()
	gitignorePath := filepath.Join(tmp, ".gitignore")
	os.WriteFile(gitignorePath, []byte("engines/\n*.o\n"), 0644)

	buf := new(bytes.Buffer)
	if err := updateGitIgnoreForMigration(tmp, buf); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(gitignorePath)
	if strings.Contains(string(data), "engines/") {
		t.Errorf("engines/ should have been removed, got: %s", string(data))
	}
	if !strings.Contains(string(data), ".railyard/") {
		t.Errorf(".railyard/ should have been added, got: %s", string(data))
	}
}

func TestCheckMigrationNeeded_NoEnginesDir(t *testing.T) {
	// Should not warn when engines/ doesn't exist.
	tmp := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(origDir)

	cmd := newStartCmd()
	buf := new(bytes.Buffer)
	cmd.SetErr(buf)

	checkMigrationNeeded(cmd)
	if buf.Len() > 0 {
		t.Errorf("expected no output, got: %s", buf.String())
	}
}

func TestCheckMigrationNeeded_AlreadyMigrated(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "engines"), 0755)
	os.MkdirAll(filepath.Join(tmp, ".railyard"), 0755)
	origDir, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(origDir)

	cmd := newStartCmd()
	buf := new(bytes.Buffer)
	cmd.SetErr(buf)

	checkMigrationNeeded(cmd)
	if buf.Len() > 0 {
		t.Errorf("expected no output, got: %s", buf.String())
	}
}

func TestCheckMigrationNeeded_NeedsMigration(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "engines"), 0755)
	origDir, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(origDir)

	cmd := newStartCmd()
	buf := new(bytes.Buffer)
	cmd.SetErr(buf)

	checkMigrationNeeded(cmd)
	if !strings.Contains(buf.String(), "ry migrate") {
		t.Errorf("expected migration warning, got: %s", buf.String())
	}
}
