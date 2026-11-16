package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Root        string           `toml:"root,omitempty"`
	DefaultHost string           `toml:"default_host,omitempty"`
	Protocol    string           `toml:"protocol,omitempty"`
	CacheTTL    string           `toml:"cache_ttl,omitempty"`
	Providers   []ProviderConfig `toml:"provider,omitempty"`

	path   string
	exists bool // a config file was found
	ttl    time.Duration
}

type ProviderConfig struct {
	Name     string   `toml:"name,omitempty"`
	Type     string   `toml:"type"` // github | gitlab | bitbucket | azure
	Host     string   `toml:"host,omitempty"`
	URL      string   `toml:"url,omitempty"` // API base, or the org/collection URL for azure
	Owners   []string `toml:"owners,omitempty"`
	Username string   `toml:"username,omitempty"`
	Token    string   `toml:"token,omitempty"`
	TokenEnv string   `toml:"token_env,omitempty"`
	TokenCmd string   `toml:"token_cmd,omitempty"`
	Auth     string   `toml:"auth,omitempty"` // token | gh | glab | git-credential | ssh; empty = automatic
	SSH      string   `toml:"ssh,omitempty"`  // GitLab auth = "ssh": destination, default git@<host>
	Protocol string   `toml:"protocol,omitempty"`
	Insecure bool     `toml:"insecure,omitempty"`
	Archived bool     `toml:"include_archived,omitempty"`
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

// configHeader starts every config file gx writes.
const configHeader = `# gx configuration, written by "gx setup" and "gx config".
# Comments in this file are not kept when gx saves it. Options: see README.md.

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
	cfg, err := readConfig(configPath())
	if err != nil {
		return nil, err
	}
	if v := os.Getenv("GX_ROOT"); v != "" {
		cfg.Root = v
	}
	return cfg, cfg.normalize()
}

// readConfig reads the file as written, without defaults; a missing file
// gives an empty config.
func readConfig(path string) (*Config, error) {
	cfg := &Config{path: path}
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, cfg); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		cfg.exists = true
	}
	return cfg, nil
}

// saveConfig validates cfg and writes it as is (without defaults) to path.
func saveConfig(path string, cfg *Config) error {
	check := *cfg
	check.Providers = slices.Clone(cfg.Providers)
	if err := check.normalize(); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString(configHeader)
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o600)
}

// normalize fills in defaults and validates.
func (cfg *Config) normalize() error {
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
			return fmt.Errorf("cache_ttl: %w", err)
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
				return fmt.Errorf("provider %d (azure): url is required", i+1)
			}
			p.URL = strings.TrimRight(p.URL, "/")
			if p.Host == "" {
				u, err := ParseRepoURL(p.URL+"/x/_git/x", "")
				if err != nil {
					return fmt.Errorf("provider %d (azure): bad url: %w", i+1, err)
				}
				p.Host = u.Host
			}
		default:
			return fmt.Errorf("provider %d: unknown type %q (github, gitlab, bitbucket, azure)", i+1, p.Type)
		}
		p.Host = strings.ToLower(p.Host)
		p.Auth = strings.ToLower(p.Auth)
		if err := validAuth(*p); err != nil {
			return fmt.Errorf("provider %s: %w", p.Label(), err)
		}
	}
	return nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}
