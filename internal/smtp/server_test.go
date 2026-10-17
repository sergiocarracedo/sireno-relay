package smtp

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/emersion/go-sasl"
	wire "github.com/emersion/go-smtp"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/relay"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/view"
)

type fakeProvider struct{ count int }

func (*fakeProvider) Folders(context.Context, config.Provider) ([]string, error) { return nil, nil }
func (*fakeProvider) Messages(context.Context, config.Provider, string) ([]provider.Message, error) {
	return nil, nil
}
func (f *fakeProvider) Send(context.Context, config.Provider, string, []string, []byte) error {
	f.count++
	return nil
}

func TestSMTPSubmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.yml")
	cfg := `providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password: [printf, password]}
profiles:
  safe:
    folders: {allow: ["ro:INBOX"]}
    addresses: {allow: ["wo:friend@example.org"]}
users:
  bot: {provider: home, profile: safe, password: [printf, secret]}
`
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := view.Open("")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeProvider{}
	srv := Server(&relay.Relay{ConfigPath: path, Provider: f, Store: store}, "127.0.0.1:0")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	defer srv.Close()
	client, err := wire.Dial(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Auth(sasl.NewPlainClient("", "bot", "secret")); err != nil {
		t.Fatal(err)
	}
	msg := []byte("From: me@example.org\r\nTo: friend@example.org\r\nSubject: Hello\r\n\r\nHi")
	if err := client.SendMail("me@example.org", []string{"friend@example.org", "bad@example.org"}, bytes.NewReader(msg)); err == nil || f.count != 0 {
		t.Fatal("denied recipient was submitted")
	}
	if err := client.SendMail("me@example.org", []string{"friend@example.org"}, bytes.NewReader(msg)); err != nil {
		t.Fatal(err)
	}
	if f.count != 1 {
		t.Fatalf("submitted %d times", f.count)
	}
}
