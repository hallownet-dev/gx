package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// gitOut runs git in dir and returns trimmed stdout.
func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// attached runs a command with the terminal attached and propagates its exit code.
func attached(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return exitCode(ee.ExitCode())
	}
	return err
}

func git(args ...string) error { return attached("", "git", args...) }

type change struct {
	Code byte // M A D R C T U ?
	Path string
	Orig string // rename source
}

type repoStatus struct {
	Head      string
	Upstream  string
	Ahead     int
	Behind    int
	Staged    []change
	Unstaged  []change
	Untracked []change
	Conflicts []change
	Initial   bool
}

func (s *repoStatus) Clean() bool {
	return len(s.Staged)+len(s.Unstaged)+len(s.Untracked)+len(s.Conflicts) == 0
}

func (s *repoStatus) Dirty() int {
	return len(s.Staged) + len(s.Unstaged) + len(s.Untracked) + len(s.Conflicts)
}

func readStatus(dir string) (*repoStatus, error) {
	out, err := gitOut(dir, "status", "--porcelain=v2", "--branch", "-z")
	if err != nil {
		return nil, err
	}
	s := &repoStatus{}
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		r := recs[i]
		if len(r) < 2 {
			continue
		}
		switch r[0] {
		case '#':
			f := strings.Fields(r)
			if len(f) < 3 {
				continue
			}
			switch f[1] {
			case "branch.oid":
				s.Initial = f[2] == "(initial)"
			case "branch.head":
				s.Head = f[2]
			case "branch.upstream":
				s.Upstream = f[2]
			case "branch.ab":
				if len(f) >= 4 {
					s.Ahead, _ = strconv.Atoi(strings.TrimPrefix(f[2], "+"))
					s.Behind, _ = strconv.Atoi(strings.TrimPrefix(f[3], "-"))
				}
			}
		case '1', '2':
			n := 9
			if r[0] == '2' {
				n = 10
			}
			parts := strings.SplitN(r, " ", n)
			if len(parts) < n {
				continue
			}
			c := change{Path: parts[n-1]}
			if r[0] == '2' && i+1 < len(recs) {
				c.Orig = recs[i+1]
				i++
			}
			x, y := parts[1][0], parts[1][1]
			if x != '.' {
				cx := c
				cx.Code = x
				s.Staged = append(s.Staged, cx)
			}
			if y != '.' {
				cy := c
				cy.Code = y
				cy.Orig = ""
				s.Unstaged = append(s.Unstaged, cy)
			}
		case 'u':
			parts := strings.SplitN(r, " ", 11)
			if len(parts) == 11 {
				s.Conflicts = append(s.Conflicts, change{Code: 'U', Path: parts[10]})
			}
		case '?':
			s.Untracked = append(s.Untracked, change{Code: '?', Path: r[2:]})
		}
	}
	return s, nil
}

func stashCount(dir string) int {
	out, err := gitOut(dir, "rev-list", "--walk-reflogs", "--count", "refs/stash")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
}

// operation reports an in-progress merge/rebase/etc.
func operation(gitDir string) string {
	exists := func(p string) bool { _, err := os.Stat(filepath.Join(gitDir, p)); return err == nil }
	switch {
	case exists("rebase-merge"), exists("rebase-apply"):
		return "REBASING"
	case exists("MERGE_HEAD"):
		return "MERGING"
	case exists("CHERRY_PICK_HEAD"):
		return "CHERRY-PICKING"
	case exists("REVERT_HEAD"):
		return "REVERTING"
	case exists("BISECT_LOG"):
		return "BISECTING"
	}
	return ""
}

func repoTop(dir string) (string, error) {
	top, err := gitOut(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository")
	}
	return filepath.FromSlash(top), nil
}

func originURL(dir string) string {
	u, _ := gitOut(dir, "config", "--get", "remote.origin.url")
	if u == "" {
		// Fall back to the first remote.
		if remotes, _ := gitOut(dir, "remote"); remotes != "" {
			u, _ = gitOut(dir, "remote", "get-url", strings.Fields(remotes)[0])
		}
	}
	return u
}

// findRepos walks root and returns every directory containing .git. It does
// not descend into repositories.
func findRepos(root string) []string {
	var repos []string
	root = filepath.Clean(root)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
			repos = append(repos, p)
			return filepath.SkipDir
		}
		name := d.Name()
		if p != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
			return filepath.SkipDir
		}
		if rel, _ := filepath.Rel(root, p); strings.Count(rel, string(filepath.Separator)) >= 8 {
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(repos)
	return repos
}

func relTo(root, p string) string {
	if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return p
}
