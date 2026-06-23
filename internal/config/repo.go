package config

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseGitHubRepo extracts the owner and repository name from a repo identifier
// in any of the forms Railyard accepts in the `repo` config field:
//
//   - owner/repo slug    — "zulandar/blog"
//   - HTTPS URL          — "https://github.com/zulandar/blog.git"
//   - SSH scp-like URL   — "git@github.com:zulandar/blog.git"
//   - ssh:// URL         — "ssh://git@github.com/zulandar/blog.git"
//
// A trailing ".git" suffix and surrounding slashes/whitespace are stripped. The
// returned owner and name are suitable for direct use as the {owner}/{repo} path
// segments of the GitHub API. The host is not validated, so GitHub Enterprise
// hosts work too.
func ParseGitHubRepo(repo string) (owner, name string, err error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", "", fmt.Errorf("repo is empty")
	}

	// Reduce every accepted form to a bare "owner/name" path.
	var path string
	switch {
	case strings.Contains(repo, "://"):
		// URL form (https://, ssh://, …). url.Parse handles any "user@" prefix.
		u, perr := url.Parse(repo)
		if perr != nil {
			return "", "", fmt.Errorf("invalid repo URL %q: %w", repo, perr)
		}
		path = u.Path
	case strings.Contains(repo, "@") && strings.Contains(repo, ":"):
		// SSH scp-like form: git@host:owner/repo.git — path is after the first ':'.
		path = repo[strings.Index(repo, ":")+1:]
	default:
		// owner/repo slug.
		path = repo
	}

	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repo %q: expected owner/repo, a GitHub URL, or git@host:owner/repo", repo)
	}
	return parts[0], parts[1], nil
}
