package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCheckAndAgentPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	config := `providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password_command: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password_command: [printf, password]}
profiles:
  bot: {folders: {allow: ["ro:INBOX"]}}
users:
  bot: {provider: home, profile: bot, password_command: [printf, secret]}
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
	os.Unsetenv("SIRENO_AGENT_PASSWORD")
	if err := run([]string{"mail", "folders", "--user", "bot"}, &out); err == nil {
		t.Fatal("missing agent password was accepted")
	}
}
