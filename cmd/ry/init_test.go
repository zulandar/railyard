package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initGitRepo creates a temporary git repository with user.name "TestUser",
// email "test@test.com", remote origin "git@github.com:org/myrepo.git",
// and one initial commit. Returns the path to the repo root.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	for _, args := range [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.name", "TestUser"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "remote", "add", "origin", "git@github.com:org/myrepo.git"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}

	// Create an initial commit so the repo is non-empty.
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}

	return dir
}

func TestDetectGitRoot(t *testing.T) {
	dir := initGitRepo(t)

	// Create a subdirectory and call detectGitRoot from there.
	sub := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}

	root, err := detectGitRoot(sub)
	if err != nil {
		t.Fatalf("detectGitRoot(%q): %v", sub, err)
	}
	if root != dir {
		t.Errorf("detectGitRoot = %q, want %q", root, dir)
	}
}

func TestDetectGitRoot_NotARepo(t *testing.T) {
	// A plain temp directory is not a git repo.
	dir := t.TempDir()
	_, err := detectGitRoot(dir)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
}

func TestDetectGitRemote(t *testing.T) {
	dir := initGitRepo(t)

	remote, err := detectGitRemote(dir)
	if err != nil {
		t.Fatalf("detectGitRemote: %v", err)
	}
	if remote != "git@github.com:org/myrepo.git" {
		t.Errorf("detectGitRemote = %q, want %q", remote, "git@github.com:org/myrepo.git")
	}
}

func TestDetectGitRemote_NoRemote(t *testing.T) {
	dir := t.TempDir()

	// Create a repo with no remote.
	for _, args := range [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "user.email", "test@test.com"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s\n%s", args, err, out)
		}
	}

	remote, err := detectGitRemote(dir)
	if err != nil {
		t.Fatalf("detectGitRemote: %v", err)
	}
	if remote != "" {
		t.Errorf("detectGitRemote = %q, want empty string", remote)
	}
}

func TestDetectOwner(t *testing.T) {
	dir := initGitRepo(t)

	owner := detectOwner(dir)
	if owner != "testuser" {
		t.Errorf("detectOwner = %q, want %q", owner, "testuser")
	}
}

func TestSanitizeOwner(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Alice Smith", "alice-smith"},
		{"bob_jones", "bob-jones"},
		{"charlie123", "charlie123"},
		{"  spaces  ", "spaces"},
		{"UPPERCASE", "uppercase"},
		{"special!@#chars", "specialchars"},
	}

	for _, tt := range tests {
		got := sanitizeOwner(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeOwner(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
