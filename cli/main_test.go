package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCheckRejectsRetiredAgentPassword(t *testing.T) {
	previous := findExecutable
	findExecutable = func(string) (string, error) { return "/usr/bin/himalaya", nil }
	t.Cleanup(func() { findExecutable = previous })
	path := filepath.Join(t.TempDir(), "config.yml")
	config := `providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password: [printf, password]}
profiles:
  bot: {folders: {allow: ["ro:INBOX"]}}
users:
  bot: {provider: home, profile: bot, token_hash: d7c5c19d04e1f02b9e21d9b8014758a148fa5bb4a6c3148d66566837dc3f3b98}
`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"config-check", "--config", path}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "configuration valid") {
		t.Fatalf("unexpected output %q", out.String())
	}
	t.Setenv("SIRENO_CONFIG", path)
	if err := run([]string{"config-check"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"mail", "folders", "--user", "bot"}, &out); err == nil {
		t.Fatal("retired direct mail command was accepted")
	}
}

func TestRequiresHimalaya(t *testing.T) {
	previous := findExecutable
	findExecutable = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { findExecutable = previous })
	if err := run([]string{"config-check"}, io.Discard); err == nil || !strings.Contains(err.Error(), "Himalaya CLI is required") {
		t.Fatalf("unexpected requirement error: %v", err)
	}
}

func TestMissingConfigurationGuidesOwnerToOnboard(t *testing.T) {
	previous := findExecutable
	findExecutable = func(string) (string, error) { return "/usr/bin/himalaya", nil }
	t.Cleanup(func() { findExecutable = previous })
	path := filepath.Join(t.TempDir(), "missing.yml")
	var out bytes.Buffer
	err := run([]string{"config-check", "--config", path}, &out)
	if err == nil || !strings.Contains(err.Error(), "sirenorelay onboard") {
		t.Fatalf("error=%v", err)
	}
}

func TestDefaultConfigPathUsesSirenorelayConfigDirectory(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	if got, want := defaultConfigPath(), filepath.Join(configHome, "sirenorelay", "config.yml"); got != want {
		t.Fatalf("defaultConfigPath()=%q, want %q", got, want)
	}
}

func TestDefaultConfigPathUsesLegacyDirectoryWhenNeeded(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	legacyDir := filepath.Join(configHome, "sireno-relay")
	if err := os.MkdirAll(legacyDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "config.yml")
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := defaultConfigPath(); got != legacyPath {
		t.Fatalf("defaultConfigPath()=%q, want %q", got, legacyPath)
	}
}

func TestCommandTooling(t *testing.T) {
	previous := findExecutable
	findExecutable = func(string) (string, error) { return "/usr/bin/himalaya", nil }
	t.Cleanup(func() { findExecutable = previous })

	var out bytes.Buffer
	if err := run([]string{"completion", "bash"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "sirenorelay") {
		t.Fatalf("completion did not mention command: %q", out.String())
	}
	out.Reset()
	if err := run([]string{"version"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != version {
		t.Fatalf("version = %q, want %q", got, version)
	}
	if err := run([]string{"serve", "unexpected"}, io.Discard); err == nil {
		t.Fatal("unexpected positional argument was accepted")
	} else if _, ok := err.(usageError); !ok {
		t.Fatalf("error = %T, want usageError", err)
	}
}

func TestVersionFlag(t *testing.T) {
	previous := findExecutable
	findExecutable = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { findExecutable = previous })

	var out bytes.Buffer
	if err := run([]string{"--version"}, &out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), version+"\n"; got != want {
		t.Fatalf("--version = %q, want %q", got, want)
	}
}

func TestServeAcceptsDebugFlag(t *testing.T) {
	cmd := newRootCmd(io.Discard, io.Discard)
	serve, _, err := cmd.Find([]string{"serve"})
	if err != nil {
		t.Fatal(err)
	}
	flag := serve.Flags().Lookup("debug")
	if flag == nil || flag.DefValue != "false" {
		t.Fatalf("debug flag=%#v", flag)
	}
}

func TestServiceCommandsAcceptAnOwnerConfigFlag(t *testing.T) {
	cmd := newRootCmd(io.Discard, io.Discard)
	service, _, err := cmd.Find([]string{"service"})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"install", "start", "stop", "restart", "status", "uninstall"} {
		subcommand, _, findErr := service.Find([]string{action})
		if findErr != nil {
			t.Fatalf("find %s: %v", action, findErr)
		}
		if subcommand.InheritedFlags().Lookup("config") == nil {
			t.Errorf("%s does not inherit --config", action)
		}
	}
}
