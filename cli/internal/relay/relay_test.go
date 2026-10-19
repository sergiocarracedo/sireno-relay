package relay

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/view"
)

func tokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", digest)
}

type fake struct {
	sent     int
	data     []byte
	messages []provider.Message
	stored   struct {
		folder string
		target provider.Message
		flags  []provider.Flag
		action provider.FlagAction
	}
}

func (*fake) Capabilities(context.Context, config.Provider) (provider.Capabilities, error) {
	return provider.Capabilities{provider.CapacityFolders: true, provider.CapacityList: true, provider.CapacityRead: true, provider.CapacitySend: true, provider.CapacityUpdateFlags: true}, nil
}
func (*fake) Folders(context.Context, config.Provider) ([]string, error) { return nil, nil }
func (f *fake) Messages(context.Context, config.Provider, string) ([]provider.Message, error) {
	return f.messages, nil
}
func (f *fake) Page(_ context.Context, _ config.Provider, request provider.PageRequest) (provider.Page, error) {
	if request.Page != 1 {
		return provider.Page{}, nil
	}
	return provider.Page{Messages: f.messages}, nil
}
func (f *fake) Send(_ context.Context, _ config.Provider, _ string, _ []string, data []byte) error {
	f.sent++
	f.data = data
	return nil
}

func (f *fake) StoreFlags(_ context.Context, _ config.Provider, folder string, target provider.Message, flags []provider.Flag, action provider.FlagAction) error {
	f.stored.folder = folder
	f.stored.target = target
	f.stored.flags = append([]provider.Flag(nil), flags...)
	f.stored.action = action
	return nil
}

func TestSendNoPartialDelivery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	content := fmt.Sprintf(`providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password: [printf, password]}
profiles:
  bot:
    folders: {allow: ["ro:INBOX"]}
    outbound: {addresses: {allow: ["friend@example.org"]}, signature: "Sent by Hermes Agent"}
users:
  bot: {provider: home, profile: bot, token_hash: %s}
`, tokenHash("agent-token"))
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := view.Open("")
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{}
	r := &Relay{ConfigPath: path, Provider: f, Store: store}
	id, err := r.AuthenticateToken("agent-token")
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
	if f.sent != 1 || !strings.HasPrefix(string(f.data), "X-Sireno-Agent: bot\r\n") || strings.Contains(string(f.data), "fake") || !strings.Contains(string(f.data), "Hi\n\nSent by Hermes Agent") {
		t.Fatal("wrong identity or count")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "friend@example.org", "else@example.org", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.Send(context.Background(), id, "me@example.org", []string{"friend@example.org"}, raw); err == nil || f.sent != 1 {
		t.Fatal("old profile remained authorized")
	}
}

func TestAuthenticateTokenSelectsOnlyMatchingUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	content := fmt.Sprintf(`providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password: [printf, password]}
profiles:
  bot: {folders: {allow: ["ro:INBOX"]}}
users:
  bot: {provider: home, profile: bot, token_hash: %s}
`, tokenHash("agent-token"))
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	r := &Relay{ConfigPath: path, Provider: &fake{}, Store: mustStore(t)}
	identity, err := r.AuthenticateToken("agent-token")
	if err != nil || identity.Name != "bot" {
		t.Fatalf("identity=%#v err=%v", identity, err)
	}
	if _, err := r.AuthenticateToken("wrong-token"); err == nil {
		t.Fatal("wrong token authenticated")
	}
}

func TestUpdateFlagsRevalidatesVirtualMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	content := fmt.Sprintf(`providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password: [printf, password]}
profiles:
  bot: {folders: {allow: ["rw:INBOX"]}}
users:
  bot: {provider: home, profile: bot, token_hash: %s}
`, tokenHash("agent-token"))
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	f := &fake{messages: []provider.Message{{
		ID: "42", Generation: "101", Raw: []byte("From: sender@example.org\r\nSubject: Hi\r\n\r\nBody"),
	}}}
	r := &Relay{ConfigPath: path, Provider: f, Store: mustStore(t)}
	identity, err := r.AuthenticateToken("agent-token")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.Snapshot(context.Background(), identity, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateFlags(context.Background(), identity, "INBOX", snapshot.Messages[0].UID, []provider.Flag{provider.FlagSeen}, provider.FlagAdd); err != nil {
		t.Fatal(err)
	}
	if f.stored.folder != "INBOX" || f.stored.target.ID != "42" || f.stored.target.Generation != "101" || f.stored.action != provider.FlagAdd || len(f.stored.flags) != 1 || f.stored.flags[0] != provider.FlagSeen {
		t.Fatalf("unexpected store call: %+v", f.stored)
	}
}

func TestCurrentAppliesProfileChangesImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	content := fmt.Sprintf(`providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password: [printf, password]}
profiles:
  bot: {folders: {allow: ["ro:INBOX"]}}
users:
  bot: {provider: home, profile: bot, token_hash: %s}
`, tokenHash("agent-token"))
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	r := &Relay{ConfigPath: path, Provider: &fake{}, Store: mustStore(t)}
	identity, err := r.AuthenticateToken("agent-token")
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(content, `allow: ["ro:INBOX"]`, `allow: ["ro:Private"]`, 1)
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	current, err := r.Current(identity)
	if err != nil {
		t.Fatalf("profile update required reconnect: %v", err)
	}
	if !current.Policy.ReadFolder("Private") || current.Policy.ReadFolder("INBOX") {
		t.Fatal("current operation did not use updated policy")
	}
}

func TestTokenRotationRevokesAuthenticatedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	content := fmt.Sprintf(`providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password: [printf, password]}
profiles:
  bot: {folders: {allow: ["ro:INBOX"]}, outbound: {addresses: {allow: ["friend@example.org"]}}}
users:
  bot: {provider: home, profile: bot, token_hash: %s}
`, tokenHash("old-token"))
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	f := &fake{}
	r := &Relay{ConfigPath: path, Provider: f, Store: mustStore(t)}
	identity, err := r.AuthenticateToken("old-token")
	if err != nil {
		t.Fatal(err)
	}
	rotated := strings.Replace(content, tokenHash("old-token"), tokenHash("new-token"), 1)
	if err := os.WriteFile(path, []byte(rotated), 0600); err != nil {
		t.Fatal(err)
	}
	raw := []byte("From: me@example.org\r\nTo: friend@example.org\r\nSubject: Hello\r\n\r\nHi")
	if err := r.Send(context.Background(), identity, "me@example.org", []string{"friend@example.org"}, raw); err == nil || f.sent != 0 {
		t.Fatal("rotated token retained send access")
	}
}

func mustStore(t *testing.T) *view.Store {
	t.Helper()
	store, err := view.Open("")
	if err != nil {
		t.Fatal(err)
	}
	return store
}
