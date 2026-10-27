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
# needs GOPRIVATE while the repo is private; `go env -w` works in any shell (bash, zsh, fish, pwsh)
go env -w GOPRIVATE=github.com/hallownet-dev
go install github.com/hallownet-dev/gx@latest
```

For a one-off without changing your Go config: `GOPRIVATE=github.com/hallownet-dev go install …` in bash/zsh, or `$env:GOPRIVATE = "github.com/hallownet-dev"; go install …` in PowerShell.

Or download a binary from [Releases](https://github.com/hallownet-dev/gx/releases) (Linux and Windows, amd64/arm64), rename it to `gx` / `gx.exe` and put it on your PATH. `checksums.txt` lists the SHA-256 of each.

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
auth = "ssh"                    # token minted with your SSH key

[[provider]]
name = "client-azure"
type = "azure"
url = "https://devops.client.com/tfs/DefaultCollection"
protocol = "https"              # no token: uses Git Credential Manager

[[provider]]
type = "bitbucket"
host = "bitbucket.client.com"   # bitbucket.org = cloud, anything else = Server/DC
token_env = "BB_TOKEN"          # or omit to use the git credential helper
```

### Authentication

You usually don't need to create tokens. Without `token`, `token_env` or `token_cmd`, gx reuses logins you already have, trying in order:

| provider | tried automatically |
|---|---|
| GitHub | `gh auth token` → git credential helper |
| GitLab | `glab` → git credential helper |
| Azure DevOps | git credential helper → `az account get-access-token` (dev.azure.com only) |
| Bitbucket | git credential helper |

**git credential helper** means whatever git itself uses for HTTPS: Git Credential Manager, macOS Keychain, libsecret, `pass`… If you have cloned from a host over HTTPS once, gx can list its repos. Prompts are disabled, so a missing credential fails fast instead of hanging.

Pin a method with `auth = "token" | "gh" | "glab" | "az" | "az-devops" | "git-credential"`.

**Azure DevOps CLI:** `auth = "az-devops"` lists repos with `az repos list --organization <url>`, so the session from `az devops login` is reused (cloud and Server). It's never tried automatically. Several organizations or collections on one server = one `[[provider]]` per `url`.

**GitLab over SSH:** `auth = "ssh"` creates a 7-day `read_api` token with your SSH key (`ssh git@host personal_access_token gx-<machine> read_api <date>`, GitLab 14.2+), caches it in the user cache dir (`0600`) and renews it a day before expiry or when revoked. Set `ssh = "ssh://git@host:2222"` if SSH isn't `git@<host>:22`. The tokens show up in GitLab under *Access tokens* as `gx-<machine>`.

`gx doctor` shows which method each provider used, or why each one failed.

If you do use tokens, keep them out of the file with `token_env` or `token_cmd`:

| provider | token scope |
|---|---|
| GitHub | `repo`, `read:org` |
| GitLab | `read_api` |
| Bitbucket Cloud | API token / app password with *Repositories: Read*, plus `username` (Atlassian email or username) |
| Bitbucket Server/DC | HTTP access token with repository read (`username` optional) |
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

## Releasing

Work happens on branches. A PR runs vet, test and build on Linux, macOS and Windows; after merge the same runs again on `main`.

To publish binaries, tag a commit on `main` with a [semver](https://semver.org) version and push the tag:

```sh
git tag v0.2.0 && git push origin v0.2.0
```

GoReleaser then creates the GitHub release with the binaries and a commit list as notes.
