package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testTokenHash = "d7c5c19d04e1f02b9e21d9b8014758a148fa5bb4a6c3148d66566837dc3f3b98"

const valid = `providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password: [pass, imap]}
    smtp: {address: "smtp.example.org:465", username: me, password: [pass, smtp]}
profiles:
  invoice:
    folders: {allow: ["ro:INBOX/Invoices"]}
users:
  bot:
    provider: home
    profile: invoice
    token_hash: ` + testTokenHash + `
listen: {mcp: "127.0.0.1:8080"}
`

func loadText(t *testing.T, text string, mode os.FileMode) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(path)
	return err
}

func TestLoad(t *testing.T) {
	if err := loadText(t, valid, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input string
		mode        os.FileMode
	}{
		{"duplicate key", valid + "users: {}\n", 0600},
		{"unknown field", strings.Replace(valid, "profile: invoice", "profil: invoice", 1), 0600},
		{"unknown profile", strings.Replace(valid, "profile: invoice", "profile: absent", 1), 0600},
		{"retired agent password", strings.Replace(valid, "token_hash: "+testTokenHash, "password: [pass, agent]", 1), 0600},
		{"invalid attachment mode", strings.Replace(valid, "folders: {allow: [\"ro:INBOX/Invoices\"]}", "folders: {allow: [\"ro:INBOX/Invoices\"]}\n    inbound: {attachments: {mode: discard}}", 1), 0600},
		{"retired IMAP listener", strings.Replace(valid, `listen: {mcp: "127.0.0.1:8080"}`, `listen: {imap: "127.0.0.1:1143"}`, 1), 0600},
		{"public MCP listener", strings.Replace(valid, "127.0.0.1:8080", "0.0.0.0:8080", 1), 0600},
		{"world readable", valid, 0644},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := loadText(t, tc.input, tc.mode); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestWarnBroadFolder(t *testing.T) {
	cfg := &Config{Profiles: map[string]Profile{"bot": {Folders: Rules{Allow: []string{"ro:*"}}}}, Providers: map[string]Provider{"p": {Address: "me@example.org", IMAP: Server{Address: "imap:993", Username: "me", Password: Password{Command: []string{"pass"}}}, SMTP: Server{Address: "smtp:465", Username: "me", Password: Password{Command: []string{"pass"}}}}}, Users: map[string]User{"bot": {Provider: "p", Profile: "bot", TokenHash: testTokenHash}}}
	warnings, err := cfg.Validate()
	if err != nil || len(warnings) != 1 {
		t.Fatalf("warnings=%v err=%v", warnings, err)
	}
}

func TestRejectsDuplicateTokenHash(t *testing.T) {
	digest := sha256.Sum256([]byte("shared-token"))
	text := strings.SplitN(valid, "users:\n", 2)[0] + fmt.Sprintf(`users:
  first: {provider: home, profile: invoice, token_hash: %x}
  second: {provider: home, profile: invoice, token_hash: %x}
`, digest, digest)
	if err := loadText(t, text, 0600); err == nil || !strings.Contains(err.Error(), "same token_hash") {
		t.Fatalf("duplicate token hash error = %v", err)
	}
}

func TestLoadExpandsEnvironmentValues(t *testing.T) {
	t.Setenv("SIRENO_TEST_USER", "owner@example.org")
	text := strings.ReplaceAll(valid, "me@example.org", `"{env:SIRENO_TEST_USER}"`)
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers["home"].Address; got != "owner@example.org" {
		t.Fatalf("provider address = %q", got)
	}
	if err := loadText(t, strings.Replace(valid, "me@example.org", `"{env:SIRENO_MISSING_VALUE}"`, 1), 0600); err == nil {
		t.Fatal("missing variable was accepted")
	}
}

func TestPasswordAcceptsEnvironmentValueOrCommand(t *testing.T) {
	t.Setenv("SIRENO_TEST_PASSWORD", "agent-secret")
	text := strings.Replace(valid, "password: [pass, imap]", `password: "{env:SIRENO_TEST_PASSWORD}"`, 1)
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers["home"].IMAP.Password; got.Value != "agent-secret" || len(got.Command) != 0 {
		t.Fatalf("password = %#v", got)
	}
}
