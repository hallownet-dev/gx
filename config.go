package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Root        string           `toml:"root"`
	DefaultHost string           `toml:"default_host"`
	Protocol    string           `toml:"protocol"`
	CacheTTL    string           `toml:"cache_ttl"`
	Providers   []ProviderConfig `toml:"provider"`

	path string
	ttl  time.Duration
}

type ProviderConfig struct {
	Name     string   `toml:"name"`
	Type     string   `toml:"type"` // github | gitlab | bitbucket | azure
	Host     string   `toml:"host"`
	URL      string   `toml:"url"` // API base, or the org/collection URL for azure
	Owners   []string `toml:"owners"`
	Username string   `toml:"username"`
	Token    string   `toml:"token"`
	TokenEnv string   `toml:"token_env"`
	TokenCmd string   `toml:"token_cmd"`
	Protocol string   `toml:"protocol"`
	Insecure bool     `toml:"insecure"`
	Archived bool     `toml:"include_archived"`
}

// ID identifies a provider in the cache; it changes when the endpoint does.
func (p ProviderConfig) ID() string {
	return p.Type + "|" + p.Host + "|" + p.URL + "|" + strings.Join(p.Owners, ",")
}

func (p ProviderConfig) Label() string {
	if p.Name != "" {
		return p.Name
	}
	return p.Type + ":" + p.Host
}

const sampleConfig = `# gx configuration
#
# Repositories are cloned to <root>/<host>/<full path>, e.g.
#   ~/Projects/github.com/org/repo
#   ~/Projects/gitlab.com/group/subgroup/repo
#   ~/Projects/devops.client.com/Collection/Project/repo

root = "~/Projects"
default_host = "github.com"   # used for shorthands like "gx cl org/repo"
protocol = "ssh"              # ssh | https, default clone protocol
cache_ttl = "15m"             # how long "gx l" reuses the remote repo list

# Tokens are resolved from token, token_env or token_cmd (first set wins).
# GitHub falls back to "gh auth token", GitLab to "glab config get token".

[[provider]]
type = "github"
host = "github.com"
# owners = ["hallownet-dev"]  # only list repos of these users/orgs

# [[provider]]
# name = "client-gitlab"
# type = "gitlab"
# host = "gitlab.client.com"
# token_env = "CLIENT_GITLAB_TOKEN"
# owners = ["team/subgroup"]   # path prefixes

# [[provider]]
# name = "client-azure"
# type = "azure"
# url = "https://devops.client.com/tfs/DefaultCollection"  # or https://dev.azure.com/<org>
# token_cmd = "pass show client/azure-pat"
# protocol = "https"
# owners = ["ProjectA"]        # project names

# [[provider]]
# type = "bitbucket"
# host = "bitbucket.org"       # cloud; any other host is treated as Bitbucket Server/DC
# username = "me@example.com"  # cloud: Atlassian account email (API token) or username (app password)
# token_env = "BITBUCKET_TOKEN"
# owners = ["workspace"]       # cloud: workspaces, server: project keys
`

func configPath() string {
	if p := os.Getenv("GX_CONFIG"); p != "" {
		return p
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "gx", "config.toml")
}

func loadConfig() (*Config, error) {
	cfg := &Config{}
	path := configPath()
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	} else {
		cfg.Providers = []ProviderConfig{{Type: "github", Host: "github.com"}}
	}
	cfg.path = path

	if v := os.Getenv("GX_ROOT"); v != "" {
		cfg.Root = v
	}
	if cfg.Root == "" {
		cfg.Root = "~/Projects"
	}
	cfg.Root = expandHome(cfg.Root)
	if cfg.DefaultHost == "" {
		cfg.DefaultHost = "github.com"
	}
	if cfg.Protocol == "" {
		cfg.Protocol = "ssh"
	}
	cfg.ttl = 15 * time.Minute
	if cfg.CacheTTL != "" {
		d, err := time.ParseDuration(cfg.CacheTTL)
		if err != nil {
			return nil, fmt.Errorf("cache_ttl: %w", err)
		}
		cfg.ttl = d
	}

	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		p.Type = strings.ToLower(p.Type)
		if p.Protocol == "" {
			p.Protocol = cfg.Protocol
		}
		switch p.Type {
		case "github", "gitlab", "bitbucket":
			if p.Host == "" {
				p.Host = map[string]string{"github": "github.com", "gitlab": "gitlab.com", "bitbucket": "bitbucket.org"}[p.Type]
			}
		case "azure":
			if p.URL == "" {
				return nil, fmt.Errorf("provider %d (azure): url is required", i+1)
			}
			p.URL = strings.TrimRight(p.URL, "/")
			if p.Host == "" {
				u, err := ParseRepoURL(p.URL+"/x/_git/x", "")
				if err != nil {
					return nil, fmt.Errorf("provider %d (azure): bad url: %w", i+1, err)
				}
				p.Host = u.Host
			}
		default:
			return nil, fmt.Errorf("provider %d: unknown type %q (github, gitlab, bitbucket, azure)", i+1, p.Type)
		}
		p.Host = strings.ToLower(p.Host)
	}
	return cfg, nil
}

func (p ProviderConfig) resolveToken() (string, error) {
	switch {
	case p.Token != "":
		return p.Token, nil
	case p.TokenEnv != "":
		if v := os.Getenv(p.TokenEnv); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("environment variable %s is empty", p.TokenEnv)
	case p.TokenCmd != "":
		return runTokenCmd(p.TokenCmd)
	}
	switch p.Type {
	case "github":
		if out, err := exec.Command("gh", "auth", "token", "--hostname", p.Host).Output(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		return "", fmt.Errorf("no token: set token_env/token_cmd or run \"gh auth login --hostname %s\"", p.Host)
	case "gitlab":
		if out, err := exec.Command("glab", "config", "get", "token", "--host", p.Host).Output(); err == nil && len(strings.TrimSpace(string(out))) > 0 {
			return strings.TrimSpace(string(out)), nil
		}
	}
	return "", fmt.Errorf("no token configured (token, token_env or token_cmd)")
}

func runTokenCmd(cmd string) (string, error) {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.Command("cmd", "/C", cmd)
	} else {
		c = exec.Command("sh", "-c", cmd)
	}
	c.Stderr = os.Stderr
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("token_cmd failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}

func cmdConfig(args []string) error {
	path := configPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(sampleConfig), 0o600); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, green("created ")+path)
	}
	if len(args) > 0 && (args[0] == "edit" || args[0] == "e") {
		editor := os.Getenv("VISUAL")
		if editor == "" {
			editor = os.Getenv("EDITOR")
		}
		if editor == "" {
			editor = "vi"
			if runtime.GOOS == "windows" {
				editor = "notepad"
			}
		}
		fields := strings.Fields(editor)
		return attached("", fields[0], append(fields[1:], path)...)
	}
	fmt.Println(path)
	return nil
}
