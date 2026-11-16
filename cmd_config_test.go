package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// testAsker answers prompts from the given lines.
func testAsker(lines ...string) *asker {
	return &asker{in: bufio.NewReader(strings.NewReader(strings.Join(lines, "\n") + "\n")), out: io.Discard}
}

func okCheck(*asker, ProviderConfig) (int, error) { return 3, nil }

func tempConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gx", "config.toml")
	t.Setenv("GX_CONFIG", path)
	t.Setenv("GX_ROOT", "")
	return path
}

func TestNoConfigMeansNoProviders(t *testing.T) {
	tempConfig(t)
	cfg, err := loadConfig()
	if err != nil || cfg.exists || len(cfg.Providers) != 0 {
		t.Fatalf("got %+v %v", cfg, err)
	}
}

func TestSaveConfigRoundTrip(t *testing.T) {
	path := tempConfig(t)
	in := &Config{Root: "~/src", Protocol: "https", Providers: []ProviderConfig{
		{Type: "azure", URL: "https://dev.azure.com/acme", Owners: []string{"Shop"}},
		{Type: "github"},
	}}
	if err := saveConfig(path, in); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.HasPrefix(s, "# gx configuration") || strings.Contains(s, `token = ""`) || strings.Contains(s, "insecure") {
		t.Fatalf("want header and no empty keys:\n%s", s)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode().Perm())
		}
	}
	cfg, err := loadConfig()
	if err != nil || !cfg.exists || len(cfg.Providers) != 2 || cfg.Providers[0].Host != "dev.azure.com" || cfg.Providers[1].Host != "github.com" {
		t.Fatalf("got %+v %v", cfg, err)
	}
	// What was written is what the user chose, not the filled-in defaults.
	if strings.Contains(s, "github.com") {
		t.Fatalf("defaults leaked into the file:\n%s", s)
	}
}

func TestSaveConfigRejectsInvalid(t *testing.T) {
	path := tempConfig(t)
	err := saveConfig(path, &Config{Providers: []ProviderConfig{{Type: "azure"}}})
	if err == nil {
		t.Fatal("want an error for azure without url")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("invalid config must not be written")
	}
}

func TestSetupWizard(t *testing.T) {
	path := tempConfig(t)
	a := testAsker(
		"~/src",                      // root
		"2",                          // protocol: https
		"4",                          // Azure DevOps
		"https://dev.azure.com/acme", // url
		"Shop, Web",                  // owners
		"y",                          // add another
		"1",                          // GitHub
		"",                           // host: default
		"",                           // owners: all
		"n",                          // add another
	)
	if err := runSetup(a, path, okCheck); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Protocol != "https" || !strings.HasSuffix(filepath.ToSlash(cfg.Root), "/src") || len(cfg.Providers) != 2 {
		t.Fatalf("got %+v", cfg)
	}
	az := cfg.Providers[0]
	if az.Type != "azure" || az.URL != "https://dev.azure.com/acme" || !slices.Equal(az.Owners, []string{"Shop", "Web"}) {
		t.Fatalf("azure provider: %+v", az)
	}
	if cfg.Providers[1].Type != "github" || cfg.Providers[1].Host != "github.com" {
		t.Fatalf("github provider: %+v", cfg.Providers[1])
	}
}

func TestSetupRerunNeedsConfirmation(t *testing.T) {
	path := tempConfig(t)
	if err := saveConfig(path, &Config{Providers: []ProviderConfig{{Type: "github"}}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := runSetup(testAsker("n"), path, okCheck); !errors.Is(err, errCancelled) {
		t.Fatalf("want cancelled, got %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("declined rerun changed the config")
	}
	// Confirmed: starts over from scratch.
	a := testAsker("y", "", "", "2", "", "", "n") // replace, root, protocol, GitLab, host, owners, no more
	if err := runSetup(a, path, okCheck); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if len(cfg.Providers) != 1 || cfg.Providers[0].Type != "gitlab" || cfg.Protocol != "ssh" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestSetupFailedCheckNotKeptUnlessAsked(t *testing.T) {
	path := tempConfig(t)
	failing := func(*asker, ProviderConfig) (int, error) { return 0, errors.New("401") }
	a := testAsker("", "", "1", "", "", "n", "n") // root, protocol, GitHub, host, owners, keep? no, add another? no
	if err := runSetup(a, path, failing); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := loadConfig(); len(cfg.Providers) != 0 {
		t.Fatalf("failed provider was kept: %+v", cfg.Providers)
	}
}

func TestConfigEditsAndRemoves(t *testing.T) {
	path := tempConfig(t)
	if err := saveConfig(path, &Config{Providers: []ProviderConfig{{Type: "github"}, {Type: "gitlab"}}}); err != nil {
		t.Fatal(err)
	}
	// Menu: 1 root, 2 protocol, 3 default host, 4 cache ttl, 5 github, 6 gitlab, 7 add.
	a := testAsker(
		"1", "~/code", // set root
		"5", "4", // github → remove
		"5", "2", "team-a", // (now) gitlab → owners
		"", // done
	)
	if err := runConfig(a, path, okCheck); err != nil {
		t.Fatal(err)
	}
	cfg, _ := loadConfig()
	if !strings.HasSuffix(filepath.ToSlash(cfg.Root), "/code") || len(cfg.Providers) != 1 ||
		cfg.Providers[0].Type != "gitlab" || !slices.Equal(cfg.Providers[0].Owners, []string{"team-a"}) {
		t.Fatalf("got %+v", cfg)
	}
}

func TestConfigRejectsBadValueAndKeepsFile(t *testing.T) {
	path := tempConfig(t)
	if err := saveConfig(path, &Config{Providers: []ProviderConfig{{Type: "github"}}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := runConfig(testAsker("4", "soon", ""), path, okCheck); err != nil { // cache ttl = "soon"
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("invalid value was saved")
	}
}

func TestAskerMenu(t *testing.T) {
	a := testAsker("0", "9", "x", "2")
	if got := a.menu("pick", []string{"a", "b"}); got != 1 {
		t.Fatalf("want 1 after rejecting bad input, got %d", got)
	}
	if got := testAsker("").menu("pick", []string{"a"}); got != -1 {
		t.Fatalf("empty answer should cancel, got %d", got)
	}
	if got := (&asker{in: bufio.NewReader(strings.NewReader("")), out: io.Discard}).menu("pick", []string{"a"}); got != -1 {
		t.Fatalf("EOF should cancel, got %d", got)
	}
}
