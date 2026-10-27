package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Remote is a repository as reported by a provider API.
type Remote struct {
	Name     string    `json:"name"` // display name from the provider
	Desc     string    `json:"desc,omitempty"`
	SSH      string    `json:"ssh,omitempty"`
	HTTPS    string    `json:"https,omitempty"`
	Web      string    `json:"web,omitempty"`
	Updated  time.Time `json:"updated,omitempty"`
	Private  bool      `json:"private,omitempty"`
	Archived bool      `json:"archived,omitempty"`
	Fork     bool      `json:"fork,omitempty"`
	Branch   string    `json:"branch,omitempty"`

	// Derived on load, not cached.
	Provider string  `json:"-"`
	Clone    string  `json:"-"`
	URL      RepoURL `json:"-"`
}

// list returns the provider's repositories and the auth method that worked.
func (p ProviderConfig) list(ctx context.Context) ([]Remote, string, error) {
	cred, err := p.credential(ctx)
	if err != nil {
		return nil, "", err
	}
	out, err := p.listWith(ctx, cred)
	var se *statusError
	if errors.As(err, &se) && se.code == http.StatusUnauthorized && cred.Method == "ssh" {
		// The cached SSH-minted token was revoked; mint a new one once.
		if cred, err = p.credentialVia(ctx, "ssh", true); err == nil {
			out, err = p.listWith(ctx, cred)
		}
	}
	if err != nil {
		return nil, cred.Method, err
	}
	if !p.Archived {
		kept := out[:0]
		for _, r := range out {
			if !r.Archived {
				kept = append(kept, r)
			}
		}
		out = kept
	}
	return out, cred.Method, nil
}

func (p ProviderConfig) listWith(ctx context.Context, cred credential) ([]Remote, error) {
	c := newAPIClient(p, cred)
	switch p.Type {
	case "github":
		return c.github(ctx)
	case "gitlab":
		return c.gitlab(ctx)
	case "bitbucket":
		if p.Host == "bitbucket.org" {
			return c.bitbucketCloud(ctx)
		}
		return c.bitbucketServer(ctx)
	case "azure":
		if cred.Method == "az-devops" {
			return c.azureCLI(ctx)
		}
		return c.azure(ctx)
	}
	return nil, fmt.Errorf("unknown provider type %q", p.Type)
}

type apiClient struct {
	p    ProviderConfig
	http *http.Client
	// schemes are the ways to present the credential, tried in order; a 401
	// moves on to the next one for the rest of the session.
	schemes []func(*http.Request)
	scheme  int
}

func newAPIClient(p ProviderConfig, cred credential) *apiClient {
	c := &apiClient{p: p, http: &http.Client{Timeout: 30 * time.Second}}
	if p.Insecure {
		c.http.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+cred.Secret) }
	basic := func(r *http.Request) { r.SetBasicAuth(cred.User, cred.Secret) }
	switch {
	case p.Type == "github", p.Type == "gitlab", cred.Method == "az":
		// PATs and OAuth tokens alike are accepted as Bearer.
		c.schemes = []func(*http.Request){bearer}
	case p.Type == "azure", cred.User != "":
		// PATs and passwords go as Basic; OAuth tokens from a credential
		// manager need Bearer.
		c.schemes = []func(*http.Request){basic, bearer}
	default:
		c.schemes = []func(*http.Request){bearer}
	}
	return c
}

type statusError struct {
	code int
	msg  string
}

func (e *statusError) Error() string { return e.msg }

func (c *apiClient) get(ctx context.Context, u string, v any) (http.Header, error) {
	for {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "gx/"+version)
		c.schemes[c.scheme](req)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && c.scheme+1 < len(c.schemes) {
			resp.Body.Close()
			c.scheme++
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
			msg := strings.TrimSpace(string(body))
			if strings.Contains(resp.Header.Get("Content-Type"), "html") {
				msg = ""
			}
			return nil, &statusError{resp.StatusCode, fmt.Sprintf("%s: %s %s", redactURL(u), resp.Status, msg)}
		}
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			return nil, fmt.Errorf("%s: decoding response: %w", redactURL(u), err)
		}
		return resp.Header, nil
	}
}

func redactURL(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return u
	}
	pu.RawQuery = ""
	return pu.String()
}

func (c *apiClient) ownerOK(owner string) bool {
	if len(c.p.Owners) == 0 {
		return true
	}
	for _, o := range c.p.Owners {
		if strings.EqualFold(owner, o) || strings.HasPrefix(strings.ToLower(owner), strings.ToLower(o)+"/") {
			return true
		}
	}
	return false
}

var linkNext = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func (c *apiClient) github(ctx context.Context) ([]Remote, error) {
	base := c.p.URL
	if base == "" {
		base = "https://api.github.com"
		if c.p.Host != "github.com" {
			base = "https://" + c.p.Host + "/api/v3"
		}
	}
	next := base + "/user/repos?per_page=100&sort=pushed&affiliation=owner,collaborator,organization_member"
	var out []Remote
	for next != "" {
		var page []struct {
			FullName    string    `json:"full_name"`
			Description string    `json:"description"`
			SSHURL      string    `json:"ssh_url"`
			CloneURL    string    `json:"clone_url"`
			HTMLURL     string    `json:"html_url"`
			PushedAt    time.Time `json:"pushed_at"`
			Private     bool      `json:"private"`
			Archived    bool      `json:"archived"`
			Fork        bool      `json:"fork"`
			Branch      string    `json:"default_branch"`
			Owner       struct {
				Login string `json:"login"`
			} `json:"owner"`
		}
		h, err := c.get(ctx, next, &page)
		if err != nil {
			return nil, err
		}
		for _, r := range page {
			if !c.ownerOK(r.Owner.Login) {
				continue
			}
			out = append(out, Remote{Name: r.FullName, Desc: r.Description, SSH: r.SSHURL, HTTPS: r.CloneURL,
				Web: r.HTMLURL, Updated: r.PushedAt, Private: r.Private, Archived: r.Archived, Fork: r.Fork, Branch: r.Branch})
		}
		next = ""
		if m := linkNext.FindStringSubmatch(h.Get("Link")); m != nil {
			next = m[1]
		}
	}
	return out, nil
}

func (c *apiClient) gitlab(ctx context.Context) ([]Remote, error) {
	base := c.p.URL
	if base == "" {
		base = "https://" + c.p.Host
	}
	q := "membership=true&simple=true&per_page=100&order_by=last_activity_at"
	if !c.p.Archived {
		q += "&archived=false"
	}
	var out []Remote
	for page := "1"; page != ""; {
		var items []struct {
			Path        string    `json:"path_with_namespace"`
			Description string    `json:"description"`
			SSH         string    `json:"ssh_url_to_repo"`
			HTTP        string    `json:"http_url_to_repo"`
			Web         string    `json:"web_url"`
			Activity    time.Time `json:"last_activity_at"`
			Branch      string    `json:"default_branch"`
			Archived    bool      `json:"archived"`
		}
		h, err := c.get(ctx, base+"/api/v4/projects?"+q+"&page="+page, &items)
		if err != nil {
			return nil, err
		}
		for _, r := range items {
			if !c.ownerOK(r.Path) {
				continue
			}
			out = append(out, Remote{Name: r.Path, Desc: r.Description, SSH: r.SSH, HTTPS: r.HTTP,
				Web: r.Web, Updated: r.Activity, Branch: r.Branch, Archived: r.Archived})
		}
		page = h.Get("X-Next-Page")
	}
	return out, nil
}

type bbLink struct {
	Href string `json:"href"`
	Name string `json:"name"`
}

func bbClone(links []bbLink) (ssh, https string) {
	for _, l := range links {
		switch l.Name {
		case "ssh":
			ssh = l.Href
		case "http", "https":
			https = l.Href
		}
	}
	return
}

func (c *apiClient) bitbucketCloud(ctx context.Context) ([]Remote, error) {
	base := c.p.URL
	if base == "" {
		base = "https://api.bitbucket.org"
	}
	next := base + "/2.0/repositories?role=member&pagelen=100&sort=-updated_on"
	var out []Remote
	for next != "" {
		var page struct {
			Next   string `json:"next"`
			Values []struct {
				FullName    string    `json:"full_name"`
				Description string    `json:"description"`
				Updated     time.Time `json:"updated_on"`
				Private     bool      `json:"is_private"`
				Parent      *struct{} `json:"parent"`
				Main        *struct {
					Name string `json:"name"`
				} `json:"mainbranch"`
				Links struct {
					Clone []bbLink `json:"clone"`
					HTML  bbLink   `json:"html"`
				} `json:"links"`
			} `json:"values"`
		}
		if _, err := c.get(ctx, next, &page); err != nil {
			return nil, err
		}
		for _, r := range page.Values {
			if !c.ownerOK(r.FullName) {
				continue
			}
			ssh, https := bbClone(r.Links.Clone)
			rem := Remote{Name: r.FullName, Desc: r.Description, SSH: ssh, HTTPS: https, Web: r.Links.HTML.Href,
				Updated: r.Updated, Private: r.Private, Fork: r.Parent != nil}
			if r.Main != nil {
				rem.Branch = r.Main.Name
			}
			out = append(out, rem)
		}
		next = page.Next
	}
	return out, nil
}

func (c *apiClient) bitbucketServer(ctx context.Context) ([]Remote, error) {
	base := c.p.URL
	if base == "" {
		base = "https://" + c.p.Host
	}
	var out []Remote
	for start, last := 0, false; !last; {
		var page struct {
			IsLastPage    bool `json:"isLastPage"`
			NextPageStart int  `json:"nextPageStart"`
			Values        []struct {
				Slug        string    `json:"slug"`
				Description string    `json:"description"`
				Public      bool      `json:"public"`
				Origin      *struct{} `json:"origin"`
				Project     struct {
					Key string `json:"key"`
				} `json:"project"`
				Links struct {
					Clone []bbLink `json:"clone"`
					Self  []bbLink `json:"self"`
				} `json:"links"`
			} `json:"values"`
		}
		if _, err := c.get(ctx, base+"/rest/api/1.0/repos?limit=500&start="+strconv.Itoa(start), &page); err != nil {
			return nil, err
		}
		for _, r := range page.Values {
			if !c.ownerOK(r.Project.Key) {
				continue
			}
			ssh, https := bbClone(r.Links.Clone)
			rem := Remote{Name: r.Project.Key + "/" + r.Slug, Desc: r.Description, SSH: ssh, HTTPS: https,
				Private: !r.Public, Fork: r.Origin != nil}
			if len(r.Links.Self) > 0 {
				rem.Web = r.Links.Self[0].Href
			}
			out = append(out, rem)
		}
		last, start = page.IsLastPage || len(page.Values) == 0, page.NextPageStart
	}
	return out, nil
}

// azureRepo is a repository as returned by both the REST API and `az repos list`.
type azureRepo struct {
	Name     string `json:"name"`
	Remote   string `json:"remoteUrl"`
	SSH      string `json:"sshUrl"`
	Web      string `json:"webUrl"`
	Branch   string `json:"defaultBranch"`
	Disabled bool   `json:"isDisabled"`
	Project  struct {
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
	} `json:"project"`
}

func (c *apiClient) azure(ctx context.Context) ([]Remote, error) {
	var resp struct {
		Value []azureRepo `json:"value"`
	}
	// 5.0 is understood by Azure DevOps Server 2019+ and the cloud service.
	if _, err := c.get(ctx, c.p.URL+"/_apis/git/repositories?api-version=5.0", &resp); err != nil {
		return nil, err
	}
	return c.azureRemotes(resp.Value), nil
}

// azureCLI lists through `az repos list`, reusing the `az devops login` session.
func (c *apiClient) azureCLI(ctx context.Context) ([]Remote, error) {
	out, err := cmdOutput(ctx, nil, "az", "repos", "list", "--organization", c.p.URL, "--detect", "false", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("az repos list: %w", err)
	}
	var repos []azureRepo
	if err := json.Unmarshal([]byte(out), &repos); err != nil {
		return nil, fmt.Errorf("az repos list: %w", err)
	}
	return c.azureRemotes(repos), nil
}

func (c *apiClient) azureRemotes(repos []azureRepo) []Remote {
	var out []Remote
	for _, r := range repos {
		if r.Disabled || !c.ownerOK(r.Project.Name) {
			continue
		}
		out = append(out, Remote{Name: r.Project.Name + "/" + r.Name, SSH: r.SSH, HTTPS: r.Remote, Web: r.Web,
			Branch: strings.TrimPrefix(r.Branch, "refs/heads/"), Private: r.Project.Visibility != "public"})
	}
	return out
}

// ---- cache ----

type cacheEntry struct {
	Fetched time.Time `json:"fetched"`
	Remotes []Remote  `json:"remotes"`
}

func cacheFile() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "gx", "remotes.json")
}

// loadRemotes returns the remote repos of all providers, from cache when
// fresh. Provider failures are reported on stderr and do not fail the call.
func loadRemotes(cfg *Config, refresh bool) []Remote {
	cache := map[string]cacheEntry{}
	if b, err := os.ReadFile(cacheFile()); err == nil {
		_ = json.Unmarshal(b, &cache)
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		changed bool
		results = make([][]Remote, len(cfg.Providers))
	)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pending := 0
	for i, p := range cfg.Providers {
		e, ok := cache[p.ID()]
		if ok && !refresh && time.Since(e.Fetched) < cfg.ttl {
			results[i] = e.Remotes
			continue
		}
		pending++
		wg.Add(1)
		go func(i int, p ProviderConfig, stale cacheEntry, hasStale bool) {
			defer wg.Done()
			rs, _, err := p.list(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s %s: %v\n", yellow("warning"), p.Label(), err)
				if hasStale {
					results[i] = stale.Remotes
				}
				return
			}
			results[i] = rs
			cache[p.ID()] = cacheEntry{Fetched: time.Now(), Remotes: rs}
			changed = true
		}(i, p, e, ok)
	}
	if pending > 0 {
		stop := spinner(fmt.Sprintf("fetching repositories from %d provider(s)", pending))
		wg.Wait()
		stop()
	}
	if changed {
		if b, err := json.Marshal(cache); err == nil {
			_ = os.MkdirAll(filepath.Dir(cacheFile()), 0o755)
			_ = os.WriteFile(cacheFile(), b, 0o600)
		}
	}

	var all []Remote
	seen := map[string]bool{}
	for i, rs := range results {
		p := cfg.Providers[i]
		for _, r := range rs {
			r.Provider = p.Label()
			r.Clone = r.SSH
			if p.Protocol == "https" || r.Clone == "" {
				r.Clone = r.HTTPS
			}
			u, err := ParseRepoURL(r.Clone, cfg.DefaultHost)
			if err != nil {
				continue
			}
			r.URL = u
			if seen[u.Key()] {
				continue
			}
			seen[u.Key()] = true
			all = append(all, r)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].Updated.Equal(all[j].Updated) {
			return all[i].Updated.After(all[j].Updated)
		}
		return all[i].URL.Key() < all[j].URL.Key()
	})
	return all
}
