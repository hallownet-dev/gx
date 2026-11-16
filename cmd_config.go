package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// asker asks questions line by line. Numbered menus instead of the fuzzy
// picker keep setup usable in any terminal and testable from a reader.
type asker struct {
	in  *bufio.Reader
	out io.Writer
}

func stdAsker() *asker { return &asker{in: bufio.NewReader(os.Stdin), out: os.Stderr} }

// line asks for a value; empty input (or end of input) keeps def.
func (a *asker) line(q, def string) string {
	if def != "" {
		fmt.Fprintf(a.out, "%s [%s]: ", q, def)
	} else {
		fmt.Fprintf(a.out, "%s: ", q)
	}
	s, _ := a.in.ReadString('\n')
	if s = strings.TrimSpace(s); s == "" {
		return def
	}
	return s
}

// yes asks a yes/no question; empty input means def, end of input means no.
func (a *asker) yes(q string, def bool) bool {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Fprintf(a.out, "%s %s ", q, hint)
	s, err := a.in.ReadString('\n')
	if err != nil && s == "" {
		fmt.Fprintln(a.out)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return def
	case "y", "yes":
		return true
	}
	return false
}

// choose shows a numbered menu and returns the chosen index. Empty input
// returns def; end of input returns -1.
func (a *asker) choose(title string, opts []string, def int) int {
	fmt.Fprintln(a.out, bold(title))
	for i, o := range opts {
		mark := " "
		if i == def {
			mark = "*"
		}
		fmt.Fprintf(a.out, " %s%2d) %s\n", mark, i+1, o)
	}
	for {
		fmt.Fprint(a.out, "> ")
		s, err := a.in.ReadString('\n')
		s = strings.TrimSpace(s)
		if s == "" {
			if err != nil {
				return -1
			}
			return def
		}
		if n, e := strconv.Atoi(s); e == nil && n >= 1 && n <= len(opts) {
			return n - 1
		}
		fmt.Fprintf(a.out, "type a number from 1 to %d\n", len(opts))
		if err != nil {
			return -1
		}
	}
}

// menu is choose where empty input means done (-1).
func (a *asker) menu(title string, opts []string) int { return a.choose(title, opts, -1) }

// checker talks to the VCS for setup and config; tests replace it.
type checker interface {
	// check lists p's repositories, signing in when needed, and returns how
	// many it found.
	check(a *asker, p ProviderConfig) (int, error)
	// collections returns the project collections when p is an Azure DevOps
	// Server URL above them, nil otherwise.
	collections(a *asker, p ProviderConfig) ([]string, error)
}

// liveChecker is the real checker. A credential entered during this run is
// kept per host until a listing proves it works, so one sign-in covers every
// collection on a server even without a credential helper.
type liveChecker struct{}

func newChecker() *liveChecker { return &liveChecker{} }

func normalized(p ProviderConfig) (ProviderConfig, error) {
	tmp := &Config{Providers: []ProviderConfig{p}}
	if err := tmp.normalize(); err != nil {
		return p, err
	}
	return tmp.Providers[0], nil
}

func (c *liveChecker) check(a *asker, p ProviderConfig) (int, error) {
	p, err := normalized(p)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if rs, ok := p.listSession(ctx); ok {
		gitCredentialStore(ctx, p.credentialURL(), sessionCreds[p.Host], true)
		return len(rs), nil
	}
	stop := spinner("checking " + p.Label())
	rs, method, err := p.list(ctx)
	stop()
	if err != nil && p.offerSignIn(err, method) {
		fmt.Fprintf(a.out, "%s: %v\n", p.Label(), err)
		ensureHelper(a)
		if a.yes("Sign in to "+p.Label()+"?", true) {
			sctx, scancel := context.WithTimeout(context.Background(), 5*time.Minute)
			rs, err = p.signIn(sctx, method == "git-credential")
			scancel()
		}
	}
	return len(rs), err
}

func (c *liveChecker) collections(a *asker, p ProviderConfig) ([]string, error) {
	p, err := normalized(p)
	if err != nil || p.Type != "azure" || p.Host == "dev.azure.com" {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cred, err := p.credential(ctx)
	var names []string
	if err == nil {
		names, err = azureCollections(ctx, p, cred)
	}
	// Nothing stored, or the stored git credential was rejected: sign in.
	if err != nil && p.offerSignIn(err, cred.Method) {
		fmt.Fprintf(a.out, "%s: %v\n", p.Label(), err)
		ensureHelper(a)
		if !a.yes("Sign in to "+p.Host+"?", true) {
			return nil, err
		}
		if cred.Method == "git-credential" {
			gitCredentialStore(ctx, p.credentialURL(), cred, false) // forget the stale one
		}
		user, secret, ferr := gitCredential(ctx, p.credentialURL(), true)
		if ferr != nil {
			return nil, ferr
		}
		if user == "" {
			user = p.Username
		}
		cred = credential{Method: "git-credential", User: user, Secret: secret}
		rememberSession(p, cred)
		names, err = azureCollections(ctx, p, cred)
	}
	if err == nil && len(names) > 0 && cred.Method == "git-credential" {
		gitCredentialStore(ctx, p.credentialURL(), cred, true) // the server accepted it
	}
	return names, err
}

var vcsTypes = []struct{ typ, name, host string }{
	{"github", "GitHub", "github.com"},
	{"gitlab", "GitLab", "gitlab.com"},
	{"bitbucket", "Bitbucket", "bitbucket.org"},
	{"azure", "Azure DevOps", ""},
}

// addProviders asks for one VCS (or, for an Azure DevOps Server URL, the
// collections to use) and checks each. Providers the user cancels or that
// fail the check and aren't kept are left out.
func addProviders(a *asker, c checker) []ProviderConfig {
	names := make([]string, len(vcsTypes))
	for i, v := range vcsTypes {
		names[i] = v.name
	}
	i := a.menu("Which VCS?", names)
	if i < 0 {
		return nil
	}
	v := vcsTypes[i]
	p := ProviderConfig{Type: v.typ}
	if v.typ == "azure" {
		p.URL = strings.TrimRight(a.line("URL (https://dev.azure.com/<org>, or your server: https://<server>/tfs, or one collection: https://<server>/tfs/<collection>)", ""), "/")
		if p.URL == "" {
			return nil
		}
	} else if h := a.line("Host ("+v.host+", or your server's host)", v.host); h != v.host {
		p.Host = h
	}
	p.Owners = splitList(a.line("Only list repositories of (comma-separated users/orgs/projects, empty = all)", ""))

	candidates := []ProviderConfig{p}
	if v.typ == "azure" {
		cols, err := c.collections(a, p)
		if err != nil {
			// Checking the URL itself would only fail again (a server URL
			// has no repository API).
			fmt.Fprintf(a.out, "%s %v\n", red("✗"), err)
			return nil
		}
		if len(cols) > 0 {
			candidates = nil
			for _, k := range pickCollections(a, cols) {
				q := p
				q.URL = p.URL + "/" + cols[k]
				candidates = append(candidates, q)
			}
		}
	}

	var out []ProviderConfig
	for _, q := range candidates {
		n, err := c.check(a, q)
		if err != nil {
			fmt.Fprintf(a.out, "%s %s: %v\n", red("✗"), q.Label(), err)
			if !a.yes("Keep it anyway?", false) {
				continue
			}
		} else {
			fmt.Fprintf(a.out, "%s %s: %d repositories\n", green("✓"), q.Label(), n)
		}
		out = append(out, q)
	}
	return out
}

// pickCollections lists the collections and returns the chosen indexes;
// empty input means all.
func pickCollections(a *asker, cols []string) []int {
	fmt.Fprintln(a.out, bold("Collections on this server"))
	for i, c := range cols {
		fmt.Fprintf(a.out, "  %2d) %s\n", i+1, c)
	}
	for {
		s := a.line("Which ones? (numbers, comma-separated, empty = all)", "")
		var idx []int
		ok := true
		for _, f := range splitList(s) {
			n, err := strconv.Atoi(f)
			if err != nil || n < 1 || n > len(cols) {
				ok = false
				break
			}
			idx = append(idx, n-1)
		}
		if !ok {
			fmt.Fprintf(a.out, "type numbers from 1 to %d\n", len(cols))
			continue
		}
		if len(idx) == 0 {
			for i := range cols {
				idx = append(idx, i)
			}
		}
		return idx
	}
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// runSetup builds a new config from scratch: root, clone protocol, VCSs.
func runSetup(a *asker, path string, c checker) error {
	if old, err := readConfig(path); err == nil && old.exists {
		if !a.yes("This replaces your config at "+path+". Continue?", false) {
			return errCancelled
		}
	}
	cfg := &Config{}
	if root := a.line("Where should repositories live?", "~/Projects"); root != "~/Projects" {
		cfg.Root = root
	}
	if a.choose("Clone over", []string{"ssh", "https"}, 0) == 1 {
		cfg.Protocol = "https"
	}
	for {
		cfg.Providers = append(cfg.Providers, addProviders(a, c)...)
		if !a.yes("Add another VCS?", false) {
			break
		}
	}
	// Shorthands like "gx cl org/repo" go to the first VCS.
	tmp := &Config{Providers: slices.Clone(cfg.Providers)}
	if tmp.normalize() == nil && len(tmp.Providers) > 0 && tmp.Providers[0].Host != "github.com" {
		cfg.DefaultHost = tmp.Providers[0].Host
	}
	if err := saveConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "%s %s (%d VCS)\n", green("saved"), path, len(cfg.Providers))
	return nil
}

// runConfig is the interactive config editor: pick a setting or provider to
// change it, or add a VCS. Every change is validated and saved right away.
func runConfig(a *asker, path string, c checker) error {
	for {
		cfg, err := readConfig(path)
		if err != nil {
			return err
		}
		settings := []struct {
			name string
			val  *string
			def  string
		}{
			{"root", &cfg.Root, "~/Projects"},
			{"protocol", &cfg.Protocol, "ssh"},
			{"default host", &cfg.DefaultHost, "github.com"},
			{"cache ttl", &cfg.CacheTTL, "15m"},
		}
		var opts []string
		for _, s := range settings {
			v := *s.val
			if v == "" {
				v = dim(s.def)
			}
			opts = append(opts, fmt.Sprintf("%-13s %s", s.name, v))
		}
		for _, p := range cfg.Providers {
			opts = append(opts, "provider      "+providerSummary(p))
		}
		opts = append(opts, "+ add a VCS")

		i := a.menu(path+" (empty to finish)", opts)
		switch {
		case i < 0:
			return nil
		case i < len(settings):
			s := settings[i]
			v := a.line(s.name, *s.val)
			if v == s.def {
				v = ""
			}
			*s.val = v
		case i < len(settings)+len(cfg.Providers):
			if !editProvider(a, cfg, i-len(settings), c) {
				continue
			}
		default:
			added := addProviders(a, c)
			if len(added) == 0 {
				continue
			}
			cfg.Providers = append(cfg.Providers, added...)
		}
		if err := saveConfig(path, cfg); err != nil {
			fmt.Fprintf(a.out, "%s not saved: %v\n", red("✗"), err)
			continue
		}
		fmt.Fprintln(a.out, green("saved"))
	}
}

// editProvider changes provider k in cfg; it reports whether cfg changed.
func editProvider(a *asker, cfg *Config, k int, c checker) bool {
	p := &cfg.Providers[k]
	switch a.menu(providerSummary(*p), []string{"check / sign in", "owners", "name", "remove"}) {
	case 0:
		if n, err := c.check(a, *p); err != nil {
			fmt.Fprintf(a.out, "%s %v\n", red("✗"), err)
		} else {
			fmt.Fprintf(a.out, "%s %d repositories\n", green("✓"), n)
		}
		return false
	case 1:
		p.Owners = splitList(a.line("Owners (comma-separated, empty = all)", strings.Join(p.Owners, ", ")))
	case 2:
		p.Name = a.line("Name", p.Name)
	case 3:
		cfg.Providers = append(cfg.Providers[:k], cfg.Providers[k+1:]...)
	default:
		return false
	}
	return true
}

func providerSummary(p ProviderConfig) string {
	s := p.Name
	if s == "" {
		s = p.Type
	}
	switch {
	case p.URL != "":
		s += " " + p.URL
	case p.Host != "":
		s += " " + p.Host
	}
	if len(p.Owners) > 0 {
		s += " (" + strings.Join(p.Owners, ", ") + ")"
	}
	return s
}

func cmdSetup(args []string) error {
	if !stdinTTY || !stderrTTY {
		return fmt.Errorf("gx setup needs a terminal")
	}
	return runSetup(stdAsker(), configPath(), newChecker())
}

func cmdConfig(args []string) error {
	path := configPath()
	if len(args) > 0 {
		switch args[0] {
		case "path":
			fmt.Println(path)
			return nil
		case "edit", "e":
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
		return fmt.Errorf("usage: gx config [path | edit]")
	}
	cfg, err := readConfig(path)
	if err != nil {
		return err
	}
	if !stdinTTY || !stderrTTY {
		if !cfg.exists {
			return fmt.Errorf("no config at %s; run gx setup", path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	}
	if !cfg.exists {
		return cmdSetup(nil)
	}
	return runConfig(stdAsker(), path, newChecker())
}

// requireConfig offers setup when there is no config file yet.
func requireConfig(cfg *Config) (*Config, error) {
	if cfg.exists {
		return cfg, nil
	}
	if !stdinTTY || !stderrTTY || !askYes(os.Stdin, "No gx config yet. Set it up now?") {
		return nil, fmt.Errorf("no config at %s; run gx setup", cfg.path)
	}
	if err := cmdSetup(nil); err != nil {
		return nil, err
	}
	return loadConfig()
}
