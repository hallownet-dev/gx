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

Then run `gx setup`: it asks where repositories should live, which VCSs you use (GitHub, GitLab, Bitbucket, Azure DevOps), signs you in and checks that each one lists your repositories. `gx doctor` checks everything again later.

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

setup
  setup            choose your VCSs and sign in (rerun to start over)
  config           change settings and VCSs (path / edit: open in editor)
  doctor           check git, config and provider credentials

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

`gx setup` writes `~/.config/gx/config.toml` (same location on every OS; override with `GX_CONFIG`). Rerunning it starts over. `gx config` shows the settings and VCSs as a numbered menu: pick one to change it, remove a VCS, check its sign-in, or add another. Every change is checked before it's saved. Without a terminal, `gx config` prints the file; `gx config edit` opens it in `$EDITOR` for options the menu doesn't cover (tokens, `auth`, `protocol` per provider, …). gx writes the file itself, so comments in it are not kept.

Without a config, `gx l` offers to run setup; gx doesn't assume any VCS.

Example of everything the file can hold:

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
| Azure DevOps | git credential helper |
| Bitbucket | git credential helper |

**git credential helper** means whatever git itself uses for HTTPS: Git Credential Manager, macOS Keychain, libsecret, `pass`… If you have cloned from a host over HTTPS once, gx can list its repos.

If nothing usable is stored, `gx l` run in a terminal offers to sign in once: Git Credential Manager opens the browser for dev.azure.com, or asks for a PAT on Azure DevOps Server (without GCM, git asks in the terminal: any username, and a PAT as the password). The new credential is saved through your credential helper, so `git clone` uses it too. If the server rejected the stored one, signing in replaces it. Scripts and `gx doctor` never prompt or change stored credentials; they print a hint instead.

Collections on one Azure DevOps Server share a stored credential (git keys it by host), so use a PAT that covers all of them, or `token_cmd` per provider. Servers that only accept Windows sign-in (NTLM/Kerberos) need `token_cmd` with a PAT.

Pin a method with `auth = "token" | "gh" | "glab" | "git-credential"`.

**Azure DevOps:** `url` is the organization or collection URL, e.g. `https://dev.azure.com/myorg` or `https://devops.example.com/tfs/CollectionA`; several of them = one `[[provider]]` per `url`. In `gx setup` you can also give just the server (`https://devops.example.com/tfs`, or whatever its virtual directory is): gx lists its collections and adds the ones you pick.

**Remembering sign-ins:** a sign-in is stored by git's credential helper. If none is configured, gx offers (once, before signing in) the best one the machine has, and otherwise warns that the sign-in is forgotten when gx exits:

| OS | offered, in order |
|---|---|
| Windows | Git Credential Manager (ships with Git for Windows) |
| macOS | Git Credential Manager if installed, else the Keychain (`osxkeychain`) |
| Linux | Git Credential Manager with a store (`secretservice` on a desktop, `gpg` if `pass` is set up, else `cache`), else `libsecret` on a desktop, else `cache --timeout=86400` (memory only, one day) |

On Linux, GCM refuses to work until `credential.credentialStore` is set; gx offers that too when GCM is configured without one.

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

## License

[MIT](LICENSE)
