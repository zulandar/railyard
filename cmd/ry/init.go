package main

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// detectGitRoot runs `git rev-parse --show-toplevel` from dir and returns
// the trimmed absolute path to the repository root, or an error if dir is
// not inside a git repository.
func detectGitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// detectGitRemote runs `git remote get-url origin` from dir and returns
// the remote URL. If no remote named "origin" is configured, it returns
// an empty string with no error.
func detectGitRemote(dir string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		// No remote configured is not an error for our purposes.
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// detectOwner returns a sanitized owner name for the repository.
// It tries git config user.name first, then falls back to $USER,
// then to "railyard" as a last resort.
func detectOwner(dir string) string {
	cmd := exec.Command("git", "config", "user.name")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return sanitizeOwner(name)
		}
	}

	if user := os.Getenv("USER"); user != "" {
		return sanitizeOwner(user)
	}

	return sanitizeOwner("railyard")
}

// sanitizeOwner normalises a human name into a lowercase, hyphen-separated
// identifier suitable for use in config files and branch names.
// It lowercases the input, replaces spaces and underscores with hyphens,
// strips any remaining non-alphanumeric/non-hyphen characters, and
// collapses consecutive hyphens. Leading/trailing hyphens are trimmed.
func sanitizeOwner(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")

	// Strip characters that are not alphanumeric or hyphens.
	re := regexp.MustCompile(`[^a-z0-9-]`)
	s = re.ReplaceAllString(s, "")

	// Collapse consecutive hyphens.
	multi := regexp.MustCompile(`-{2,}`)
	s = multi.ReplaceAllString(s, "-")

	// Trim leading/trailing hyphens.
	s = strings.Trim(s, "-")

	return s
}
