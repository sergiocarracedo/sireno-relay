package mcp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider/fixture"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/relay"
)

func TestFixturePaginationHidesDeniedCandidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	hash := func(token string) string { value := sha256.Sum256([]byte(token)); return fmt.Sprintf("%x", value) }
	config := fmt.Sprintf(`providers:
  home:
    address: owner@example.org
    imap: {address: "imap.example.org:993", username: owner, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: owner, password: [printf, password]}
profiles:
  visible: {folders: {allow: ["ro:INBOX"]}, addresses: {allow: ["ro:allowed@example.org"]}}
users:
  agent: {provider: home, profile: visible, token_hash: %s}
`, hash("fixture-token"))
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	p := fixture.New()
	p.Pages["INBOX"] = [][]provider.Message{
		{{ID: "hidden", Generation: "g", Raw: []byte("From: hidden@example.org\r\nSubject: Secret\r\n\r\nNo")}},
		{{ID: "visible", Generation: "g", Raw: []byte("From: allowed@example.org\r\nSubject: Receipt\r\n\r\nYes")}},
	}
	r := &relay.Relay{ConfigPath: path, Provider: p, Store: mustMCPStore(t)}
	identity, err := r.AuthenticateToken("fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	_, structured, err := messagesWithCursors(r, newCursorStore())(context.WithValue(context.Background(), identityKey{}, identity), nil, listMessagesArgs{Folder: "INBOX"})
	if err != nil {
		t.Fatal(err)
	}
	messages := structured.(map[string]any)["messages"].([]envelope)
	if len(messages) != 1 || messages[0].Subject != "Receipt" || len(p.Calls) != 2 {
		t.Fatalf("messages=%#v calls=%#v", messages, p.Calls)
	}
}
