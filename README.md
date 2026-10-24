# gx

git, shorter. A single binary that

- browses your repositories on **GitHub, GitLab, Bitbucket (Cloud & Server/DC) and Azure DevOps (Services & Server)** in a fuzzy picker,
- clones them into one predictable tree derived from the remote URL,
- wraps everyday git commands in one- or two-letter commands with nicer output.

```
~/Projects/github.com/hallownet-dev/gx
~/Projects/gitlab.client.com/group/subgroup/repo
~/Projects/devops.client.com/DefaultCollection/Project/repo
~/Projects/bitbucket.client.com/PROJ/repo
```

The same URL always maps to the same path, on every machine and OS.

## Install

```sh
# needs GOPRIVATE while the repo is private
GOPRIVATE=github.com/hallownet-dev go install github.com/hallownet-dev/gx@latest
```

or download a binary from Releases (built by GoReleaser on every `v*` tag).

Then enable shell integration so `gx l`, `gx j` and `gx cl` can change your directory:

| shell | add to | line |
|---|---|---|
| bash / zsh | `~/.bashrc` / `~/.zshrc` | `eval "$(gx shell bash)"` |
| fish | `~/.config/fish/config.fish` | `gx shell fish \| source` |
| PowerShell | `$PROFILE` | `gx shell pwsh \| Out-String \| iex` |

## Commands

```
repositories
  l  [query]       pick remote repos (tab = multi) → clone, or pull if cloned
  j  [query]       jump to a local repository
  cl <url|o/r>     clone into <root>/<host>/<path>
  o  [url|dir]     open the repository in the browser
  ov               overview of local repos needing attention (-a all, -f fetch)
  ls               list local repositories
  adopt <dir>...   move existing clones into the tree (-n dry run)

working
  s                status
  a  [paths]       stage (interactive picker without paths)
  c  [-a] [msg]    commit (message needs no quotes; picks files if nothing staged)
  p  [-f]          push (sets upstream on first push; -f = --force-with-lease)
  u / f            pull / fetch --all --prune
  b  [name]        switch branch (picker; creates if missing)
  d / ds / lg      diff / diff --staged / short graph log
```

Anything else goes straight to git: `gx rebase -i main`. `gx` alone is `gx s`.

## Configuration

`gx config` creates `~/.config/gx/config.toml` (same location on every OS; override with `GX_CONFIG`). Without a config, gx uses GitHub via `gh auth token`.

```toml
root = "~/Projects"
default_host = "github.com"
protocol = "ssh"            # ssh | https
cache_ttl = "15m"

[[provider]]
type = "github"             # token falls back to `gh auth token`
owners = ["hallownet-dev"]  # optional filter

[[provider]]
name = "client-gitlab"
type = "gitlab"
host = "gitlab.client.com"
token_env = "CLIENT_GITLAB_TOKEN"

[[provider]]
name = "client-azure"
type = "azure"
url = "https://devops.client.com/tfs/DefaultCollection"
token_cmd = "pass show client/azure-pat"
protocol = "https"

[[provider]]
type = "bitbucket"
host = "bitbucket.client.com"   # bitbucket.org = cloud, anything else = Server/DC
token_env = "BB_TOKEN"          # HTTP access token (Bearer); set username for basic auth
```

Tokens come from `token`, `token_env` or `token_cmd`; keep secrets out of the file by using the latter two. `gx doctor` checks every provider.

| provider | token scope |
|---|---|
| GitHub | `repo`, `read:org` |
| GitLab | `read_api` |
| Bitbucket Cloud | API token / app password with *Repositories: Read*; `username` = Atlassian email or username |
| Bitbucket Server/DC | HTTP access token with repository read |
| Azure DevOps | PAT with *Code (Read)* |

## Path rules

| remote | path |
|---|---|
| `git@github.com:org/repo.git` | `github.com/org/repo` |
| `https://gitlab.com/a/b/c/repo/-/tree/main` | `gitlab.com/a/b/c/repo` |
| `https://dev.azure.com/org/proj/_git/repo`, `git@ssh.dev.azure.com:v3/org/proj/repo` | `dev.azure.com/org/proj/repo` |
| `https://host/tfs/Collection/Proj/_git/repo` | `host/Collection/Proj/repo` |
| `https://host/scm/proj/repo.git`, `ssh://git@host:7999/proj/repo.git` | `host/proj/repo` |

## Development

```sh
go test ./...
go build -o gx .
```
