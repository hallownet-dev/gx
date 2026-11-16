package main

import (
	"bufio"
	"slices"
	"strings"
	"testing"
)

func TestChooseHelper(t *testing.T) {
	cases := []struct {
		name string
		env  helperEnv
		want [][2]string // git config --global settings; nil = nothing to set
	}{
		{"already configured", helperEnv{goos: "linux", helper: "store"}, nil},
		{"windows GCM", helperEnv{goos: "windows", gcm: true}, [][2]string{{"credential.helper", "manager"}}},
		{"windows nothing", helperEnv{goos: "windows"}, nil},
		{"mac GCM", helperEnv{goos: "darwin", gcm: true, osxkeychain: true}, [][2]string{{"credential.helper", "manager"}}},
		{"mac keychain", helperEnv{goos: "darwin", osxkeychain: true}, [][2]string{{"credential.helper", "osxkeychain"}}},
		{"linux GCM desktop", helperEnv{goos: "linux", gcm: true, dbus: true},
			[][2]string{{"credential.helper", "manager"}, {"credential.credentialStore", "secretservice"}}},
		{"linux GCM pass", helperEnv{goos: "linux", gcm: true, pass: true},
			[][2]string{{"credential.helper", "manager"}, {"credential.credentialStore", "gpg"}}},
		{"linux GCM headless", helperEnv{goos: "linux", gcm: true},
			[][2]string{{"credential.helper", "manager"}, {"credential.credentialStore", "cache"}}},
		{"linux GCM store chosen", helperEnv{goos: "linux", gcm: true, gcmStore: "gpg"}, [][2]string{{"credential.helper", "manager"}}},
		{"linux libsecret", helperEnv{goos: "linux", libsecret: true, dbus: true}, [][2]string{{"credential.helper", "libsecret"}}},
		{"linux libsecret headless", helperEnv{goos: "linux", libsecret: true}, [][2]string{{"credential.helper", "cache --timeout=86400"}}},
		{"linux nothing", helperEnv{goos: "linux"}, [][2]string{{"credential.helper", "cache --timeout=86400"}}},
		{"linux GCM set, no store", helperEnv{goos: "linux", gcm: true, helper: "manager"},
			[][2]string{{"credential.credentialStore", "cache"}}},
		{"linux GCM set, store chosen", helperEnv{goos: "linux", gcm: true, helper: "manager", gcmStore: "secretservice"}, nil},
		{"mac GCM set", helperEnv{goos: "darwin", gcm: true, helper: "manager"}, nil},
	}
	for _, c := range cases {
		desc, got := chooseHelper(c.env)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
		if got != nil && desc == "" {
			t.Errorf("%s: missing description", c.name)
		}
	}
}

func TestEnsureHelper(t *testing.T) {
	isolateGit(t, "")
	var out strings.Builder
	helperChecked = false
	ensureHelper(&asker{in: bufio.NewReader(strings.NewReader("n\n")), out: &out})
	if !strings.Contains(out.String(), "won't be remembered") {
		t.Fatalf("no warning without a helper: %q", out.String())
	}
	if h, _ := gitOut("", "config", "--global", "--get", "credential.helper"); h != "" {
		t.Fatalf("declined, but helper was set to %q", h)
	}

	isolateGit(t, "store")
	out.Reset()
	helperChecked = false
	ensureHelper(&asker{in: bufio.NewReader(strings.NewReader("")), out: &out})
	if out.Len() != 0 {
		t.Fatalf("helper configured, want silence, got %q", out.String())
	}

	// Accepting writes the chosen settings to the global config.
	isolateGit(t, "")
	out.Reset()
	helperChecked = false
	desc, settings := chooseHelper(probeHelper())
	ensureHelper(&asker{in: bufio.NewReader(strings.NewReader("y\n")), out: &out})
	if settings != nil {
		if h, _ := gitOut("", "config", "--global", "--get", settings[0][0]); h != settings[0][1] {
			t.Fatalf("accepted %q, but %s = %q", desc, settings[0][0], h)
		}
	}
}
