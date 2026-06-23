package config

import "testing"

func TestParseGitHubRepo(t *testing.T) {
	tests := []struct {
		name      string
		repo      string
		wantOwner string
		wantName  string
		wantErr   bool
	}{
		{name: "owner/repo slug", repo: "zulandar/blog", wantOwner: "zulandar", wantName: "blog"},
		{name: "https URL with .git", repo: "https://github.com/zulandar/blog.git", wantOwner: "zulandar", wantName: "blog"},
		{name: "https URL without .git", repo: "https://github.com/zulandar/blog", wantOwner: "zulandar", wantName: "blog"},
		{name: "https URL trailing slash", repo: "https://github.com/zulandar/blog/", wantOwner: "zulandar", wantName: "blog"},
		{name: "ssh scp form with .git", repo: "git@github.com:zulandar/blog.git", wantOwner: "zulandar", wantName: "blog"},
		{name: "ssh scp form without .git", repo: "git@github.com:zulandar/blog", wantOwner: "zulandar", wantName: "blog"},
		{name: "ssh url form", repo: "ssh://git@github.com/zulandar/blog.git", wantOwner: "zulandar", wantName: "blog"},
		{name: "enterprise host scp form", repo: "git@git.example.com:team/project.git", wantOwner: "team", wantName: "project"},
		{name: "surrounding whitespace", repo: "  zulandar/blog  ", wantOwner: "zulandar", wantName: "blog"},

		{name: "empty", repo: "", wantErr: true},
		{name: "bare name no owner", repo: "blog", wantErr: true},
		{name: "scp form missing repo", repo: "git@github.com:zulandar", wantErr: true},
		{name: "url missing repo", repo: "https://github.com/zulandar", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, name, err := ParseGitHubRepo(tt.repo)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseGitHubRepo(%q) = (%q, %q, nil); want error", tt.repo, owner, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseGitHubRepo(%q) returned unexpected error: %v", tt.repo, err)
			}
			if owner != tt.wantOwner || name != tt.wantName {
				t.Fatalf("ParseGitHubRepo(%q) = (%q, %q); want (%q, %q)", tt.repo, owner, name, tt.wantOwner, tt.wantName)
			}
		})
	}
}
