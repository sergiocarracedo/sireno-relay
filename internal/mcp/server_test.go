package mcp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/relay"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/view"
)

type fakeProvider struct{}

func (fakeProvider) Folders(context.Context, config.Provider) ([]string, error) {
	return []string{"INBOX", "Private"}, nil
}
func (fakeProvider) Messages(context.Context, config.Provider, string) ([]provider.Message, error) {
	return nil, nil
}
func (fakeProvider) Send(context.Context, config.Provider, string, []string, []byte) error {
	return nil
}

type attachmentProvider struct{ fakeProvider }

func (attachmentProvider) Messages(context.Context, config.Provider, string) ([]provider.Message, error) {
	return []provider.Message{{UID: 42, Raw: []byte("From: store@example.org\r\nTo: me@example.org\r\nCc: accounts@example.org\r\nDate: Wed, 14 Oct 2026 09:30:00 +0000\r\nSubject: Receipt\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nTotal\r\n--x\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=receipt.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjQK\r\n--x--\r\n")}}, nil
}

func TestHandlerRequiresBearerToken(t *testing.T) {
	digest := sha256.Sum256([]byte("test-token"))
	path := filepath.Join(t.TempDir(), "config.yml")
	text := fmt.Sprintf(`providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password: [printf, password]}
profiles:
  bot: {folders: {allow: ["ro:INBOX"]}}
users:
  bot: {provider: home, profile: bot, token_hash: %x}
`, digest)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := view.Open("")
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(&relay.Relay{ConfigPath: path, Provider: fakeProvider{}, Store: store})
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`))
	request.Header.Set("Accept", "application/json, text/event-stream")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response = httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAttachmentEndpointRechecksFilteredView(t *testing.T) {
	digest := sha256.Sum256([]byte("test-token"))
	path := filepath.Join(t.TempDir(), "config.yml")
	text := fmt.Sprintf(`providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password: [printf, password]}
profiles:
  bot:
    folders: {allow: ["ro:INBOX"]}
    inbound: {attachments: {allow: ["application/pdf"]}}
users:
  bot: {provider: home, profile: bot, token_hash: %x}
`, digest)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := view.Open("")
	if err != nil {
		t.Fatal(err)
	}
	r := &relay.Relay{ConfigPath: path, Provider: attachmentProvider{}, Store: store}
	h := Handler(r)
	request := httptest.NewRequest(http.MethodGet, "/mcp/attachments?folder=INBOX&message_uid=1&attachment_id=1", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/pdf" || response.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(response.Body.String(), "%PDF-") {
		t.Fatalf("status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	request.Header.Set("Authorization", "Bearer wrong-token")
	response = httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
	identity, err := r.AuthenticateToken("test-token")
	if err != nil {
		t.Fatal(err)
	}
	result, structured, err := readMessage(r)(context.WithValue(context.Background(), identityKey{}, identity), nil, readMessageArgs{Folder: "INBOX", UID: 1})
	if err != nil || result == nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	message := structured.(map[string]any)["message"].(messageContent)
	if message.Body != "Total" || len(message.Attachments) != 1 || strings.Contains(message.Body, "%PDF-") {
		t.Fatalf("message=%#v", message)
	}
	if message.Attachments[0].Name != "receipt.pdf" {
		t.Fatalf("attachment=%#v", message.Attachments[0])
	}
	_, listed, err := messages(r)(context.WithValue(context.Background(), identityKey{}, identity), nil, listMessagesArgs{Folder: "INBOX"})
	if err != nil {
		t.Fatal(err)
	}
	item := listed.(map[string]any)["messages"].([]envelope)[0]
	if len(item.From) != 1 || len(item.To) != 1 || len(item.Cc) != 1 || item.Date != "2026-10-14T09:30:00Z" || len(item.Attachments) != 1 || item.Attachments[0].Name != "receipt.pdf" {
		t.Fatalf("list item=%#v", item)
	}
}
