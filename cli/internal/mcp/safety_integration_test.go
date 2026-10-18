package mcp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider/fixture"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/relay"
)

const (
	homeToken    = "home-reader-token"
	privateToken = "private-reader-token"
	workToken    = "work-reader-token"
)

func TestFixturePaginationHidesDeniedCandidates(t *testing.T) {
	// A denied message must not consume a page slot or reveal its metadata.
	path := writeFixtureConfig(t, homeToken, privateToken, workToken)
	p := fixture.New()
	p.FoldersValue = []string{"INBOX"}
	p.PagesByAddress["home@example.org"] = map[string][][]provider.Message{
		"INBOX": {
			{{ID: "hidden", Generation: "home-1", Raw: rawMessage("hidden@example.org", "Secret", "No")}},
			{{ID: "visible", Generation: "home-1", Raw: rawMessage("allowed@example.org", "Receipt", "Yes")}},
		},
	}
	r := fixtureRelay(t, path, p)
	identity := fixtureIdentity(t, r, homeToken)
	_, structured, err := messagesWithCursors(r, newCursorStore())(fixtureContext(identity), nil, listMessagesArgs{Folder: "INBOX"})
	if err != nil {
		t.Fatal(err)
	}
	messages := structured.(map[string]any)["messages"].([]envelope)
	if len(messages) != 1 || messages[0].Subject != "Receipt" || len(p.PageCallSnapshot()) != 2 {
		t.Fatalf("messages=%#v calls=%#v", messages, p.PageCallSnapshot())
	}
	if strings.Contains(fmt.Sprintf("%#v", structured), "hidden") {
		t.Fatalf("denied candidate leaked: %#v", structured)
	}
}

func TestFixtureConstraintSuite(t *testing.T) {
	t.Run("unknown folders suggest only visible alternatives", func(t *testing.T) {
		path := writeFixtureConfig(t, homeToken, privateToken, workToken)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content = []byte(strings.Replace(string(content), `folders: {allow: ["rw:INBOX"]}`, `folders: {allow: ["ro:Agent/Inbox"]}`, 1))
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		p := seededFixture()
		p.FoldersByAddress["home@example.org"] = []string{"Agent/Inbox", "Private"}
		r := fixtureRelay(t, path, p)
		_, _, err = messagesWithCursors(r, newCursorStore())(fixtureContext(fixtureIdentity(t, r, homeToken)), nil, listMessagesArgs{Folder: "INBOX"})
		if err == nil || err.Error() != `folder unavailable; available folders: "Agent/Inbox"` {
			t.Fatalf("error=%v", err)
		}
		if got := len(p.PageCallSnapshot()); got != 0 {
			t.Fatalf("unknown folder reached provider page %d times", got)
		}
	})

	t.Run("unauthenticated attachment requests never reach the provider", func(t *testing.T) {
		// Reject unauthenticated requests before any mailbox access.
		path := writeFixtureConfig(t, homeToken, privateToken, workToken)
		p := seededFixture()
		r := fixtureRelay(t, path, p)
		request := httptest.NewRequest(http.MethodGet, "/mcp/attachments?folder=INBOX&message_uid=1&attachment_id=1", nil)
		response := httptest.NewRecorder()
		Handler(r).ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d", response.Code)
		}
		if got := len(p.PageCallSnapshot()); got != 0 {
			t.Fatalf("unauthenticated request reached provider %d times", got)
		}
	})

	t.Run("users sharing a provider remain policy-isolated", func(t *testing.T) {
		// Separate policies over one mailbox must expose separate virtual views.
		path := writeFixtureConfig(t, homeToken, privateToken, workToken)
		p := seededFixture()
		r := fixtureRelay(t, path, p)

		home := fixtureIdentity(t, r, homeToken)
		private := fixtureIdentity(t, r, privateToken)
		work := fixtureIdentity(t, r, workToken)
		assertFolders(t, r, home, []string{"INBOX"})
		assertFolders(t, r, private, []string{"Private"})
		assertFolders(t, r, work, []string{"INBOX"})

		list := messagesWithCursors(r, newCursorStore())
		_, homeResult, err := list(fixtureContext(home), nil, listMessagesArgs{Folder: "INBOX"})
		if err != nil {
			t.Fatal(err)
		}
		if got := homeResult.(map[string]any)["messages"].([]envelope); len(got) != 1 || got[0].Subject != "Home receipt" {
			t.Fatalf("home messages=%#v", got)
		}
		_, privateResult, err := list(fixtureContext(private), nil, listMessagesArgs{Folder: "Private"})
		if err != nil {
			t.Fatal(err)
		}
		if got := privateResult.(map[string]any)["messages"].([]envelope); len(got) != 1 || got[0].Subject != "Private note" {
			t.Fatalf("private messages=%#v", got)
		}
		p.ResetCalls()
		if _, _, err := list(fixtureContext(home), nil, listMessagesArgs{Folder: "Private"}); err == nil {
			t.Fatal("forbidden folder was listed")
		}
		if got := len(p.PageCallSnapshot()); got != 0 {
			t.Fatalf("forbidden folder reached provider %d times", got)
		}
	})

	t.Run("providers are isolated even when policy is shared", func(t *testing.T) {
		// The same policy must never make a second provider's mailbox visible.
		path := writeFixtureConfig(t, homeToken, privateToken, workToken)
		p := seededFixture()
		r := fixtureRelay(t, path, p)
		list := messagesWithCursors(r, newCursorStore())
		_, result, err := list(fixtureContext(fixtureIdentity(t, r, workToken)), nil, listMessagesArgs{Folder: "INBOX"})
		if err != nil {
			t.Fatal(err)
		}
		items := result.(map[string]any)["messages"].([]envelope)
		if len(items) != 1 || items[0].Subject != "Work receipt" {
			t.Fatalf("work messages=%#v", items)
		}
		calls := p.PageCallSnapshot()
		if len(calls) != 1 || calls[0].Address != "work@example.org" {
			t.Fatalf("provider calls=%#v", calls)
		}
	})

	t.Run("token rotation invalidates a cursor before provider access", func(t *testing.T) {
		// A cursor obtained with an old token cannot resume after token rotation.
		path := writeFixtureConfig(t, homeToken, privateToken, workToken)
		p := seededFixture()
		p.PagesByAddress["home@example.org"]["INBOX"] = [][]provider.Message{visibleMessages("home-2", 21)}
		r := fixtureRelay(t, path, p)
		list := messagesWithCursors(r, newCursorStore())
		oldIdentity := fixtureIdentity(t, r, homeToken)
		_, first, err := list(fixtureContext(oldIdentity), nil, listMessagesArgs{Folder: "INBOX"})
		if err != nil {
			t.Fatal(err)
		}
		cursor := first.(map[string]any)["next_cursor"].(string)
		rotateFixtureToken(t, path, homeToken, "rotated-home-token")
		p.ResetCalls()
		newIdentity := fixtureIdentity(t, r, "rotated-home-token")
		if _, _, err := list(fixtureContext(newIdentity), nil, listMessagesArgs{Folder: "INBOX", Cursor: cursor}); err == nil {
			t.Fatal("rotated token accepted an old cursor")
		}
		if got := len(p.PageCallSnapshot()); got != 0 {
			t.Fatalf("invalid cursor reached provider %d times", got)
		}
	})

	t.Run("attachment access is revoked when policy tightens", func(t *testing.T) {
		// A previously listed attachment becomes unavailable as soon as policy denies it.
		path := writeFixtureConfig(t, homeToken, privateToken, workToken)
		p := seededFixture()
		p.PagesByAddress["home@example.org"]["INBOX"] = [][]provider.Message{{{
			ID: "attachment", Generation: "home-attachment", Raw: multipartMessage("allowed@example.org", "Receipt", "%PDF-secret-bytes"),
		}}}
		r := fixtureRelay(t, path, p)
		identity := fixtureIdentity(t, r, homeToken)
		snapshot, err := r.Snapshot(context.Background(), identity, "INBOX")
		if err != nil || len(snapshot.Messages) != 1 || len(snapshot.Messages[0].Mail.Attachments) != 1 {
			t.Fatalf("snapshot=%#v err=%v", snapshot, err)
		}
		uid := snapshot.Messages[0].UID
		h := Handler(r)
		request := attachmentRequest(homeToken, uid)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "%PDF-secret-bytes") {
			t.Fatalf("initial attachment response=%d %q", response.Code, response.Body.String())
		}
		denyInbox(t, path)
		response = httptest.NewRecorder()
		h.ServeHTTP(response, attachmentRequest(homeToken, uid))
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "%PDF-secret-bytes") {
			t.Fatalf("revoked attachment response=%d %q", response.Code, response.Body.String())
		}
	})

	t.Run("stale virtual IDs cannot read replacement messages", func(t *testing.T) {
		// An old virtual ID must not become an ID for a replacement upstream message.
		path := writeFixtureConfig(t, homeToken, privateToken, workToken)
		p := seededFixture()
		r := fixtureRelay(t, path, p)
		identity := fixtureIdentity(t, r, homeToken)
		first, err := r.Snapshot(context.Background(), identity, "INBOX")
		if err != nil || len(first.Messages) != 1 {
			t.Fatalf("snapshot=%#v err=%v", first, err)
		}
		staleUID := first.Messages[0].UID
		p.PagesByAddress["home@example.org"]["INBOX"] = [][]provider.Message{{{
			ID: "replacement", Generation: "home-replaced", Raw: rawMessage("allowed@example.org", "Replacement", "new"),
		}}}
		if _, err := r.Message(context.Background(), identity, "INBOX", staleUID); err == nil {
			t.Fatal("stale virtual ID read replacement mail")
		}
	})

	t.Run("send and unavailable capacities never bypass policy", func(t *testing.T) {
		// Recipient rules and disabled send capacity must prevent all delivery.
		path := writeFixtureConfig(t, homeToken, privateToken, workToken)
		p := seededFixture()
		r := fixtureRelay(t, path, p)
		identity := fixtureIdentity(t, r, homeToken)
		send := sendMessage(r)
		args := sendMessageArgs{From: "home@example.org", To: []string{"recipient@example.org"}, Subject: "Allowed", Body: "body"}
		if _, result, err := send(fixtureContext(identity), nil, args); err != nil || !result.(map[string]any)["sent"].(bool) {
			t.Fatalf("allowed send result=%#v err=%v", result, err)
		}
		if got := len(p.SentSnapshot()); got != 1 {
			t.Fatalf("sent=%d", got)
		}
		args.To = []string{"outside@example.org"}
		if _, _, err := send(fixtureContext(identity), nil, args); err == nil || len(p.SentSnapshot()) != 1 {
			t.Fatal("denied recipient submitted mail")
		}
		p.CapabilitiesByAddress["home@example.org"] = provider.Capabilities{provider.CapacityFolders: true, provider.CapacityList: true, provider.CapacitySearch: true, provider.CapacityRead: true}
		args.To = []string{"recipient@example.org"}
		if _, _, err := send(fixtureContext(identity), nil, args); !errors.Is(err, provider.ErrCapacityUnavailable) || len(p.SentSnapshot()) != 1 {
			t.Fatalf("disabled send err=%v sent=%#v", err, p.SentSnapshot())
		}
	})

	t.Run("unavailable reads and writes produce no page calls", func(t *testing.T) {
		// Unsupported operations fail locally; they never probe the mailbox first.
		path := writeFixtureConfig(t, homeToken, privateToken, workToken)
		p := seededFixture()
		p.CapabilitiesByAddress["home@example.org"] = provider.Capabilities{provider.CapacityFolders: true}
		r := fixtureRelay(t, path, p)
		identity := fixtureIdentity(t, r, homeToken)
		list := messagesWithCursors(r, newCursorStore())
		if _, _, err := list(fixtureContext(identity), nil, listMessagesArgs{Folder: "INBOX"}); !errors.Is(err, provider.ErrCapacityUnavailable) {
			t.Fatalf("read err=%v", err)
		}
		if got := len(p.PageCallSnapshot()); got != 0 {
			t.Fatalf("unavailable read reached provider %d times", got)
		}
		if _, _, err := updateMessageFlags(r)(fixtureContext(identity), nil, updateMessageFlagsArgs{Folder: "INBOX", UID: 1, Flags: []string{"seen"}, Action: "add"}); !errors.Is(err, provider.ErrCapacityUnavailable) {
			t.Fatalf("write err=%v", err)
		}
		if got := len(p.PageCallSnapshot()); got != 0 {
			t.Fatalf("unavailable write reached provider %d times", got)
		}
	})
}

func seededFixture() *fixture.Provider {
	// Two providers and mixed folders give the suite realistic isolation boundaries.
	p := fixture.New()
	p.FoldersByAddress["home@example.org"] = []string{"INBOX", "Private", "Hidden"}
	p.FoldersByAddress["work@example.org"] = []string{"INBOX", "Payroll"}
	p.PagesByAddress["home@example.org"] = map[string][][]provider.Message{
		"INBOX":   {{message("home-1", "home", "allowed@example.org", "Home receipt")}},
		"Private": {{message("home-1", "private", "owner@example.org", "Private note")}},
		"Hidden":  {{message("home-1", "hidden", "owner@example.org", "Hidden note")}},
	}
	p.PagesByAddress["work@example.org"] = map[string][][]provider.Message{
		"INBOX": {{message("work-1", "work", "allowed@example.org", "Work receipt")}},
	}
	return p
}

func writeFixtureConfig(t *testing.T, home, private, work string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	content := fmt.Sprintf(`providers:
  home: &home
    address: home@example.org
    imap: {address: "imap.example.org:993", username: home, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: home, password: [printf, password]}
  work:
    address: work@example.org
    imap: {address: "imap.example.org:993", username: work, password: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: work, password: [printf, password]}
profiles:
  inbox:
    folders: {allow: ["rw:INBOX"]}
    addresses: {allow: ["ro:allowed@example.org", "wo:recipient@example.org"]}
    inbound: {attachments: {allow: ["application/pdf"]}}
  private:
    folders: {allow: ["ro:Private"]}
users:
  home-reader: {provider: home, profile: inbox, token_hash: %s}
  private-reader: {provider: home, profile: private, token_hash: %s}
  work-reader: {provider: work, profile: inbox, token_hash: %s}
`, fixtureTokenHash(home), fixtureTokenHash(private), fixtureTokenHash(work))
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixtureTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", digest)
}

func fixtureRelay(t *testing.T, path string, p *fixture.Provider) *relay.Relay {
	t.Helper()
	return &relay.Relay{ConfigPath: path, Provider: p, Store: mustMCPStore(t)}
}

func fixtureIdentity(t *testing.T, r *relay.Relay, token string) *relay.Identity {
	t.Helper()
	identity, err := r.AuthenticateToken(token)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func fixtureContext(identity *relay.Identity) context.Context {
	return context.WithValue(context.Background(), identityKey{}, identity)
}

func assertFolders(t *testing.T, r *relay.Relay, identity *relay.Identity, want []string) {
	t.Helper()
	got, err := r.Folders(context.Background(), identity)
	if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("folders=%#v err=%v want=%#v", got, err, want)
	}
}

func rotateFixtureToken(t *testing.T, path, oldToken, newToken string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(content), fixtureTokenHash(oldToken), fixtureTokenHash(newToken), 1)
	if updated == string(content) {
		t.Fatal("fixture token was not replaced")
	}
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
}

func denyInbox(t *testing.T, path string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(content), `folders: {allow: ["rw:INBOX"]}`, `folders: {allow: ["ro:Private"]}`, 1)
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
}

func attachmentRequest(token string, uid uint32) *http.Request {
	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/mcp/attachments?folder=INBOX&message_uid=%d&attachment_id=1", uid), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

func message(generation, id, from, subject string) provider.Message {
	return provider.Message{ID: id, Generation: generation, Raw: rawMessage(from, subject, "body")}
}

func rawMessage(from, subject, body string) []byte {
	return []byte(fmt.Sprintf("From: %s\r\nSubject: %s\r\n\r\n%s", from, subject, body))
}

func multipartMessage(from, subject, attachment string) []byte {
	return []byte(fmt.Sprintf("From: %s\r\nSubject: %s\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nBody\r\n--x\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=receipt.pdf\r\n\r\n%s\r\n--x--\r\n", from, subject, attachment))
}

func visibleMessages(generation string, count int) []provider.Message {
	messages := make([]provider.Message, 0, count)
	for i := 1; i <= count; i++ {
		messages = append(messages, message(generation, fmt.Sprintf("%d", i), "allowed@example.org", fmt.Sprintf("Message %d", i)))
	}
	return messages
}
