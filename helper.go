package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// helperEnv is what decides which git credential helper to offer.
type helperEnv struct {
	goos        string
	helper      string // credential.helper as configured
	gcm         bool   // git credential-manager runs
	gcmStore    string // credential.credentialStore or GCM_CREDENTIAL_STORE
	osxkeychain bool   // git-credential-osxkeychain is installed
	libsecret   bool   // git-credential-libsecret is installed
	dbus        bool   // a desktop session with a secret service
	pass        bool   // pass is installed and initialized (GCM's gpg store)
}

func probeHelper() helperEnv {
	e := helperEnv{goos: runtime.GOOS}
	e.helper, _ = gitOut("", "config", "--get-all", "credential.helper")
	e.helper = strings.TrimSpace(e.helper)
	// Ask git rather than PATH: Git for Windows ships GCM in its own exec path.
	_, err := gitOut("", "credential-manager", "--version")
	e.gcm = err == nil
	e.gcmStore = os.Getenv("GCM_CREDENTIAL_STORE")
	if e.gcmStore == "" {
		e.gcmStore, _ = gitOut("", "config", "--get", "credential.credentialStore")
	}
	execPath, _ := gitOut("", "--exec-path")
	installed := func(name string) bool {
		if _, err := os.Stat(filepath.Join(strings.TrimSpace(execPath), name)); err == nil {
			return true
		}
		_, err := exec.LookPath(name)
		return err == nil
	}
	e.osxkeychain = installed("git-credential-osxkeychain")
	e.libsecret = installed("git-credential-libsecret")
	e.dbus = os.Getenv("DBUS_SESSION_BUS_ADDRESS") != ""
	if _, err := exec.LookPath("pass"); err == nil {
		home, _ := os.UserHomeDir()
		_, err := os.Stat(filepath.Join(home, ".password-store", ".gpg-id"))
		e.pass = err == nil
	}
	return e
}

// chooseHelper returns the git config --global settings that make sign-ins
// persist on this machine, with a description for the prompt; nil when
// nothing needs to be (or can be) set.
func chooseHelper(e helperEnv) (string, [][2]string) {
	if e.helper != "" {
		// GCM on Linux refuses to work until a credential store is chosen.
		if e.goos == "linux" && e.gcm && strings.Contains(e.helper, "manager") && e.gcmStore == "" {
			store, what := linuxGCMStore(e)
			return "Git Credential Manager has no credential store; use " + what,
				[][2]string{{"credential.credentialStore", store}}
		}
		return "", nil
	}
	cache := [][2]string{{"credential.helper", "cache --timeout=86400"}}
	switch {
	case e.gcm && e.goos == "linux" && e.gcmStore == "":
		store, what := linuxGCMStore(e)
		return "Git Credential Manager with " + what,
			[][2]string{{"credential.helper", "manager"}, {"credential.credentialStore", store}}
	case e.gcm:
		return "Git Credential Manager", [][2]string{{"credential.helper", "manager"}}
	case e.goos == "darwin" && e.osxkeychain:
		return "the macOS Keychain", [][2]string{{"credential.helper", "osxkeychain"}}
	case e.goos == "linux" && e.libsecret && e.dbus:
		return "your desktop keyring (libsecret)", [][2]string{{"credential.helper", "libsecret"}}
	case e.goos != "windows": // git credential-cache needs Unix sockets
		return "git's in-memory cache for a day (nothing written to disk)", cache
	}
	return "", nil
}

func linuxGCMStore(e helperEnv) (store, what string) {
	switch {
	case e.dbus:
		return "secretservice", "your desktop keyring"
	case e.pass:
		return "gpg", "pass (gpg)"
	}
	return "cache", "an in-memory cache"
}

var helperChecked bool

// ensureHelper checks, once per run, that a sign-in will be remembered. When
// git has no credential helper (or GCM on Linux has no store), it offers the
// best one this machine can use, and otherwise warns: the sign-in would then
// last only until gx exits.
func ensureHelper(a *asker) {
	if helperChecked {
		return
	}
	helperChecked = true
	e := probeHelper()
	desc, settings := chooseHelper(e)
	if settings == nil {
		if e.helper == "" {
			fmt.Fprintf(a.out, "%s git has no credential helper: this sign-in won't be remembered (see README, Authentication)\n", yellow("!"))
		}
		return
	}
	var cmds []string
	for _, s := range settings {
		cmds = append(cmds, fmt.Sprintf("git config --global %s '%s'", s[0], s[1]))
	}
	q := "git has no credential helper, so this sign-in wouldn't be remembered"
	if e.helper != "" {
		q = "Signing in would fail"
	}
	if !a.yes(fmt.Sprintf("%s. Use %s (%s)?", q, desc, strings.Join(cmds, "; ")), true) {
		fmt.Fprintf(a.out, "%s this sign-in won't be remembered (see README, Authentication)\n", yellow("!"))
		return
	}
	for _, s := range settings {
		if _, err := gitOut("", "config", "--global", s[0], s[1]); err != nil {
			fmt.Fprintf(a.out, "%s %v\n", red("✗"), err)
			return
		}
	}
}
