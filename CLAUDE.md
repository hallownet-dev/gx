# gx

Single-binary git companion: fuzzy-browse repos on GitHub, GitLab, Bitbucket (Cloud + Server/DC) and Azure DevOps (Services + Server), clone them to `<root>/<host>/<full path>`, and wrap daily git commands in 1–2 letter subcommands. See README.md for user-facing docs.

## Why it exists

The owner switches machines and OSes often and works for clients on all four providers. Goals: the same repo lives at the same path on every machine (this also keeps Claude Code's per-path memory/history aligned), no heavy TUI (lazygit was rejected as slow and clumsy), and as few hand-managed tokens as possible.

## Layout

Flat `package main`, one file per concern:

- `repourl.go`: URL → `RepoURL{Host, Path}`. **The core invariant: every URL form of a repo (ssh, scp-like, https, browser URL, shorthand) maps to the same path.** Any change here needs a case in `repourl_test.go`.
- `auth.go`: credential resolution (token / gh / glab / az / git-credential / GitLab-over-ssh minting) with ordered fallback.
- `providers.go`: per-provider API listing, Basic→Bearer retry on 401, remote list cache.
- `config.go`: TOML config at `~/.config/gx/config.toml` on every OS (deliberately not `os.UserConfigDir`, so dotfiles sync works the same everywhere).
- `cmd_repos.go`: `l j cl o ov ls adopt doctor`. `cmd_work.go`: `s a c p b`. `main.go`: dispatch, help, `gx shell`.
- `git.go`: shells out to the `git` binary (keeps the user's SSH and credential setup); porcelain v2 status parser.
- `ui.go`: colors, spinner, fuzzy picker (`ktr0731/go-fuzzyfinder`; no fzf dependency, works on Windows).

## Conventions and decisions

- Unknown subcommands pass through to git; `gx` with no args runs `gx s`. Don't add subcommands that shadow common git verbs (`status`, `commit`, `push` stay raw git).
- Changing directory: a process can't change its parent's directory, so gx writes the target to `$GX_CD_FILE` and the shell function from `gx shell <sh>` does the `cd`. Without the wrapper the path goes to stdout.
- `gx p -f` maps to `--force-with-lease`; `gx c` with nothing staged opens a file picker rather than silently staging everything.
- `auth = "ssh"` is never tried automatically, because it creates a token on the server.
- Credential helpers are called with prompting disabled (`GIT_TERMINAL_PROMPT=0`, `credential.interactive=never`), so a missing credential must fail fast, never hang.
- Archived repos are hidden unless `include_archived = true`.
- Errors from a single provider are warnings; `gx l` still shows the others (and stale cache if available).

## Testing

```sh
go vet ./... && go test ./... && go build -o gx .
```

Tests isolate git via `GIT_CONFIG_GLOBAL`/`XDG_*` and use fake credential helpers, an `httptest` server and a fake `ssh` on PATH. Never run tests or `gx doctor` against real GitLab with `auth = "ssh"` without the owner's OK: it creates real tokens. The TUI can be smoke-tested through a pty (Python `pty.fork`, send `\r` / `\x1b`).

CI (`.github/workflows/ci.yml`) runs on Linux, macOS and Windows; shell-script-based tests skip on Windows. Flow: branch → PR (ci.yml: vet, test, build) → manual merge → ci.yml again on `main`. Releases: pushing a `vX.Y.Z` tag runs `.github/workflows/release.yml`, where GoReleaser publishes plain linux/windows amd64/arm64 binaries. Commit messages stay conventional (`feat:`, `fix:`, `chore:` …).

## Verified vs. not yet verified

- Verified for real: GitHub listing via `gh` (100+ repos, pagination), all git subcommands, picker, `ov`, `adopt -n`.
- Unit-tested only, never run against a real server: GitLab, Bitbucket Cloud/Server, Azure DevOps APIs; `az` auth; GitLab SSH token minting.

## Open ideas

- Windows-native tests for the credential helper path.
- Homebrew tap / scoop manifest via GoReleaser.
