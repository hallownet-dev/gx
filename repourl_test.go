package main

import (
	"strings"
	"testing"
)

func TestParseRepoURL(t *testing.T) {
	cases := []struct {
		in, want string
		rebuilt  bool
	}{
		// GitHub
		{"git@github.com:hallownet-dev/gx.git", "github.com/hallownet-dev/gx", false},
		{"https://github.com/hallownet-dev/gx", "github.com/hallownet-dev/gx", false},
		{"https://github.com/hallownet-dev/gx.git", "github.com/hallownet-dev/gx", false},
		{"https://github.com/hallownet-dev/gx/tree/main/cmd", "github.com/hallownet-dev/gx", true},
		{"ssh://git@github.com/hallownet-dev/gx.git", "github.com/hallownet-dev/gx", false},
		{"hallownet-dev/gx", "github.com/hallownet-dev/gx", true},
		{"github.com/hallownet-dev/gx", "github.com/hallownet-dev/gx", true},
		{"GitHub.com/hallownet-dev/gx", "github.com/hallownet-dev/gx", true},

		// GitLab with nested groups
		{"git@gitlab.com:group/sub/subsub/repo.git", "gitlab.com/group/sub/subsub/repo", false},
		{"https://gitlab.company.com/group/sub/repo/-/tree/main", "gitlab.company.com/group/sub/repo", true},
		{"ssh://git@gitlab.company.com:2222/group/repo.git", "gitlab.company.com/group/repo", false},

		// Azure DevOps cloud
		{"https://dev.azure.com/org/proj/_git/repo", "dev.azure.com/org/proj/repo", false},
		{"https://org@dev.azure.com/org/proj/_git/repo", "dev.azure.com/org/proj/repo", false},
		{"git@ssh.dev.azure.com:v3/org/proj/repo", "dev.azure.com/org/proj/repo", false},
		{"https://dev.azure.com/org/_git/repo", "dev.azure.com/org/repo/repo", false},
		{"https://org.visualstudio.com/DefaultCollection/proj/_git/repo", "dev.azure.com/org/proj/repo", false},
		{"https://dev.azure.com/org/proj/_git/repo?path=/README.md", "dev.azure.com/org/proj/repo", false},

		// Azure DevOps Server (self-hosted)
		{"https://devops.client.com/tfs/DefaultCollection/Proj/_git/Repo", "devops.client.com/DefaultCollection/Proj/Repo", false},
		{"ssh://devops.client.com:22/tfs/DefaultCollection/Proj/_git/Repo", "devops.client.com/DefaultCollection/Proj/Repo", false},
		{"https://devops.client.com/Coll/My%20Project/_git/Repo", "devops.client.com/Coll/My Project/Repo", false},

		// Bitbucket cloud
		{"https://bitbucket.org/ws/repo.git", "bitbucket.org/ws/repo", false},
		{"git@bitbucket.org:ws/repo.git", "bitbucket.org/ws/repo", false},
		{"https://user@bitbucket.org/ws/repo/src/main/", "bitbucket.org/ws/repo", true},

		// Bitbucket Server / Data Center
		{"https://user@bitbucket.client.com/scm/proj/repo.git", "bitbucket.client.com/proj/repo", false},
		{"ssh://git@bitbucket.client.com:7999/proj/repo.git", "bitbucket.client.com/proj/repo", false},
		{"https://bitbucket.client.com/bitbucket/scm/proj/repo.git", "bitbucket.client.com/proj/repo", false},
		{"https://bitbucket.client.com/scm/~jdoe/repo.git", "bitbucket.client.com/~jdoe/repo", false},
	}
	for _, c := range cases {
		u, err := ParseRepoURL(c.in, "github.com")
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.in, err)
			continue
		}
		if got := u.Key(); got != c.want {
			t.Errorf("%s:\n  got  %s\n  want %s", c.in, got, c.want)
		}
		if u.Rebuilt != c.rebuilt {
			t.Errorf("%s: Rebuilt = %v, want %v", c.in, u.Rebuilt, c.rebuilt)
		}
	}
}

func TestParseRepoURLErrors(t *testing.T) {
	for _, in := range []string{"", "repo", "https://github.com/", "git@github.com:onlyone", "https://h.com/a/../b"} {
		if u, err := ParseRepoURL(in, "github.com"); err == nil {
			t.Errorf("%q: expected error, got %s", in, u.Key())
		}
	}
}

func TestCloneURL(t *testing.T) {
	u, _ := ParseRepoURL("hallownet-dev/gx", "github.com")
	if got := u.CloneURL("ssh"); got != "git@github.com:hallownet-dev/gx.git" {
		t.Errorf("ssh: %s", got)
	}
	if got := u.CloneURL("https"); got != "https://github.com/hallownet-dev/gx.git" {
		t.Errorf("https: %s", got)
	}
	raw := "https://devops.client.com/tfs/Coll/Proj/_git/Repo"
	u, _ = ParseRepoURL(raw, "github.com")
	if got := u.CloneURL("ssh"); got != raw {
		t.Errorf("cloneable URL should be kept as is, got %s", got)
	}
}

func TestWebURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:org/repo.git":                       "https://github.com/org/repo",
		"https://user@bitbucket.org/ws/repo.git":            "https://bitbucket.org/ws/repo",
		"git@ssh.dev.azure.com:v3/org/proj/repo":            "https://dev.azure.com/org/proj/_git/repo",
		"https://org@dev.azure.com/org/proj/_git/repo":      "https://dev.azure.com/org/proj/_git/repo",
		"ssh://git@gitlab.company.com:2222/group/sub/r.git": "https://gitlab.company.com/group/sub/r",
	}
	for in, want := range cases {
		if got := WebURL(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestLocalPath(t *testing.T) {
	u, _ := ParseRepoURL("git@gitlab.com:a/b/c.git", "")
	got := u.LocalPath("/root")
	if !strings.HasSuffix(strings.ReplaceAll(got, "\\", "/"), "/root/gitlab.com/a/b/c") {
		t.Errorf("got %s", got)
	}
}
