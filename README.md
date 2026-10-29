# gx

git, shorter. A single binary that

- browses your repositories on **GitHub, GitLab, Bitbucket (Cloud & Server/DC) and Azure DevOps (Services & Server)** in a fuzzy picker,
- clones them into one predictable tree derived from the remote URL,
- wraps everyday git commands in one- or two-letter commands with nicer output.

```
~/Projects/github.com/hallownet-dev/gx
~/Projects/gitlab.example.com/group/subgroup/repo
~/Projects/devops.example.com/DefaultCollection/Project/repo
~/Projects/bitbucket.example.com/PROJ/repo
```

The same URL always maps to the same path, on every machine and OS.

## What it looks like

`gx l` lists the repositories of every configured provider in a fuzzy picker. Type to filter, <kbd>tab</kbd> to select several, <kbd>enter</kbd> to clone them (or pull, if already cloned) and `cd` into the result:

```
                                                       ┌─────────────────────────────────────────────────────┐
                                                       │ acme/api                                            │
                                                       │                                                     │
                                                       │ HTTP API for the shop                               │
                                                       │                                                     │
                                                       │ private                                             │
                                                       │ branch    main                                      │
    github.com/jdoe/advent-of-code                     │ updated   1 day ago                                 │
    github.com/jdoe/dotfiles                           │ provider  github:github.com                         │
    github.com/acme/design-system                      │ clone     git@github.com:acme/api.git               │
    github.com/acme/infra                              │                                                     │
  ● github.com/acme/web                                │ ● ~/Projects/github.com/acme/api                    │
> ● github.com/acme/api                                │                                                     │
  6/6                                                  │                                                     │
  tab select · enter clone/pull · ● = cloned           │                                                     │
❯                                                      └─────────────────────────────────────────────────────┘
```

Cloning by URL or `owner/repo` puts the repo where it belongs, whatever form the URL has:

```console
~$ gx cl acme/api
Cloning into '/home/jane/Projects/github.com/acme/api'...
done.
~/Projects/github.com/acme/api$ gx cl git@gitlab.example.com:platform/infra/terraform.git
Cloning into '/home/jane/Projects/gitlab.example.com/platform/infra/terraform'...
done.
~/Projects/gitlab.example.com/platform/infra/terraform$ gx j api      # picker of local repos, pre-filtered; enter jumps
~/Projects/github.com/acme/api$
```

A day of work in a repo (`gx` alone is `gx s`):

```console
$ gx
github.com/acme/api
main → origin/main ≡
c743f41 feat: add config loader · 2 hours ago

changes (1)
   M  README.md
untracked (2)
   ?  main.go
   ?  notes.txt
$ gx a main.go                 # without paths: pick files in a fuzzy picker
$ gx c add http server         # no quotes needed
[main 54ce762] add http server
 1 file changed, 1 insertion(+)
 create mode 100644 main.go
$ gx p
To github.com:acme/api.git
   c743f41..54ce762  main -> main
$ gx b feat/auth               # switch, or create if it doesn't exist
Switched to a new branch 'feat/auth'
$ gx lg
* 54ce762 (HEAD -> feat/auth, origin/main, origin/HEAD, main) add http server · Jane Doe, 1 minute ago
* c743f41 feat: add config loader · Jane Doe, 2 hours ago
* eecc6b6 initial commit · Jane Doe, 3 days ago
$ gx rebase -i main            # anything gx doesn't know goes straight to git
```

And across all local clones, what needs attention:

```console
$ gx ov -f                     # -f fetches first, -a also shows clean repos
! github.com/acme/api  feat/auth  ✎ 2 changed · no upstream
! github.com/acme/web  main       ↓1 behind

3 repositories, 2 need attention
```

## Install

gx needs `git` on the PATH. Binaries for Linux, macOS and Windows (amd64 and arm64) are on the [Releases](https://github.com/hallownet-dev/gx/releases) page; `checksums.txt` there lists their SHA-256.

**Linux and macOS**

```sh
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m); case $arch in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
mkdir -p ~/.local/bin
curl -fsSL -o ~/.local/bin/gx "https://github.com/hallownet-dev/gx/releases/latest/download/gx_${os}_${arch}"
chmod +x ~/.local/bin/gx
```

Make sure `~/.local/bin` is on your PATH (on macOS it isn't by default: add `export PATH="$HOME/.local/bin:$PATH"` to `~/.zshrc`). The binaries aren't signed; downloading with `curl` as above avoids Gatekeeper's quarantine. If you downloaded one in a browser instead, run `xattr -d com.apple.quarantine ~/.local/bin/gx`.

**Windows (PowerShell)**

```powershell
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$dir = "$env:LOCALAPPDATA\Programs\gx"
New-Item -ItemType Directory -Force $dir | Out-Null
Invoke-WebRequest "https://github.com/hallownet-dev/gx/releases/latest/download/gx_windows_$arch.exe" -OutFile "$dir\gx.exe"
$path = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($path -notlike "*$dir*") { [Environment]::SetEnvironmentVariable('Path', "$path;$dir", 'User') }
```

Open a new terminal afterwards so the PATH change applies. The file must be called `gx.exe` (the PowerShell integration looks it up by that name).

**With Go** (any OS, Go 1.27+)

```sh
go install github.com/hallownet-dev/gx@latest
```

This puts `gx` in `$(go env GOPATH)/bin` (`%USERPROFILE%\go\bin` on Windows), which needs to be on your PATH.

### Shell integration

A program can't change its parent shell's directory, so `gx l`, `gx j` and `gx cl` hand the target to a small shell function that does the `cd`. Add one line to your shell's startup file:

| shell | add to | line |
|---|---|---|
| bash / zsh | `~/.bashrc` / `~/.zshrc` | `eval "$(gx shell bash)"` |
| fish | `~/.config/fish/config.fish` | `gx shell fish \| source` |
| PowerShell (5.1 and 7) | `$PROFILE` | `gx shell pwsh \| Out-String \| iex` |

In PowerShell, `$PROFILE` may not exist yet: create it with `New-Item -Force $PROFILE`, then `notepad $PROFILE`. Without the integration (e.g. in `cmd.exe`) everything still works, and the target path is printed instead.

Then check the setup with `gx doctor`, and create a config with `gx config` if you use more than GitHub via `gh`.

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
name = "work-gitlab"
type = "gitlab"
host = "gitlab.example.com"
auth = "ssh"                    # token minted with your SSH key

[[provider]]
name = "work-azure"
type = "azure"
url = "https://devops.example.com/tfs/DefaultCollection"
protocol = "https"              # no token: uses Git Credential Manager

[[provider]]
type = "bitbucket"
host = "bitbucket.example.com"   # bitbucket.org = cloud, anything else = Server/DC
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

**Azure DevOps CLI:** `auth = "az-devops"` lists repos of all projects with `az devops invoke --organization <url> --area git --resource repositories`, so the session from `az devops login` is reused (cloud and Server). It's never tried automatically. `url` is the organization or collection URL, e.g. `https://devops.example.com/tfs/CollectionA`; several of them = one `[[provider]]` per `url`.

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
go vet ./... && go test ./... && go build -o gx .
```

## Releasing

Work happens on branches. A PR runs vet, test and build on Linux, macOS and Windows; after merge the same runs again on `main`.

To publish binaries, tag a commit on `main` with a [semver](https://semver.org) version and push the tag:

```sh
git tag v0.2.0 && git push origin v0.2.0
```

GoReleaser then creates the GitHub release with binaries for Linux, macOS and Windows (amd64, arm64) and release notes grouped from [conventional commit](https://www.conventionalcommits.org) messages (`feat:`, `fix:`, `feat!:`; `chore:`, `ci:`, `docs:`, `test:` are left out).
