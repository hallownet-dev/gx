package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
		_, _ = w.Write([]byte(`{"value":[{"name":"Repo","remoteUrl":"https://devops.example.com/Coll/Proj/_git/Repo",
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
	user, secret, err := gitCredential(context.Background(), "https://devops.example.com/tfs/Coll", false)
	if err != nil || user != "alice" || secret != "s3cret" {
		t.Fatalf("got %q %q %v", user, secret, err)
	}
}

func TestGitCredentialNothingStoredDoesNotHang(t *testing.T) {
	isolateGit(t, "")
	start := time.Now()
	_, _, err := gitCredential(context.Background(), "https://example.invalid", false)
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

	p := ProviderConfig{Type: "gitlab", Host: "gitlab.example.com", SSH: "ssh://git@gitlab.example.com:2222", Auth: "ssh"}
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
	if !strings.Contains(string(b), "-o BatchMode=yes -p 2222 git@gitlab.example.com personal_access_token gx-") ||
		!strings.Contains(string(b), " read_api ") {
		t.Fatalf("unexpected ssh args: %s", b)
	}
	if st, _ := os.Stat(tokenCacheFile()); st == nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("token cache must be 0600")
	}
}

func TestValidAuth(t *testing.T) {
	bad := []ProviderConfig{
		{Type: "github", Auth: "ssh"},
		{Type: "bitbucket", Auth: "az"},
		{Type: "gitlab", Auth: "token"},
		{Type: "azure", Auth: "az"},
		{Type: "azure", Auth: "az-devops"},
	}
	for _, p := range bad {
		err := validAuth(p)
		if err == nil {
			t.Errorf("%s/%s should be rejected", p.Type, p.Auth)
		} else if strings.HasPrefix(p.Auth, "az") && !strings.Contains(err.Error(), "was removed") {
			t.Errorf("%s/%s: want 'was removed', got %v", p.Type, p.Auth, err)
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

// loggingHelper returns a credential helper that records each action (get,
// store, erase) in a log file and answers get with username/password.
func loggingHelper(t *testing.T, user, pass string) (helper, log string) {
	log = filepath.ToSlash(filepath.Join(t.TempDir(), "helper.log"))
	helper = `!f() { echo "$1" >> '` + log + `'; cat >/dev/null; test "$1" = get || exit 0; printf 'username=` + user + `\npassword=` + pass + `\n'; }; f`
	return helper, log
}

func helperLog(t *testing.T, log string) string {
	b, _ := os.ReadFile(log)
	return string(b)
}

func TestSignInApprovesOnSuccess(t *testing.T) {
	helper, log := loggingHelper(t, "alice", "s3cret")
	isolateGit(t, helper)
	srv := azureServer(t, func(r *http.Request) bool {
		_, pass, ok := r.BasicAuth()
		return ok && pass == "s3cret"
	})
	p := ProviderConfig{Type: "azure", URL: srv.URL + "/Coll", Host: "127.0.0.1"}
	rs, err := p.signIn(context.Background(), false)
	if err != nil || len(rs) != 1 {
		t.Fatalf("got %v %v", rs, err)
	}
	if l := helperLog(t, log); !strings.Contains(l, "store") || strings.Contains(l, "erase") {
		t.Fatalf("want approve and no reject; helper log: %q", l)
	}
}

func TestSignInRejectsStaleFirst(t *testing.T) {
	helper, log := loggingHelper(t, "alice", "s3cret")
	isolateGit(t, helper)
	srv := azureServer(t, func(r *http.Request) bool {
		_, pass, ok := r.BasicAuth()
		return ok && pass == "s3cret"
	})
	p := ProviderConfig{Type: "azure", URL: srv.URL + "/Coll", Host: "127.0.0.1"}
	if _, err := p.signIn(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	l := helperLog(t, log)
	if e, s := strings.Index(l, "erase"), strings.Index(l, "store"); e < 0 || s < e {
		t.Fatalf("want the stale credential erased before the new one is stored; helper log: %q", l)
	}
}

// A plain listing (doctor, scripts, declined prompt) never changes what the
// credential helper stores.
func TestPlainListNeverTouchesStore(t *testing.T) {
	for _, accept := range []bool{true, false} {
		helper, log := loggingHelper(t, "alice", "s3cret")
		isolateGit(t, helper)
		srv := azureServer(t, func(*http.Request) bool { return accept })
		p := ProviderConfig{Type: "azure", URL: srv.URL + "/Coll", Host: "127.0.0.1"}
		_, method, err := p.list(context.Background())
		if accept != (err == nil) {
			t.Fatalf("accept=%v: got %v", accept, err)
		}
		if !accept && !p.offerSignIn(err, method) {
			t.Fatalf("a 401 on a git credential should offer sign-in")
		}
		if l := helperLog(t, log); strings.Contains(l, "store") || strings.Contains(l, "erase") {
			t.Fatalf("accept=%v: helper log: %q", accept, l)
		}
	}
}

func TestNoRejectForToken(t *testing.T) {
	helper, log := loggingHelper(t, "alice", "s3cret")
	isolateGit(t, helper)
	srv := azureServer(t, func(*http.Request) bool { return false })
	p := ProviderConfig{Type: "azure", URL: srv.URL + "/Coll", Host: "127.0.0.1", Token: "x"}
	if _, _, err := p.list(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	if l := helperLog(t, log); strings.Contains(l, "erase") {
		t.Fatalf("a token failure must not erase git credentials; helper log: %q", l)
	}
}

func TestOfferSignIn(t *testing.T) {
	p := ProviderConfig{Type: "github", Host: "github.com"}
	type c struct {
		err    error
		method string
	}
	yes := []c{
		{fmt.Errorf("x: %w", errNoCredential), ""},
		{&statusError{code: 401}, "git-credential"},
		{fmt.Errorf("wrapped: %w", &statusError{code: 401}), "git-credential"},
	}
	no := []c{
		{&statusError{code: 401}, "gh"}, // a git sign-in can't fix a rejected gh token
		{&statusError{code: 500}, "git-credential"},
		{errors.New("dial tcp: connection refused"), "git-credential"},
		{fmt.Errorf("x: %w", errWindowsSignIn), ""},
		{nil, ""},
	}
	for _, x := range yes {
		if !p.offerSignIn(x.err, x.method) {
			t.Errorf("offerSignIn(%v, %q) = false", x.err, x.method)
		}
	}
	for _, x := range no {
		if p.offerSignIn(x.err, x.method) {
			t.Errorf("offerSignIn(%v, %q) = true", x.err, x.method)
		}
	}
	if (ProviderConfig{Type: "azure", Token: "x"}).offerSignIn(fmt.Errorf("%w", errNoCredential), "") {
		t.Error("a token-only provider can't sign in through git")
	}
}

// A Windows-sign-in server must not be offered a sign-in that can't work.
func TestWindowsSignInNotOffered(t *testing.T) {
	helper, _ := loggingHelper(t, "", "")
	isolateGit(t, helper)
	p := ProviderConfig{Type: "azure", URL: "https://devops.example.com/tfs/Coll", Host: "devops.example.com"}
	_, method, err := p.list(context.Background())
	if err == nil || !strings.Contains(err.Error(), "token_cmd") || p.offerSignIn(err, method) {
		t.Fatalf("got %v (offer=%v)", err, p.offerSignIn(err, method))
	}
}

func TestGitCredentialEmptyPassword(t *testing.T) {
	helper, _ := loggingHelper(t, "", "")
	isolateGit(t, helper)
	_, _, err := gitCredential(context.Background(), "https://devops.example.com/tfs/Coll", false)
	if err == nil || !strings.Contains(err.Error(), "Windows sign-in") {
		t.Fatalf("got %v", err)
	}
}

func TestAzureCollections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects/_apis/projectCollections":
			_, _ = w.Write([]byte(`{"count":2,"value":[{"name":"Alpha"},{"name":"Beta"}]}`))
		case "/projects/Alpha/_apis/projectCollections": // some servers answer at collection level too
			_, _ = w.Write([]byte(`{"count":2,"value":[{"name":"Alpha"},{"name":"Beta"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	cred := credential{Method: "token", Secret: "x"}
	ctx := context.Background()
	got, err := azureCollections(ctx, ProviderConfig{Type: "azure", URL: srv.URL + "/projects"}, cred)
	if err != nil || !slices.Equal(got, []string{"Alpha", "Beta"}) {
		t.Fatalf("server URL: %v %v", got, err)
	}
	for _, u := range []string{srv.URL + "/projects/Alpha", srv.URL + "/tfs/Coll"} {
		if got, err := azureCollections(ctx, ProviderConfig{Type: "azure", URL: u}, cred); err != nil || got != nil {
			t.Fatalf("collection URL %s: want nil, got %v %v", u, got, err)
		}
	}
}

// A credential entered for one provider is reused for others on the same
// host in the same run (several Azure DevOps Server collections).
func TestSessionCredentialSharedByHost(t *testing.T) {
	isolateGit(t, "")
	srv := azureServer(t, func(r *http.Request) bool {
		_, pass, ok := r.BasicAuth()
		return ok && pass == "s3cret"
	})
	sessionCreds = map[string]credential{}
	a := ProviderConfig{Type: "azure", URL: srv.URL + "/Coll", Host: "127.0.0.1"}
	b := ProviderConfig{Type: "azure", URL: srv.URL + "/Other", Host: "127.0.0.1"}
	if _, ok := b.listSession(context.Background()); ok {
		t.Fatal("nothing signed in yet")
	}
	rememberSession(a, credential{Method: "git-credential", User: "alice", Secret: "s3cret"})
	if rs, ok := b.listSession(context.Background()); !ok || len(rs) != 1 {
		t.Fatalf("want reuse for the same host, got %v %v", rs, ok)
	}
	rememberSession(a, credential{Method: "git-credential", Secret: "wrong"})
	if _, ok := b.listSession(context.Background()); ok {
		t.Fatal("a rejected session credential must not count")
	}
}

func TestStatusErrorShowsJSONMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"$id":"1","innerException":null,"message":"TF400813: Resource not available for anonymous access.","typeName":"` + strings.Repeat("x", 400) + `"}`))
	}))
	t.Cleanup(srv.Close)
	_, _, err := ProviderConfig{Type: "azure", URL: srv.URL + "/Coll", Token: "x"}.list(context.Background())
	if err == nil || !strings.HasSuffix(err.Error(), "401 Unauthorized TF400813: Resource not available for anonymous access.") {
		t.Fatalf("got %v", err)
	}
}
