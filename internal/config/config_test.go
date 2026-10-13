package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const valid = `providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password_command: [pass, imap]}
    smtp: {address: "smtp.example.org:465", username: me, password_command: [pass, smtp]}
profiles:
  invoice:
    folders: {allow: ["ro:INBOX/Invoices"]}
users:
  bot:
    provider: home
    profile: invoice
    password_command: [pass, bot]
listen: {imap: "127.0.0.1:1143", smtp: "127.0.0.1:1025"}
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
		{"public listener", strings.Replace(valid, "127.0.0.1:1143", "0.0.0.0:1143", 1), 0600},
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
	cfg := &Config{Profiles: map[string]Profile{"bot": {Folders: Rules{Allow: []string{"ro:*"}}}}, Providers: map[string]Provider{"p": {Address: "me@example.org", IMAP: Server{Address: "imap:993", Username: "me", PasswordCommand: []string{"pass"}}, SMTP: Server{Address: "smtp:465", Username: "me", PasswordCommand: []string{"pass"}}}}, Users: map[string]User{"bot": {Provider: "p", Profile: "bot", PasswordCommand: []string{"pass"}}}}
	warnings, err := cfg.Validate()
	if err != nil || len(warnings) != 1 {
		t.Fatalf("warnings=%v err=%v", warnings, err)
	}
}
