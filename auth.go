package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Method string // token | gh | glab | az | az-devops | git-credential | ssh
}

// authModes lists the explicit `auth` values each provider type accepts.
var authModes = map[string][]string{
	"github":    {"token", "gh", "git-credential"},
	"gitlab":    {"token", "glab", "git-credential", "ssh"},
	"bitbucket": {"token", "git-credential"},
	"azure":     {"token", "az", "az-devops", "git-credential"},
}

// Azure DevOps application ID, the resource to request Entra ID tokens for.
const azureDevOpsResource = "499b84ac-1321-427f-aa17-267ca6975798"

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
	case "azure":
		if p.Host == "dev.azure.com" {
			return []string{"git-credential", "az"}
		}
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
		errs = append(errs, m+": "+err.Error())
	}
	return credential{}, fmt.Errorf("no credentials (%s)", strings.Join(errs, "; "))
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
	case "az":
		c.Secret, err = cmdOutput(ctx, nil, "az", "account", "get-access-token",
			"--resource", azureDevOpsResource, "--query", "accessToken", "-o", "tsv")
	case "az-devops":
		// No token: the listing itself goes through `az repos list`.
		return c, nil
	case "git-credential":
		var user string
		user, c.Secret, err = gitCredential(ctx, p.credentialURL())
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
		// az prints warnings before the actual error.
		for _, line := range strings.Split(stderr.String(), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "WARNING:") {
				msg = line
				break
			}
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitCredential asks git's configured credential helpers (Git Credential
// Manager, osxkeychain, libsecret, store, …) for the credentials of rawURL.
// Prompting is disabled so it fails instead of blocking.
func gitCredential(ctx context.Context, rawURL string) (user, secret string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("bad URL %q", rawURL)
	}
	// git drops path itself unless credential.useHttpPath is set for the host.
	in := fmt.Sprintf("protocol=%s\nhost=%s\n", u.Scheme, u.Host)
	if p := strings.Trim(u.Path, "/"); p != "" {
		in += "path=" + p + "\n"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "credential.interactive=never", "credential", "fill")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=", "SSH_ASKPASS=")
	cmd.Stdin = strings.NewReader(in + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		if msg == "" {
			msg = err.Error()
		}
		return "", "", fmt.Errorf("nothing stored for %s (%s)", u.Host, strings.TrimPrefix(msg, "fatal: "))
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		switch k {
		case "username":
			user = v
		case "password":
			secret = v
		}
	}
	if secret == "" {
		return "", "", fmt.Errorf("nothing stored for %s", u.Host)
	}
	return user, secret, nil
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
	if !slices.Contains(authModes[p.Type], p.Auth) {
		return fmt.Errorf("auth %q is not supported for %s (use %s)", p.Auth, p.Type, strings.Join(authModes[p.Type], ", "))
	}
	if p.Auth == "token" && p.Token == "" && p.TokenEnv == "" && p.TokenCmd == "" {
		return errors.New(`auth "token" needs token, token_env or token_cmd`)
	}
	return nil
}
