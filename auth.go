package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// credential is what a provider API is called with. How it is sent (Bearer
// or Basic) is decided per provider by apiClient.
type credential struct {
	User   string
	Secret string
	Method string // token | gh | glab | git-credential | ssh
}

// authModes lists the explicit `auth` values each provider type accepts.
var authModes = map[string][]string{
	"github":    {"token", "gh", "git-credential"},
	"gitlab":    {"token", "glab", "git-credential", "ssh"},
	"bitbucket": {"token", "git-credential"},
	"azure":     {"token", "git-credential"},
}

// modes returns the auth methods to try, in order. "ssh" is never automatic
// because it creates a token on the server.
func (p ProviderConfig) modes() []string {
	if p.Auth != "" {
		return []string{p.Auth}
	}
	if p.Token != "" || p.TokenEnv != "" || p.TokenCmd != "" {
		return []string{"token"}
	}
	switch p.Type {
	case "github":
		return []string{"gh", "git-credential"}
	case "gitlab":
		return []string{"glab", "git-credential"}
	}
	return []string{"git-credential"}
}

func (p ProviderConfig) credential(ctx context.Context) (credential, error) {
	var errs []string
	for _, m := range p.modes() {
		c, err := p.credentialVia(ctx, m, false)
		if err == nil {
			return c, nil
		}
		if errors.Is(err, errWindowsSignIn) {
			return credential{}, err // signing in again wouldn't help
		}
		errs = append(errs, m+": "+err.Error())
	}
	return credential{}, fmt.Errorf("%w (%s)", errNoCredential, strings.Join(errs, "; "))
}

const signInHint = "run gx l in a terminal to sign in"

// errNoCredential means no auth method produced a credential.
var errNoCredential = errors.New("no credentials")

var helperChecked bool

// ensureHelper warns, once per run, when git has no credential helper: a
// sign-in would then work only until gx exits. If Git Credential Manager is
// installed, it offers to configure it.
func ensureHelper(a *asker) {
	if helperChecked {
		return
	}
	helperChecked = true
	if out, _ := gitOut("", "config", "--get-all", "credential.helper"); strings.TrimSpace(out) != "" {
		return
	}
	if _, err := exec.LookPath("git-credential-manager"); err == nil &&
		a.yes("git has no credential helper, so this sign-in wouldn't be remembered. Use Git Credential Manager (git config --global credential.helper manager)?", true) {
		if _, err := gitOut("", "config", "--global", "credential.helper", "manager"); err != nil {
			fmt.Fprintf(a.out, "%s %v\n", red("✗"), err)
		}
		return
	}
	fmt.Fprintf(a.out, "%s git has no credential helper: this sign-in won't be remembered (see README, Authentication)\n", yellow("!"))
}

// errWindowsSignIn means Git Credential Manager answered with an empty
// credential, which it does for servers using Windows sign-in (NTLM/Kerberos).
var errWindowsSignIn = errors.New("server returned no credential (Windows sign-in?); set token_cmd to a PAT")

// offerSignIn reports whether an interactive git sign-in can fix a listing
// that failed with err after using the given auth method: nothing was
// stored, or the server rejected the git credential. A rejected gh/glab token
// or a configured token is not something a git sign-in replaces.
func (p ProviderConfig) offerSignIn(err error, method string) bool {
	if !slices.Contains(p.modes(), "git-credential") {
		return false
	}
	var se *statusError
	return errors.Is(err, errNoCredential) ||
		method == "git-credential" && errors.As(err, &se) && se.code == http.StatusUnauthorized
}

// signIn asks git's credential helpers for a credential with prompting
// allowed (Git Credential Manager opens a browser or asks for a PAT, plain
// git asks in the terminal), lists with it, and stores it on success. With
// stale, the stored credential the server rejected is forgotten first, so
// the helper asks instead of handing it back. This is the only place gx
// changes what credential helpers store.
func (p ProviderConfig) signIn(ctx context.Context, stale bool) ([]Remote, error) {
	if stale {
		if user, secret, err := gitCredential(ctx, p.credentialURL(), false); err == nil {
			gitCredentialStore(ctx, p.credentialURL(), credential{User: user, Secret: secret}, false)
		}
	}
	user, secret, err := gitCredential(ctx, p.credentialURL(), true)
	if err != nil {
		return nil, err
	}
	if p.Username != "" && user == "" {
		user = p.Username
	}
	cred := credential{Method: "git-credential", User: user, Secret: secret}
	rs, err := p.listCred(ctx, cred)
	var se *statusError
	if err == nil || errors.As(err, &se) && se.code == http.StatusUnauthorized {
		gitCredentialStore(ctx, p.credentialURL(), cred, err == nil)
	}
	return rs, err
}

func (p ProviderConfig) credentialVia(ctx context.Context, mode string, renew bool) (credential, error) {
	c := credential{Method: mode, User: p.Username}
	var err error
	switch mode {
	case "token":
		c.Secret, err = p.staticToken(ctx)
	case "gh":
		c.Secret, err = cmdOutput(ctx, nil, "gh", "auth", "token", "--hostname", p.Host)
	case "glab":
		c.Secret, err = cmdOutput(ctx, nil, "glab", "config", "get", "token", "--host", p.Host)
	case "git-credential":
		var user string
		user, c.Secret, err = gitCredential(ctx, p.credentialURL(), false)
		if user != "" {
			c.User = user
		}
	case "ssh":
		c.Secret, err = gitlabSSHToken(ctx, p, renew)
	default:
		err = fmt.Errorf("unknown auth mode %q", mode)
	}
	if err == nil && c.Secret == "" {
		err = errors.New("empty token")
	}
	return c, err
}

func (p ProviderConfig) staticToken(ctx context.Context) (string, error) {
	switch {
	case p.Token != "":
		return p.Token, nil
	case p.TokenEnv != "":
		if v := os.Getenv(p.TokenEnv); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("environment variable %s is empty", p.TokenEnv)
	case p.TokenCmd != "":
		if runtime.GOOS == "windows" {
			return cmdOutput(ctx, nil, "cmd", "/C", p.TokenCmd)
		}
		return cmdOutput(ctx, nil, "sh", "-c", p.TokenCmd)
	}
	return "", errors.New("set token, token_env or token_cmd")
}

// credentialURL is the URL git would use for this provider over HTTPS; git
// credential helpers key their entries by it.
func (p ProviderConfig) credentialURL() string {
	if p.Type == "azure" || (p.Type == "bitbucket" && p.Host != "bitbucket.org" && p.URL != "") {
		return p.URL
	}
	return "https://" + p.Host
}

// cmdOutput runs a helper command and returns its trimmed stdout, or its
// first stderr line as the error.
func cmdOutput(ctx context.Context, env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("%s is not installed", name)
	}
	if err != nil {
		msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// credentialInput is the request git credential reads for rawURL.
func credentialInput(rawURL string) (string, *url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", nil, fmt.Errorf("bad URL %q", rawURL)
	}
	// git drops path itself unless credential.useHttpPath is set for the host.
	in := fmt.Sprintf("protocol=%s\nhost=%s\n", u.Scheme, u.Host)
	if p := strings.Trim(u.Path, "/"); p != "" {
		in += "path=" + p + "\n"
	}
	return in, u, nil
}

// noPrompt keeps git and Git Credential Manager from asking the user.
var noPrompt = []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=", "SSH_ASKPASS="}

// gitCredential asks git's configured credential helpers (Git Credential
// Manager, osxkeychain, libsecret, store, …) for the credentials of rawURL.
// Unless interactive, prompting is disabled so it fails instead of blocking.
func gitCredential(ctx context.Context, rawURL string, interactive bool) (user, secret string, err error) {
	in, u, err := credentialInput(rawURL)
	if err != nil {
		return "", "", err
	}
	var cmd *exec.Cmd
	var stderr bytes.Buffer
	if interactive {
		cmd = exec.CommandContext(ctx, "git", "credential", "fill")
		cmd.Stderr = os.Stderr
	} else {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cmd = exec.CommandContext(ctx, "git", "-c", "credential.interactive=never", "credential", "fill")
		cmd.Env = append(os.Environ(), noPrompt...)
		cmd.Stderr = &stderr
	}
	cmd.Stdin = strings.NewReader(in + "\n")
	out, err := cmd.Output()
	if err != nil {
		msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		if msg == "" {
			msg = err.Error()
		}
		return "", "", fmt.Errorf("nothing stored for %s (%s)", u.Host, strings.TrimPrefix(msg, "fatal: "))
	}
	gotPassword := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		switch k {
		case "username":
			user = v
		case "password":
			secret, gotPassword = v, true
		}
	}
	if gotPassword && secret == "" {
		// Git Credential Manager answers this way for servers that use
		// Windows sign-in (NTLM/Kerberos), which gx can't speak.
		return "", "", fmt.Errorf("%s: %w", u.Host, errWindowsSignIn)
	}
	if secret == "" {
		return "", "", fmt.Errorf("nothing stored for %s", u.Host)
	}
	return user, secret, nil
}

// gitCredentialStore tells git's credential helpers that c worked (approve)
// or was rejected (reject), so they store or forget it.
func gitCredentialStore(ctx context.Context, rawURL string, c credential, approve bool) {
	in, _, err := credentialInput(rawURL)
	if err != nil {
		return
	}
	action := "reject"
	if approve {
		action = "approve"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "credential", action)
	cmd.Env = append(os.Environ(), noPrompt...)
	cmd.Stdin = strings.NewReader(in + "username=" + c.User + "\npassword=" + c.Secret + "\n\n")
	_ = cmd.Run()
}

// ---- GitLab: token minted over SSH ----

type cachedToken struct {
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
}

const sshTokenDays = 7

func tokenCacheFile() string {
	return filepath.Join(filepath.Dir(cacheFile()), "tokens.json")
}

// gitlabSSHToken returns a read_api token created with the user's SSH key via
// `ssh git@host personal_access_token …` (GitLab 14.2+), cached until a day
// before it expires.
func gitlabSSHToken(ctx context.Context, p ProviderConfig, renew bool) (string, error) {
	key := "gitlab-ssh|" + p.Host
	cache := map[string]cachedToken{}
	if b, err := os.ReadFile(tokenCacheFile()); err == nil {
		_ = json.Unmarshal(b, &cache)
	}
	if t, ok := cache[key]; ok && !renew && time.Until(t.Expires) > 24*time.Hour {
		return t.Token, nil
	}

	target, port := p.sshTarget()
	host, _ := os.Hostname()
	name := "gx-" + strings.ReplaceAll(host, " ", "-")
	expires := time.Now().UTC().AddDate(0, 0, sshTokenDays).Truncate(24 * time.Hour)
	args := []string{"-o", "BatchMode=yes"}
	if port != "" {
		args = append(args, "-p", port)
	}
	args = append(args, target, "personal_access_token", name, "read_api", expires.Format("2006-01-02"))
	out, err := cmdOutput(ctx, nil, "ssh", args...)
	if err != nil {
		return "", fmt.Errorf("ssh %s: %w", target, err)
	}
	token := ""
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Token:"); ok {
			token = strings.TrimSpace(v)
		}
	}
	if token == "" {
		msg, _, _ := strings.Cut(out, "\n")
		return "", fmt.Errorf("ssh %s: unexpected reply %q (GitLab 14.2+ required)", target, msg)
	}
	cache[key] = cachedToken{Token: token, Expires: expires}
	if b, err := json.Marshal(cache); err == nil {
		_ = os.MkdirAll(filepath.Dir(tokenCacheFile()), 0o700)
		_ = os.WriteFile(tokenCacheFile(), b, 0o600)
	}
	return token, nil
}

// sshTarget returns the ssh destination and port, from `ssh` in the config
// ("git@host", "ssh://git@host:2222") or git@<host>.
func (p ProviderConfig) sshTarget() (target, port string) {
	if p.SSH == "" {
		return "git@" + p.Host, ""
	}
	if u, err := url.Parse(p.SSH); err == nil && u.Scheme == "ssh" {
		target = u.Hostname()
		if u.User != nil {
			target = u.User.Username() + "@" + target
		}
		return target, u.Port()
	}
	return p.SSH, ""
}

func validAuth(p ProviderConfig) error {
	if p.Auth == "" {
		return nil
	}
	if p.Auth == "az" || p.Auth == "az-devops" {
		return fmt.Errorf("auth %q was removed; delete the line (gx signs in through git credentials)", p.Auth)
	}
	if !slices.Contains(authModes[p.Type], p.Auth) {
		return fmt.Errorf("auth %q is not supported for %s (use %s)", p.Auth, p.Type, strings.Join(authModes[p.Type], ", "))
	}
	if p.Auth == "token" && p.Token == "" && p.TokenEnv == "" && p.TokenCmd == "" {
		return errors.New(`auth "token" needs token, token_env or token_cmd`)
	}
	return nil
}
