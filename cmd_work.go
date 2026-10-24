package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func branchLine(s *repoStatus, stash int) string {
	head := s.Head
	if s.Initial {
		head += dim(" (no commits)")
	}
	line := bold(cyan(head))
	if s.Upstream != "" {
		line += dim(" → ") + s.Upstream
		if s.Ahead > 0 {
			line += " " + green(fmt.Sprintf("↑%d", s.Ahead))
		}
		if s.Behind > 0 {
			line += " " + red(fmt.Sprintf("↓%d", s.Behind))
		}
		if s.Ahead == 0 && s.Behind == 0 {
			line += " " + dim("≡")
		}
	} else if s.Head != "(detached)" {
		line += " " + magenta("(no upstream)")
	}
	if stash > 0 {
		line += "  " + blue(fmt.Sprintf("stash %d", stash))
	}
	return line
}

func writeChanges(b *strings.Builder, s *repoStatus, limit int) {
	if s.Clean() {
		b.WriteString(green("✓ clean") + "\n")
		return
	}
	section := func(title string, color func(string) string, list []change) {
		if len(list) == 0 {
			return
		}
		fmt.Fprintf(b, "%s %s\n", bold(title), dim(fmt.Sprintf("(%d)", len(list))))
		for i, c := range list {
			if limit > 0 && i == limit {
				fmt.Fprintf(b, "   %s\n", dim(fmt.Sprintf("… %d more", len(list)-limit)))
				break
			}
			p := c.Path
			if c.Orig != "" {
				p = c.Orig + dim(" → ") + c.Path
			}
			fmt.Fprintf(b, "   %s  %s\n", color(string(c.Code)), p)
		}
	}
	section("conflicts", yellow, s.Conflicts)
	section("staged", green, s.Staged)
	section("changes", red, s.Unstaged)
	section("untracked", magenta, s.Untracked)
}

// gx s: compact status.
func cmdStatus(args []string) error {
	top, err := repoTop("")
	if err != nil {
		return err
	}
	s, err := readStatus(top)
	if err != nil {
		return err
	}
	name := filepath.Base(top)
	if cfg, err := loadConfig(); err == nil {
		name = relTo(cfg.Root, top)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", dim(name))
	line := branchLine(s, stashCount(top))
	if gd, err := gitOut(top, "rev-parse", "--absolute-git-dir"); err == nil {
		if op := operation(gd); op != "" {
			line += "  " + bold(yellow(op))
		}
	}
	b.WriteString(line + "\n")
	if last, err := gitOut(top, "log", "-1", "--format=%h%x00%s%x00%cr"); err == nil && last != "" {
		p := strings.SplitN(last, "\x00", 3)
		if len(p) == 3 {
			fmt.Fprintf(&b, "%s %s %s\n", yellow(p[0]), p[1], dim("· "+p[2]))
		}
	}
	b.WriteString("\n")
	writeChanges(&b, s, 0)
	fmt.Print(b.String())
	return nil
}

// pickChanges lets the user choose unstaged/untracked files.
func pickChanges(s *repoStatus, header string) ([]string, error) {
	var items []change
	items = append(items, s.Conflicts...)
	items = append(items, s.Unstaged...)
	items = append(items, s.Untracked...)
	if len(items) == 0 {
		return nil, fmt.Errorf("no changes to stage")
	}
	idx, err := pick(len(items), func(i int) string {
		return string(items[i].Code) + "  " + items[i].Path
	}, true, "", header, func(i int) string {
		c := items[i]
		var out string
		if c.Code == '?' {
			out, _ = gitOut("", "diff", "--no-index", "--color=always", "--", os.DevNull, c.Path)
		} else {
			out, _ = gitOut("", "diff", "--color=always", "--", c.Path)
		}
		if out == "" {
			return dim("(no textual diff)")
		}
		return out
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	for _, i := range idx {
		if !seen[items[i].Path] {
			seen[items[i].Path] = true
			paths = append(paths, items[i].Path)
		}
	}
	return paths, nil
}

// gx a [paths]: stage files, picking interactively when no paths are given.
func cmdAdd(args []string) error {
	if len(args) > 0 {
		return git(append([]string{"add"}, args...)...)
	}
	top, err := repoTop("")
	if err != nil {
		return err
	}
	if err := os.Chdir(top); err != nil {
		return err
	}
	s, err := readStatus(top)
	if err != nil {
		return err
	}
	paths, err := pickChanges(s, "tab select · enter stage")
	if err != nil {
		return err
	}
	return git(append([]string{"add", "--"}, paths...)...)
}

// gx c [-a] [--amend] [message words...]
func cmdCommit(args []string) error {
	f, words := flags(args, "-a|--all", "--amend", "-n|--no-verify")
	top, err := repoTop("")
	if err != nil {
		return err
	}
	if err := os.Chdir(top); err != nil {
		return err
	}
	s, err := readStatus(top)
	if err != nil {
		return err
	}
	if len(s.Conflicts) > 0 {
		return fmt.Errorf("resolve %d conflict(s) first", len(s.Conflicts))
	}
	if len(s.Staged) == 0 && !f["--amend"] {
		switch {
		case f["-a"]:
			if s.Clean() {
				return fmt.Errorf("nothing to commit")
			}
			if err := git("add", "-A"); err != nil {
				return err
			}
		case s.Clean():
			return fmt.Errorf("nothing to commit")
		default:
			paths, err := pickChanges(s, "nothing staged · tab select files to commit · enter confirm")
			if err != nil {
				return err
			}
			if err := git(append([]string{"add", "--"}, paths...)...); err != nil {
				return err
			}
		}
	} else if f["-a"] {
		if err := git("add", "-A"); err != nil {
			return err
		}
	}
	cargs := []string{"commit"}
	if f["--amend"] {
		cargs = append(cargs, "--amend")
		if len(words) == 0 {
			cargs = append(cargs, "--no-edit")
		}
	}
	if f["-n"] {
		cargs = append(cargs, "--no-verify")
	}
	if len(words) > 0 {
		cargs = append(cargs, "-m", strings.Join(words, " "))
	}
	return git(cargs...)
}

// gx p: push, setting upstream on first push; -f means --force-with-lease.
func cmdPush(args []string) error {
	var pass []string
	for _, a := range args {
		if a == "-f" || a == "--force" {
			a = "--force-with-lease"
		}
		pass = append(pass, a)
	}
	if _, err := gitOut("", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err != nil {
		remote := "origin"
		if remotes, _ := gitOut("", "remote"); remotes != "" && !strings.Contains("\n"+remotes+"\n", "\norigin\n") {
			remote = strings.Fields(remotes)[0]
		}
		return git(append([]string{"push", "-u", remote, "HEAD"}, pass...)...)
	}
	return git(append([]string{"push"}, pass...)...)
}

type branchItem struct {
	name, short, when, subject string
	remote, current            bool
}

// gx b [name]: switch branch, picking interactively without a name; creates
// the branch when it doesn't exist locally or on a remote.
func cmdBranch(args []string) error {
	if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		return git(append([]string{"branch"}, args...)...)
	}
	current, _ := gitOut("", "branch", "--show-current")
	out, err := gitOut("", "for-each-ref", "--sort=-committerdate",
		"--format=%(refname)%00%(refname:short)%00%(committerdate:relative)%00%(subject)", "refs/heads", "refs/remotes")
	if err != nil {
		return err
	}
	var items []branchItem
	localNames := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		p := strings.SplitN(line, "\x00", 4)
		if len(p) < 4 || strings.HasSuffix(p[0], "/HEAD") {
			continue
		}
		it := branchItem{name: p[0], short: p[1], when: p[2], subject: p[3], remote: strings.HasPrefix(p[0], "refs/remotes/")}
		if !it.remote {
			localNames[it.short] = true
			it.current = it.short == current
		}
		items = append(items, it)
	}
	remoteLocal := func(it branchItem) string {
		_, after, _ := strings.Cut(it.short, "/")
		return after
	}
	var list []branchItem
	for _, it := range items {
		if it.remote && (localNames[remoteLocal(it)] || it.short == strings.SplitN(it.short, "/", 2)[0]) {
			continue
		}
		list = append(list, it)
	}

	if len(args) > 0 {
		name := args[0]
		if localNames[name] {
			return git("switch", name)
		}
		for _, it := range list {
			if it.remote && remoteLocal(it) == name {
				return git("switch", "-c", name, "--track", it.short)
			}
		}
		return git("switch", "-c", name)
	}

	width := 0
	for _, it := range list {
		if len(it.short) > width {
			width = len(it.short)
		}
	}
	idx, err := pick(len(list), func(i int) string {
		it := list[i]
		mark := "  "
		if it.current {
			mark = "* "
		}
		return fmt.Sprintf("%s%-*s  %s", mark, width, it.short, it.when)
	}, false, "", "enter switch · remote branches get a tracking branch", func(i int) string {
		it := list[i]
		log, _ := gitOut("", "log", "--color=always", "--format=%C(yellow)%h%C(reset) %s %C(dim)· %an, %cr%C(reset)", "-25", it.name)
		return bold(it.short) + "\n\n" + log
	})
	if err != nil {
		return err
	}
	it := list[idx[0]]
	if it.current {
		return nil
	}
	if it.remote {
		return git("switch", "-c", remoteLocal(it), "--track", it.short)
	}
	return git("switch", it.short)
}
