package relay

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/view"
)

type fake struct {
	sent int
	data []byte
}

func (*fake) Folders(context.Context, config.Provider) ([]string, error) { return nil, nil }
func (*fake) Messages(context.Context, config.Provider, string) ([]provider.Message, error) {
	return nil, nil
}
func (f *fake) Send(_ context.Context, _ config.Provider, _ string, _ []string, data []byte) error {
	f.sent++
	f.data = data
	return nil
}

func TestSendNoPartialDelivery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	content := `providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password_command: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password_command: [printf, password]}
profiles:
  bot:
    folders: {allow: ["ro:INBOX"]}
    addresses: {allow: ["wo:friend@example.org"]}
users:
  bot: {provider: home, profile: bot, password_command: [printf, secret]}
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := view.Open("")
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{}
	r := &Relay{ConfigPath: path, Provider: f, Store: store}
	id, err := r.Authenticate(context.Background(), "bot", "secret")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("From: me@example.org\r\nTo: friend@example.org\r\nX-Sireno-Agent: fake\r\nSubject: Hello\r\n\r\nHi")
	if err := r.Send(context.Background(), id, "me@example.org", []string{"friend@example.org", "bad@example.org"}, raw); err == nil || f.sent != 0 {
		t.Fatal("mixed recipients sent")
	}
	if err := r.Send(context.Background(), id, "me@example.org", []string{"friend@example.org"}, raw); err != nil {
		t.Fatal(err)
	}
	if f.sent != 1 || !strings.HasPrefix(string(f.data), "X-Sireno-Agent: bot\r\n") || strings.Contains(string(f.data), "fake") {
		t.Fatal("wrong identity or count")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "wo:friend@example.org", "wo:else@example.org", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.Send(context.Background(), id, "me@example.org", []string{"friend@example.org"}, raw); err == nil || f.sent != 1 {
		t.Fatal("old profile remained authorized")
	}
}
