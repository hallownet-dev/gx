// gx is a small git companion: browse repositories on GitHub, GitLab,
// Bitbucket and Azure DevOps, clone them into a tree derived from their URL,
// and run everyday git commands with short names.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
)

var version = "dev"

type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

const usage = `gx — git, shorter

repositories
  l  [query]       pick remote repos (tab = multi) → clone, or pull if cloned
                   -r refresh the cached list · -p print instead of picking
  j  [query]       jump to a local repository
  cl <url|o/r>     clone into <root>/<host>/<path>
  o  [url|dir]     open the repository in the browser
  ov               overview of all local repos needing attention
                   -a show all · -f fetch first
  ls               list local repositories (-r relative paths)
  adopt <dir>...   move existing clones into the tree (-n dry run, -y yes)

working
  s                status
  a  [paths]       stage (interactive picker without paths)
  c  [-a] [msg]    commit; message words need no quotes; picks files if nothing is staged
                   --amend · -n no-verify
  p  [-f]          push; sets upstream on first push; -f = --force-with-lease
  u                pull
  f                fetch --all --prune
  b  [name]        switch branch (picker without name; creates if missing)
  d / ds           diff / diff --staged
  lg [n]           short graph log

setup
  config [edit]    print (or edit) the config file, creating it if missing
  doctor           check git, config and provider credentials
  shell <sh>       print shell integration (bash, zsh, fish, pwsh) so gx can cd
  version

Anything else is passed to git:  gx rebase -i main  →  git rebase -i main
`

func main() {
	err := run(os.Args[1:])
	if err == nil {
		return
	}
	var ec exitCode
	switch {
	case errors.As(err, &ec):
		os.Exit(int(ec))
	case errors.Is(err, errCancelled):
		os.Exit(130)
	}
	fmt.Fprintln(os.Stderr, red("gx: ")+err.Error())
	os.Exit(1)
}

func run(args []string) error {
	if len(args) == 0 {
		return cmdStatus(nil)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "l", "list":
		return cmdList(rest)
	case "j", "jump":
		return cmdJump(rest)
	case "cl", "clone":
		return cmdClone(rest)
	case "o", "open":
		return cmdOpen(rest)
	case "ov", "overview":
		return cmdOverview(rest)
	case "ls":
		return cmdLs(rest)
	case "adopt":
		return cmdAdopt(rest)
	case "s", "st":
		return cmdStatus(rest)
	case "a":
		return cmdAdd(rest)
	case "c":
		return cmdCommit(rest)
	case "p":
		return cmdPush(rest)
	case "u":
		return git(append([]string{"pull"}, rest...)...)
	case "f":
		return git(append([]string{"fetch", "--all", "--prune"}, rest...)...)
	case "b":
		return cmdBranch(rest)
	case "d":
		return git(append([]string{"diff"}, rest...)...)
	case "ds":
		return git(append([]string{"diff", "--staged"}, rest...)...)
	case "lg":
		n := "20"
		if len(rest) > 0 && rest[0][0] != '-' {
			n, rest = rest[0], rest[1:]
		}
		return git(append([]string{"log", "--graph", "--format=%C(yellow)%h%C(reset)%C(auto)%d%C(reset) %s %C(dim)· %an, %cr%C(reset)", "-n", n}, rest...)...)
	case "config":
		return cmdConfig(rest)
	case "doctor":
		return cmdDoctor(rest)
	case "shell":
		return cmdShell(rest)
	case "version", "--version", "-v":
		fmt.Println("gx", buildVersion())
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	}
	return git(args...)
}

func buildVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func cmdShell(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(`usage: gx shell <bash|zsh|fish|pwsh>

  bash/zsh   eval "$(gx shell bash)"            in ~/.bashrc or ~/.zshrc
  fish       gx shell fish | source             in ~/.config/fish/config.fish
  pwsh       gx shell pwsh | Out-String | iex   in $PROFILE`)
	}
	switch args[0] {
	case "bash", "zsh":
		fmt.Print(`gx() {
  local f rc
  f="$(mktemp "${TMPDIR:-/tmp}/gx-cd.XXXXXX")" || return
  GX_CD_FILE="$f" command gx "$@"; rc=$?
  if [ -s "$f" ]; then cd -- "$(cat -- "$f")" || rc=$?; fi
  rm -f -- "$f"
  return $rc
}
`)
	case "fish":
		fmt.Print(`function gx
    set -l f (mktemp)
    GX_CD_FILE=$f command gx $argv
    set -l rc $status
    if test -s $f
        cd (cat $f)
    end
    rm -f $f
    return $rc
end
`)
	case "pwsh", "powershell":
		fmt.Print(`function gx {
    $f = New-TemporaryFile
    $env:GX_CD_FILE = $f.FullName
    try { & (Get-Command gx -CommandType Application | Select-Object -First 1) @args }
    finally { Remove-Item Env:GX_CD_FILE -ErrorAction SilentlyContinue }
    $d = Get-Content -Raw $f.FullName -ErrorAction SilentlyContinue
    Remove-Item $f.FullName -ErrorAction SilentlyContinue
    if ($d) { Set-Location $d.Trim() }
}
`)
	default:
		return fmt.Errorf("unsupported shell %q (bash, zsh, fish, pwsh)", args[0])
	}
	return nil
}
