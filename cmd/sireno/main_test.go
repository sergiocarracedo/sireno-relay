package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCheckAndAgentPassword(t *testing.T) {
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
  bot: {provider: home, profile: bot, password: [printf, secret]}
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
	os.Unsetenv("SIRENO_AGENT_PASSWORD")
	if err := run([]string{"mail", "folders", "--user", "bot"}, &out); err == nil {
		t.Fatal("missing agent password was accepted")
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

func TestCommandTooling(t *testing.T) {
	previous := findExecutable
	findExecutable = func(string) (string, error) { return "/usr/bin/himalaya", nil }
	t.Cleanup(func() { findExecutable = previous })

	var out bytes.Buffer
	if err := run([]string{"completion", "bash"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "sireno-relay") {
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
