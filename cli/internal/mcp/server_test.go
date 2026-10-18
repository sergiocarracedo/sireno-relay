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

func (fakeProvider) Capabilities(context.Context, config.Provider) (provider.Capabilities, error) {
	return provider.Capabilities{provider.CapacityFolders: true, provider.CapacityList: true, provider.CapacityRead: true, provider.CapacitySend: true}, nil
}
func (fakeProvider) Folders(context.Context, config.Provider) ([]string, error) {
	return []string{"INBOX", "Private"}, nil
}
func (fakeProvider) Messages(context.Context, config.Provider, string) ([]provider.Message, error) {
	return nil, nil
}
func (fakeProvider) Page(context.Context, config.Provider, provider.PageRequest) (provider.Page, error) {
	return provider.Page{}, nil
}
func (fakeProvider) Send(context.Context, config.Provider, string, []string, []byte) error {
	return nil
}
func (fakeProvider) StoreFlags(context.Context, config.Provider, string, provider.Message, []provider.Flag, provider.FlagAction) error {
	return nil
}

type attachmentProvider struct{ fakeProvider }

func (attachmentProvider) Messages(context.Context, config.Provider, string) ([]provider.Message, error) {
	return []provider.Message{{ID: "42", Generation: "1", Flags: []provider.Flag{provider.FlagSeen}, Raw: []byte("From: store@example.org\r\nTo: me@example.org\r\nCc: accounts@example.org\r\nDate: Wed, 14 Oct 2026 09:30:00 +0000\r\nSubject: Receipt\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nTotal\r\n--x\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=receipt.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\nJVBERi0xLjQK\r\n--x--\r\n")}}, nil
}
func (p attachmentProvider) Page(ctx context.Context, account config.Provider, request provider.PageRequest) (provider.Page, error) {
	if request.Page != 1 {
		return provider.Page{}, nil
	}
	messages, err := p.Messages(ctx, account, request.Folder)
	return provider.Page{Messages: messages}, err
}

type mutationProvider struct {
	attachmentProvider
	called bool
}

type unavailableProvider struct {
	fakeProvider
	messagesCalled int
}

type manyMessagesProvider struct{ fakeProvider }

func (manyMessagesProvider) Page(_ context.Context, _ config.Provider, request provider.PageRequest) (provider.Page, error) {
	messages := make([]provider.Message, 0, 25)
	for i := 1; i <= 25; i++ {
		messages = append(messages, provider.Message{ID: fmt.Sprintf("%d", i), Generation: "test", Raw: []byte(fmt.Sprintf("From: sender@example.org\r\nSubject: Message %d\r\n\r\nBody", i))})
	}
	start := int(request.Page-1) * int(request.PageSize)
	if start >= len(messages) {
		return provider.Page{}, nil
	}
	end := min(start+int(request.PageSize), len(messages))
	return provider.Page{Messages: messages[start:end], HasMore: end < len(messages)}, nil
}

type sendProvider struct {
	fakeProvider
	sent int
	raw  []byte
}

func (p *sendProvider) Send(_ context.Context, _ config.Provider, _ string, _ []string, raw []byte) error {
	p.sent++
	p.raw = append([]byte(nil), raw...)
	return nil
}

func (*unavailableProvider) Capabilities(context.Context, config.Provider) (provider.Capabilities, error) {
	return provider.Capabilities{provider.CapacityFolders: true}, nil
}

func (p *unavailableProvider) Messages(context.Context, config.Provider, string) ([]provider.Message, error) {
	p.messagesCalled++
	return nil, nil
}

func (p *mutationProvider) Capabilities(context.Context, config.Provider) (provider.Capabilities, error) {
	return provider.Capabilities{provider.CapacityFolders: true, provider.CapacityList: true, provider.CapacityRead: true, provider.CapacitySend: true, provider.CapacityUpdateFlags: true}, nil
}
func (p *mutationProvider) StoreFlags(_ context.Context, _ config.Provider, folder string, target provider.Message, flags []provider.Flag, action provider.FlagAction) error {
	if folder != "INBOX" || target.ID != "42" || target.Generation != "1" || len(flags) != 1 || flags[0] != provider.FlagSeen || action != provider.FlagAdd {
		return fmt.Errorf("unexpected update")
	}
	p.called = true
	return nil
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
	if len(item.From) != 1 || len(item.To) != 1 || len(item.Cc) != 1 || item.Date != "2026-10-14T09:30:00Z" || !item.Read || item.AttachmentCount != 1 || len(item.Attachments) != 1 || item.Attachments[0].Name != "receipt.pdf" {
		t.Fatalf("list item=%#v", item)
	}
}

func TestUpdateMessageFlagsUsesVirtualID(t *testing.T) {
	digest := sha256.Sum256([]byte("test-token"))
	path := filepath.Join(t.TempDir(), "config.yml")
	text := fmt.Sprintf(`providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password: [printf, password]}
profiles:
  bot:
    folders: {allow: ["rw:INBOX"]}
    inbound: {attachments: {allow: ["application/pdf"]}}
users:
  bot: {provider: home, profile: bot, token_hash: %x}
`, digest)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	p := &mutationProvider{}
	store, err := view.Open("")
	if err != nil {
		t.Fatal(err)
	}
	r := &relay.Relay{ConfigPath: path, Provider: p, Store: store}
	identity, err := r.AuthenticateToken("test-token")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = updateMessageFlags(r)(context.WithValue(context.Background(), identityKey{}, identity), nil, updateMessageFlagsArgs{Folder: "INBOX", UID: 1, Action: "add", Flags: []string{"seen"}})
	if err != nil || !p.called {
		t.Fatalf("result error=%v called=%v", err, p.called)
	}
}

func TestMCPReportsUnavailableCapacityWithoutProviderIO(t *testing.T) {
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
	p := &unavailableProvider{}
	h := Handler(&relay.Relay{ConfigPath: path, Provider: p, Store: mustMCPStore(t)})
	initialize := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`))
	initialize.Header.Set("Authorization", "Bearer test-token")
	initialize.Header.Set("Accept", "application/json, text/event-stream")
	initialized := httptest.NewRecorder()
	h.ServeHTTP(initialized, initialize)
	if initialized.Code != http.StatusOK {
		t.Fatalf("initialize status=%d body=%s", initialized.Code, initialized.Body.String())
	}
	sessionID := initialized.Header().Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatalf("missing MCP session ID: headers=%v", initialized.Header())
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_messages","arguments":{"folder":"INBOX"}}}`))
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Session-Id", sessionID)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "capacity unavailable") || p.messagesCalled != 0 {
		t.Fatalf("status=%d body=%s calls=%d", response.Code, response.Body.String(), p.messagesCalled)
	}
}

func TestListMessagesCapsVisibleResultsAtTwenty(t *testing.T) {
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
	r := &relay.Relay{ConfigPath: path, Provider: manyMessagesProvider{}, Store: mustMCPStore(t)}
	identity, err := r.AuthenticateToken("test-token")
	if err != nil {
		t.Fatal(err)
	}
	cursors := newCursorStore()
	list := messagesWithCursors(r, cursors)
	_, structured, err := list(context.WithValue(context.Background(), identityKey{}, identity), nil, listMessagesArgs{Folder: "INBOX"})
	if err != nil {
		t.Fatal(err)
	}
	first := structured.(map[string]any)
	if got := len(first["messages"].([]envelope)); got != 20 {
		t.Fatalf("listed %d messages, want 20", got)
	}
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("next cursor = %#v", first["next_cursor"])
	}
	_, structured, err = list(context.WithValue(context.Background(), identityKey{}, identity), nil, listMessagesArgs{Folder: "INBOX", Cursor: cursor})
	if err != nil {
		t.Fatal(err)
	}
	second := structured.(map[string]any)
	if got := len(second["messages"].([]envelope)); got != 5 {
		t.Fatalf("second page listed %d messages, want 5", got)
	}
	if _, ok := second["next_cursor"]; ok {
		t.Fatal("last page returned a cursor")
	}
	secondMessage := second["messages"].([]envelope)[0]
	resolved, err := r.Message(context.Background(), identity, "INBOX", secondMessage.UID)
	if err != nil || resolved.Mail.Subject != "Message 21" {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
}

func TestSendMessageBuildsAttachmentFreeMail(t *testing.T) {
	digest := sha256.Sum256([]byte("test-token"))
	path := filepath.Join(t.TempDir(), "config.yml")
	text := fmt.Sprintf(`providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password: [printf, password]}
profiles:
  bot: {folders: {allow: ["ro:INBOX"]}, outbound: {addresses: {allow: ["friend@example.org"]}}}
users:
  bot: {provider: home, profile: bot, token_hash: %x}
`, digest)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	p := &sendProvider{}
	r := &relay.Relay{ConfigPath: path, Provider: p, Store: mustMCPStore(t)}
	identity, err := r.AuthenticateToken("test-token")
	if err != nil {
		t.Fatal(err)
	}
	_, result, err := sendMessage(r)(context.WithValue(context.Background(), identityKey{}, identity), nil, sendMessageArgs{From: "me@example.org", To: []string{"friend@example.org"}, Subject: "Hello", Body: "Processed"})
	if err != nil || !result.(map[string]any)["sent"].(bool) || p.sent != 1 || !strings.Contains(string(p.raw), "Content-Type: text/plain") || strings.Contains(string(p.raw), "Content-Disposition") {
		t.Fatalf("result=%#v sent=%d raw=%q err=%v", result, p.sent, p.raw, err)
	}
}

func mustMCPStore(t *testing.T) *view.Store {
	t.Helper()
	store, err := view.Open("")
	if err != nil {
		t.Fatal(err)
	}
	return store
}
