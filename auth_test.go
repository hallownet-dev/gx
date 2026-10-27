package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// isolateGit points git at an empty global config, optionally with a
// credential helper, so tests never touch the user's credentials.
func isolateGit(t *testing.T, helper string) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "gitconfig")
	content := ""
	if helper != "" {
		content = "[credential]\n\thelper = \"" + helper + "\"\n"
	}
	if err := os.WriteFile(cfg, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
}

const fakeHelper = `!f() { test "$1" = get || exit 0; cat >/dev/null; echo username=alice; echo password=s3cret; }; f`

func azureServer(t *testing.T, accept func(*http.Request) bool) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !accept(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[{"name":"Repo","remoteUrl":"https://devops.client.com/Coll/Proj/_git/Repo",
			"defaultBranch":"refs/heads/main","project":{"name":"Proj"}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGitCredentialHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper")
	}
	isolateGit(t, fakeHelper)
	user, secret, err := gitCredential(context.Background(), "https://devops.client.com/tfs/Coll")
	if err != nil || user != "alice" || secret != "s3cret" {
		t.Fatalf("got %q %q %v", user, secret, err)
	}
}

func TestGitCredentialNothingStoredDoesNotHang(t *testing.T) {
	isolateGit(t, "")
	start := time.Now()
	_, _, err := gitCredential(context.Background(), "https://example.invalid")
	if err == nil || !strings.Contains(err.Error(), "nothing stored") {
		t.Fatalf("expected 'nothing stored' error, got %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("took too long; prompting is probably not disabled")
	}
}

func TestAzureViaGitCredentialBasic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper")
	}
	isolateGit(t, fakeHelper)
	srv := azureServer(t, func(r *http.Request) bool {
		_, pass, ok := r.BasicAuth()
		return ok && pass == "s3cret"
	})
	p := ProviderConfig{Type: "azure", URL: srv.URL + "/Coll", Host: "127.0.0.1"}
	rs, method, err := p.list(context.Background())
	if err != nil || method != "git-credential" || len(rs) != 1 || rs[0].Name != "Proj/Repo" || rs[0].Branch != "main" {
		t.Fatalf("got %+v %q %v", rs, method, err)
	}
}

func TestFallsBackToBearerOn401(t *testing.T) {
	srv := azureServer(t, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer oauth-tok" })
	p := ProviderConfig{Type: "azure", URL: srv.URL + "/Coll", Token: "oauth-tok", Username: "bob"}
	rs, _, err := p.list(context.Background())
	if err != nil || len(rs) != 1 {
		t.Fatalf("got %v %v", rs, err)
	}
}

func TestAutoModesReportEveryFailure(t *testing.T) {
	isolateGit(t, "")
	t.Setenv("PATH", t.TempDir()) // no gh, no git → every method fails
	p := ProviderConfig{Type: "github", Host: "github.com"}
	_, err := p.credential(context.Background())
	if err == nil || !strings.Contains(err.Error(), "gh: gh is not installed") || !strings.Contains(err.Error(), "git-credential:") {
		t.Fatalf("got %v", err)
	}
}

func TestGitLabSSHToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	isolateGit(t, "")
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + calls + "\n" +
		"printf 'Token:   glpat-%s\\nScopes:  read_api\\nExpires: 2099-01-01\\n' $(wc -l < " + calls + " | tr -d ' ')\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	p := ProviderConfig{Type: "gitlab", Host: "gitlab.client.com", SSH: "ssh://git@gitlab.client.com:2222", Auth: "ssh"}
	ctx := context.Background()
	tok, err := gitlabSSHToken(ctx, p, false)
	if err != nil || tok != "glpat-1" {
		t.Fatalf("first: %q %v", tok, err)
	}
	if tok, _ = gitlabSSHToken(ctx, p, false); tok != "glpat-1" {
		t.Fatalf("expected cached token, got %q", tok)
	}
	if tok, _ = gitlabSSHToken(ctx, p, true); tok != "glpat-2" {
		t.Fatalf("expected renewed token, got %q", tok)
	}
	b, _ := os.ReadFile(calls)
	if !strings.Contains(string(b), "-o BatchMode=yes -p 2222 git@gitlab.client.com personal_access_token gx-") ||
		!strings.Contains(string(b), " read_api ") {
		t.Fatalf("unexpected ssh args: %s", b)
	}
	if st, _ := os.Stat(tokenCacheFile()); st == nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("token cache must be 0600")
	}
}

func TestAzDevopsList(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho \"$@\" > " + calls + "\n" + `cat <<'EOF'
{"count":3,"value":[{"name":"api","remoteUrl":"https://devops.client.com/tfs/CollA/Shop/_git/api","sshUrl":"ssh://devops.client.com:22/tfs/CollA/Shop/_git/api","defaultBranch":"refs/heads/main","project":{"name":"Shop","visibility":"private"}},
 {"name":"old","isDisabled":true,"project":{"name":"Shop"}},
 {"name":"x","project":{"name":"Other"}}]}
EOF
`
	if err := os.WriteFile(filepath.Join(bin, "az"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	p := ProviderConfig{Type: "azure", URL: "https://devops.client.com/tfs/CollA", Auth: "az-devops", Owners: []string{"Shop"}}
	rs, method, err := p.list(context.Background())
	if err != nil || method != "az-devops" || len(rs) != 1 || rs[0].Name != "Shop/api" || rs[0].Branch != "main" {
		t.Fatalf("got %+v %q %v", rs, method, err)
	}
	if b, _ := os.ReadFile(calls); !strings.Contains(string(b), "devops invoke --organization https://devops.client.com/tfs/CollA --area git --resource repositories") {
		t.Fatalf("unexpected az args: %s", b)
	}
}

func TestValidAuth(t *testing.T) {
	bad := []ProviderConfig{
		{Type: "github", Auth: "ssh"},
		{Type: "bitbucket", Auth: "az"},
		{Type: "gitlab", Auth: "token"},
	}
	for _, p := range bad {
		if validAuth(p) == nil {
			t.Errorf("%s/%s should be rejected", p.Type, p.Auth)
		}
	}
	if err := validAuth(ProviderConfig{Type: "gitlab", Auth: "ssh"}); err != nil {
		t.Error(err)
	}
}

func TestSSHTarget(t *testing.T) {
	cases := map[string][2]string{
		"":                     {"git@h.com", ""},
		"git@ssh.h.com":        {"git@ssh.h.com", ""},
		"ssh://git@h.com:2222": {"git@h.com", "2222"},
		"ssh://gitlab@h.com":   {"gitlab@h.com", ""},
	}
	for in, want := range cases {
		target, port := ProviderConfig{Host: "h.com", SSH: in}.sshTarget()
		if target != want[0] || port != want[1] {
			t.Errorf("%q: got %s %s", in, target, port)
		}
	}
}
