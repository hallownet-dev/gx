package main

import (
	"fmt"
	"os"
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
	Auth     string   `toml:"auth"` // token | gh | glab | az | az-devops | git-credential | ssh; empty = automatic
	SSH      string   `toml:"ssh"`  // GitLab auth = "ssh": destination, default git@<host>
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

# Authentication. With token, token_env or token_cmd set, that token is used.
# Otherwise gx reuses logins you already have, trying in order:
#   github     gh auth token          → git credential helper
#   gitlab     glab                   → git credential helper
#   azure      git credential helper  → az account get-access-token (dev.azure.com)
#   bitbucket  git credential helper
# "git credential helper" = whatever git uses for HTTPS (Git Credential
# Manager, keychain, libsecret…), so cloning once over HTTPS is enough.
# Force one method with auth = "token" | "gh" | "glab" | "az" | "git-credential".
# Azure only: auth = "az-devops" lists via "az repos list" with your
# "az devops login" session (works for Azure DevOps Server too).
# GitLab only: auth = "ssh" creates a 7-day read_api token with your SSH key
# (ssh git@host personal_access_token …, GitLab 14.2+) and renews it.

[[provider]]
type = "github"
host = "github.com"
# owners = ["hallownet-dev"]  # only list repos of these users/orgs

# [[provider]]
# name = "client-gitlab"
# type = "gitlab"
# host = "gitlab.client.com"
# auth = "ssh"                 # or token_env = "CLIENT_GITLAB_TOKEN"
# ssh = "ssh://git@gitlab.client.com:2222"   # only if SSH is not git@<host>:22
# owners = ["team/subgroup"]   # path prefixes

# [[provider]]
# name = "client-azure"
# type = "azure"
# url = "https://devops.client.com/tfs/DefaultCollection"  # or https://dev.azure.com/<org>
# (no token: uses Git Credential Manager; or token_cmd = "pass show client/azure-pat")
# protocol = "https"
# owners = ["ProjectA"]        # project names

# [[provider]]
# type = "bitbucket"
# host = "bitbucket.org"       # cloud; any other host is treated as Bitbucket Server/DC
# (no token: uses Git Credential Manager; or set token_env + username, see README)
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
		p.Auth = strings.ToLower(p.Auth)
		if err := validAuth(*p); err != nil {
			return nil, fmt.Errorf("provider %s: %w", p.Label(), err)
		}
	}
	return cfg, nil
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
