package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// RepoURL is a git remote reduced to the parts that decide where it lives
// locally: <root>/<Host>/<Path...>.
type RepoURL struct {
	Host string   // lowercased, without port or userinfo
	Path []string // e.g. [org repo], [group sub repo], [org project repo]
	Raw  string   // the input, unchanged
	// Rebuilt is true when Raw is not directly cloneable (shorthand or a web
	// page URL) and CloneURL has to be reconstructed from Host and Path.
	Rebuilt bool
}

// Key is the slash-separated tree location, e.g. github.com/org/repo.
func (u RepoURL) Key() string {
	return u.Host + "/" + strings.Join(u.Path, "/")
}

func (u RepoURL) LocalPath(root string) string {
	return filepath.Join(append([]string{root, u.Host}, u.Path...)...)
}

// CloneURL returns Raw when it is cloneable, otherwise a URL built from Host
// and Path using the given protocol ("ssh" or "https").
func (u RepoURL) CloneURL(protocol string) string {
	if !u.Rebuilt {
		return u.Raw
	}
	p := strings.Join(u.Path, "/")
	if protocol == "https" {
		return "https://" + u.Host + "/" + p + ".git"
	}
	return "git@" + u.Host + ":" + p + ".git"
}

// ParseRepoURL understands scp-like (git@host:path), ssh://, https://, git://
// URLs, browser URLs of common hosts, and shorthands "owner/repo" (resolved
// against defaultHost) and "host/owner/repo".
//
// Provider-specific noise is dropped so every URL form of the same repo maps
// to the same path:
//
//	Azure DevOps   …/[tfs/]<org|collection>/<project>/_git/<repo>  -> host/org/project/repo
//	               git@ssh.dev.azure.com:v3/<org>/<project>/<repo> -> dev.azure.com/org/project/repo
//	Bitbucket DC   …/scm/<project>/<repo>.git                      -> host/project/repo
//	GitLab         …/<group>/<sub…>/<repo>[/-/…]                    -> host/group/sub…/repo
func ParseRepoURL(raw, defaultHost string) (RepoURL, error) {
	in := strings.TrimSpace(raw)
	if in == "" {
		return RepoURL{}, fmt.Errorf("empty repository URL")
	}
	u := RepoURL{Raw: in}
	var p string

	switch {
	case strings.Contains(in, "://"):
		pu, err := url.Parse(in)
		if err != nil {
			return RepoURL{}, fmt.Errorf("invalid URL %q: %w", in, err)
		}
		u.Host = pu.Hostname()
		p = pu.Path
	case isSCPLike(in):
		hostPart, pathPart, _ := strings.Cut(in, ":")
		if i := strings.LastIndex(hostPart, "@"); i >= 0 {
			hostPart = hostPart[i+1:]
		}
		u.Host = hostPart
		p = pathPart
	default:
		// Shorthand: owner/repo or host/owner/repo.
		u.Rebuilt = true
		segs := splitPath(in)
		if len(segs) >= 3 && strings.Contains(segs[0], ".") {
			u.Host, p = segs[0], strings.Join(segs[1:], "/")
		} else {
			u.Host, p = defaultHost, in
		}
	}

	u.Host = strings.ToLower(u.Host)
	segs := splitPath(p)
	if len(segs) > 0 {
		segs[len(segs)-1] = strings.TrimSuffix(segs[len(segs)-1], ".git")
	}

	// Azure DevOps cloud SSH and legacy visualstudio.com hosts.
	if u.Host == "ssh.dev.azure.com" || u.Host == "vs-ssh.visualstudio.com" {
		u.Host = "dev.azure.com"
		if len(segs) > 0 && segs[0] == "v3" {
			segs = segs[1:]
		}
	} else if org, ok := strings.CutSuffix(u.Host, ".visualstudio.com"); ok {
		u.Host = "dev.azure.com"
		if len(segs) > 0 && strings.EqualFold(segs[0], "DefaultCollection") {
			segs = segs[1:]
		}
		segs = append([]string{org}, segs...)
	}

	if i := indexOf(segs, "_git"); i >= 0 {
		// Azure DevOps (cloud or server). Drop the optional "tfs" virtual
		// directory, and handle …/<org>/_git/<repo> where project == repo.
		before, after := segs[:i], segs[i+1:]
		if len(before) > 0 && strings.EqualFold(before[0], "tfs") {
			before = before[1:]
		}
		if len(after) > 0 {
			after = after[:1]
		}
		if len(before) == 1 && len(after) == 1 {
			before = append(before, after[0])
		}
		segs = append(append([]string{}, before...), after...)
	} else if i := indexOf(segs, "scm"); i >= 0 && len(segs)-i-1 == 2 {
		// Bitbucket Server/DC HTTP clone URL, possibly under a context path.
		segs = segs[i+1:]
	} else {
		// Browser URLs: cut at GitLab's "/-/" and at known two-level hosts.
		if i := indexOf(segs, "-"); i >= 0 {
			segs = segs[:i]
			u.Rebuilt = true
		}
		if (u.Host == "github.com" || u.Host == "bitbucket.org") && len(segs) > 2 {
			segs = segs[:2]
			u.Rebuilt = true
		}
	}

	if u.Host == "" || len(segs) < 2 {
		return RepoURL{}, fmt.Errorf("cannot derive host/owner/repo from %q", in)
	}
	for _, s := range segs {
		if s == "." || s == ".." {
			return RepoURL{}, fmt.Errorf("unsafe path segment in %q", in)
		}
	}
	u.Path = segs
	return u, nil
}

// WebURL turns a remote URL into a browser URL (best effort).
func WebURL(remote string) string {
	in := strings.TrimSpace(remote)
	if strings.HasPrefix(in, "http://") || strings.HasPrefix(in, "https://") {
		if pu, err := url.Parse(in); err == nil {
			pu.User = nil
			pu.Path = strings.TrimSuffix(pu.Path, ".git")
			return pu.String()
		}
	}
	var host, p string
	if strings.Contains(in, "://") {
		pu, err := url.Parse(in)
		if err != nil {
			return ""
		}
		host, p = pu.Hostname(), pu.Path
	} else if isSCPLike(in) {
		h, rest, _ := strings.Cut(in, ":")
		if i := strings.LastIndex(h, "@"); i >= 0 {
			h = h[i+1:]
		}
		host, p = h, rest
	} else {
		return ""
	}
	segs := splitPath(strings.TrimSuffix(p, ".git"))
	if host == "ssh.dev.azure.com" && len(segs) == 4 && segs[0] == "v3" {
		return "https://dev.azure.com/" + segs[1] + "/" + segs[2] + "/_git/" + segs[3]
	}
	return "https://" + host + "/" + strings.Join(segs, "/")
}

// isSCPLike reports whether s looks like [user@]host:path (no scheme).
func isSCPLike(s string) bool {
	colon := strings.Index(s, ":")
	if colon <= 0 {
		return false
	}
	slash := strings.Index(s, "/")
	// Windows drive letters (C:\…) are not remotes.
	return (slash < 0 || colon < slash) && colon > 1
}

func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func indexOf(ss []string, v string) int {
	for i, s := range ss {
		if s == v {
			return i
		}
	}
	return -1
}
