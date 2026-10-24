package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// flags splits args into known boolean flags and the remaining positionals.
func flags(args []string, known ...string) (map[string]bool, []string) {
	set := map[string]bool{}
	var rest []string
	for i, a := range args {
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		matched := false
		for _, k := range known {
			for _, alias := range strings.Split(k, "|") {
				if a == alias {
					set[strings.Split(k, "|")[0]] = true
					matched = true
				}
			}
		}
		if !matched {
			rest = append(rest, a)
		}
	}
	return set, rest
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// gx l: pick remote repos, clone missing ones, fast-forward existing ones.
func cmdList(args []string) error {
	f, rest := flags(args, "-r|--refresh", "-p|--print")
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	remotes := loadRemotes(cfg, f["-r"])
	if len(remotes) == 0 {
		return fmt.Errorf("no repositories found (check %s or run gx doctor)", cfg.path)
	}
	local := make([]bool, len(remotes))
	for i, r := range remotes {
		local[i] = isDir(r.URL.LocalPath(cfg.Root))
	}
	if f["-p"] {
		for i, r := range remotes {
			mark := " "
			if local[i] {
				mark = "●"
			}
			fmt.Printf("%s %s\n", mark, r.URL.Key())
		}
		return nil
	}

	idx, err := pick(len(remotes), func(i int) string {
		mark := "  "
		if local[i] {
			mark = "● "
		}
		return mark + remotes[i].URL.Key()
	}, true, strings.Join(rest, " "), "tab select · enter clone/pull · ● = cloned", func(i int) string {
		r := remotes[i]
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n\n", bold(r.Name))
		if r.Desc != "" {
			fmt.Fprintf(&b, "%s\n\n", r.Desc)
		}
		var tags []string
		if r.Private {
			tags = append(tags, "private")
		}
		if r.Fork {
			tags = append(tags, "fork")
		}
		if r.Archived {
			tags = append(tags, "archived")
		}
		if len(tags) > 0 {
			fmt.Fprintf(&b, "%s\n", yellow(strings.Join(tags, " · ")))
		}
		if r.Branch != "" {
			fmt.Fprintf(&b, "branch    %s\n", r.Branch)
		}
		if !r.Updated.IsZero() {
			fmt.Fprintf(&b, "updated   %s\n", relTime(r.Updated))
		}
		fmt.Fprintf(&b, "provider  %s\n", r.Provider)
		fmt.Fprintf(&b, "clone     %s\n", r.Clone)
		path := r.URL.LocalPath(cfg.Root)
		if local[i] {
			fmt.Fprintf(&b, "\n%s %s\n", green("●"), path)
		} else {
			fmt.Fprintf(&b, "\n%s %s\n", dim("→"), dim(path))
		}
		return b.String()
	})
	if err != nil {
		return err
	}

	var failed int
	for _, i := range idx {
		if err := ensureRepo(cfg, remotes[i].URL, remotes[i].Clone); err != nil {
			fmt.Fprintf(os.Stderr, "%s %s: %v\n", red("✗"), remotes[i].URL.Key(), err)
			failed++
		}
	}
	if len(idx) == 1 && failed == 0 {
		return setCD(remotes[idx[0]].URL.LocalPath(cfg.Root))
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d failed", failed, len(idx))
	}
	return nil
}

// ensureRepo clones u into the tree, or fast-forwards it when present.
func ensureRepo(cfg *Config, u RepoURL, cloneURL string) error {
	path := u.LocalPath(cfg.Root)
	if isDir(filepath.Join(path, ".git")) {
		fmt.Fprintf(os.Stderr, "%s %s\n", blue("↻"), u.Key())
		out, err := gitOut(path, "pull", "--ff-only", "--quiet")
		if err != nil {
			return err
		}
		if out != "" {
			fmt.Fprintln(os.Stderr, out)
		}
		return nil
	}
	if isDir(path) {
		if entries, _ := os.ReadDir(path); len(entries) > 0 {
			return fmt.Errorf("%s exists and is not a git repository", path)
		}
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", green("↓"), u.Key())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return attached("", "git", "clone", cloneURL, path)
}

// gx cl <url|owner/repo> [git clone args]
func cmdClone(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gx cl <url | owner/repo | host/path/repo>")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	u, err := ParseRepoURL(args[0], cfg.DefaultHost)
	if err != nil {
		return err
	}
	protocol := cfg.Protocol
	for _, p := range cfg.Providers {
		if p.Host == u.Host {
			protocol = p.Protocol
			break
		}
	}
	path := u.LocalPath(cfg.Root)
	if isDir(filepath.Join(path, ".git")) {
		fmt.Fprintf(os.Stderr, "%s already cloned\n", u.Key())
		return setCD(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cloneArgs := append([]string{"clone"}, args[1:]...)
	if err := attached("", "git", append(cloneArgs, u.CloneURL(protocol), path)...); err != nil {
		return err
	}
	return setCD(path)
}

// gx j [query]: jump to a local repo.
func cmdJump(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	repos := findRepos(cfg.Root)
	if len(repos) == 0 {
		return fmt.Errorf("no repositories under %s", cfg.Root)
	}
	idx, err := pick(len(repos), func(i int) string { return relTo(cfg.Root, repos[i]) },
		false, strings.Join(args, " "), "", func(i int) string { return statusPreview(repos[i]) })
	if err != nil {
		return err
	}
	return setCD(repos[idx[0]])
}

func statusPreview(dir string) string {
	s, err := readStatus(dir)
	if err != nil {
		return red(err.Error())
	}
	var b strings.Builder
	b.WriteString(branchLine(s, stashCount(dir)) + "\n")
	if last, err := gitOut(dir, "log", "-1", "--format=%h %s · %cr"); err == nil && last != "" {
		b.WriteString(dim(last) + "\n")
	}
	b.WriteString("\n")
	writeChanges(&b, s, 30)
	return b.String()
}

// gx ls: print local repos.
func cmdLs(args []string) error {
	f, _ := flags(args, "-r|--relative")
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	for _, r := range findRepos(cfg.Root) {
		if f["-r"] {
			r = relTo(cfg.Root, r)
		}
		fmt.Println(r)
	}
	return nil
}

// gx o: open the current repo (or a given URL/path) in the browser.
func cmdOpen(args []string) error {
	remote := ""
	if len(args) > 0 {
		remote = args[0]
		if isDir(remote) {
			remote = originURL(remote)
		}
	} else {
		top, err := repoTop("")
		if err != nil {
			return err
		}
		remote = originURL(top)
	}
	web := WebURL(remote)
	if web == "" {
		return fmt.Errorf("cannot derive a web URL from %q", remote)
	}
	fmt.Fprintln(os.Stderr, web)
	return openBrowser(web)
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

// gx ov: overview of every local repo; shows only those needing attention.
func cmdOverview(args []string) error {
	f, _ := flags(args, "-a|--all", "-f|--fetch")
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	repos := findRepos(cfg.Root)
	type row struct {
		rel, branch string
		notes       []string
	}
	rows := make([]row, len(repos))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	stop := spinner(fmt.Sprintf("checking %d repositories", len(repos)))
	for i, dir := range repos {
		wg.Add(1)
		go func(i int, dir string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := row{rel: relTo(cfg.Root, dir)}
			if f["-f"] {
				if _, err := gitOut(dir, "fetch", "--quiet", "--prune"); err != nil {
					r.notes = append(r.notes, red("fetch failed"))
				}
			}
			s, err := readStatus(dir)
			if err != nil {
				r.notes = append(r.notes, red(err.Error()))
				rows[i] = r
				return
			}
			r.branch = s.Head
			if n := s.Dirty(); n > 0 {
				r.notes = append(r.notes, yellow(fmt.Sprintf("✎ %d changed", n)))
			}
			if s.Ahead > 0 {
				r.notes = append(r.notes, green(fmt.Sprintf("↑%d unpushed", s.Ahead)))
			}
			if s.Behind > 0 {
				r.notes = append(r.notes, red(fmt.Sprintf("↓%d behind", s.Behind)))
			}
			if s.Upstream == "" && !s.Initial && s.Head != "(detached)" {
				r.notes = append(r.notes, magenta("no upstream"))
			}
			if n := stashCount(dir); n > 0 {
				r.notes = append(r.notes, blue(fmt.Sprintf("stash %d", n)))
			}
			if o := originURL(dir); o == "" {
				r.notes = append(r.notes, dim("no remote"))
			} else if u, err := ParseRepoURL(o, cfg.DefaultHost); err == nil && filepath.Clean(u.LocalPath(cfg.Root)) != filepath.Clean(dir) {
				r.notes = append(r.notes, dim("misplaced → "+u.Key()))
			}
			rows[i] = r
		}(i, dir)
	}
	wg.Wait()
	stop()

	width, bwidth := 0, 0
	for _, r := range rows {
		if f["-a"] || len(r.notes) > 0 {
			width = max(width, len(r.rel))
			bwidth = max(bwidth, len(r.branch))
		}
	}
	attention := 0
	for _, r := range rows {
		if len(r.notes) > 0 {
			attention++
		}
		if !f["-a"] && len(r.notes) == 0 {
			continue
		}
		state := green("✓")
		if len(r.notes) > 0 {
			state = yellow("!")
		}
		fmt.Printf("%s %-*s  %s%*s  %s\n", state, width, r.rel, cyan(r.branch), bwidth-len(r.branch), "", strings.Join(r.notes, dim(" · ")))
	}
	fmt.Printf("\n%d repositories, %d need attention\n", len(rows), attention)
	return nil
}

// gx adopt <dir...>: move existing clones into the tree based on their origin.
func cmdAdopt(args []string) error {
	f, dirs := flags(args, "-n|--dry-run", "-y|--yes")
	if len(dirs) == 0 {
		return fmt.Errorf("usage: gx adopt [-n] [-y] <dir>...   (moves repos found under <dir> into the tree)")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	type move struct{ from, to string }
	var moves []move
	for _, d := range dirs {
		d = expandHome(d)
		for _, repo := range findRepos(d) {
			o := originURL(repo)
			if o == "" {
				fmt.Fprintf(os.Stderr, "%s %s %s\n", dim("skip"), repo, dim("(no remote)"))
				continue
			}
			u, err := ParseRepoURL(o, cfg.DefaultHost)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s %s %s\n", dim("skip"), repo, dim("("+err.Error()+")"))
				continue
			}
			to := u.LocalPath(cfg.Root)
			if filepath.Clean(to) == filepath.Clean(repo) {
				continue
			}
			if _, err := os.Stat(to); err == nil {
				fmt.Fprintf(os.Stderr, "%s %s %s\n", yellow("skip"), repo, yellow("(target exists: "+to+")"))
				continue
			}
			moves = append(moves, move{repo, to})
		}
	}
	if len(moves) == 0 {
		fmt.Fprintln(os.Stderr, "nothing to move")
		return nil
	}
	for _, m := range moves {
		fmt.Printf("%s\n  %s %s\n", m.from, green("→"), m.to)
	}
	if f["-n"] {
		return nil
	}
	if !f["-y"] && !confirm(fmt.Sprintf("move %d repositories?", len(moves))) {
		return errCancelled
	}
	for _, m := range moves {
		if err := os.MkdirAll(filepath.Dir(m.to), 0o755); err != nil {
			return err
		}
		if err := os.Rename(m.from, m.to); err != nil {
			return fmt.Errorf("moving %s: %w", m.from, err)
		}
	}
	fmt.Fprintf(os.Stderr, "%s moved %d repositories\n", green("✓"), len(moves))
	return nil
}

// gx doctor: check git, config and every provider's credentials.
func cmdDoctor(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ok := func(s string) { fmt.Printf("%s %s\n", green("✓"), s) }
	bad := func(s string) { fmt.Printf("%s %s\n", red("✗"), s) }

	if v, err := gitOut("", "--version"); err == nil {
		ok(v)
	} else {
		bad("git not found in PATH")
	}
	if _, err := os.Stat(cfg.path); err == nil {
		ok("config " + cfg.path)
	} else {
		fmt.Printf("%s no config at %s (defaults in use, run `gx config` to create one)\n", yellow("!"), cfg.path)
	}
	ok("root " + cfg.Root)
	if os.Getenv("GX_CD_FILE") != "" {
		ok("shell integration active")
	} else {
		fmt.Printf("%s shell integration not active (gx can't cd for you; see `gx shell`)\n", yellow("!"))
	}

	var wg sync.WaitGroup
	results := make([]string, len(cfg.Providers))
	for i, p := range cfg.Providers {
		wg.Add(1)
		go func(i int, p ProviderConfig) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			start := time.Now()
			rs, method, err := p.list(ctx)
			if err != nil {
				via := ""
				if method != "" {
					via = " (auth " + method + ")"
				}
				results[i] = red("✗") + " " + p.Label() + via + ": " + err.Error()
				return
			}
			results[i] = fmt.Sprintf("%s %s: %d repositories (auth %s, clone %s, %s)", green("✓"), p.Label(), len(rs),
				method, p.Protocol, time.Since(start).Round(10*time.Millisecond))
		}(i, p)
	}
	wg.Wait()
	for _, r := range results {
		fmt.Println(r)
	}
	return nil
}
