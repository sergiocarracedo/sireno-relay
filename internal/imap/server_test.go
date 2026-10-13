package imap

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/relay"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/view"
)

type fakeProvider struct{ messages []provider.Message }

func (*fakeProvider) Folders(context.Context, config.Provider) ([]string, error) {
	return []string{"INBOX/A", "INBOX/Private"}, nil
}
func (f *fakeProvider) Messages(context.Context, config.Provider, string) ([]provider.Message, error) {
	return f.messages, nil
}
func (*fakeProvider) Send(context.Context, config.Provider, string, []string, []byte) error {
	return nil
}

func TestIMAPIsolatedView(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.yml")
	cfg := `providers:
  home:
    address: me@example.org
    imap: {address: "imap.example.org:993", username: me, password_command: [printf, password]}
    smtp: {address: "smtp.example.org:465", username: me, password_command: [printf, password]}
profiles:
  safe:
    folders: {allow: ["ro:INBOX/A"]}
    inbound: {denied_subjects: ["Password recovery"]}
users:
  bot: {provider: home, profile: safe, password_command: [printf, secret]}
`
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := view.Open("")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeProvider{messages: []provider.Message{
		{UID: 10, Raw: []byte("From: a@example.org\r\nSubject: Invoice\r\n\r\nhello")},
		{UID: 11, Raw: []byte("From: a@example.org\r\nSubject: Password recovery\r\n\r\nsecret")},
	}}
	srv := Server(&relay.Relay{ConfigPath: path, Provider: f, Store: store})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	defer srv.Close()
	client, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Login("bot", "secret").Wait(); err != nil {
		t.Fatal(err)
	}
	boxes, err := client.List("", "*", nil).Collect()
	if err != nil || len(boxes) != 1 || boxes[0].Mailbox != "INBOX/A" {
		t.Fatalf("boxes=%v err=%v", boxes, err)
	}
	if _, err := client.Select("INBOX/Private", nil).Wait(); err == nil {
		t.Fatal("private folder selectable")
	}
	selected, err := client.Select("INBOX/A", nil).Wait()
	if err != nil || selected.NumMessages != 1 || selected.UIDNext != 2 {
		t.Fatalf("select=%+v err=%v", selected, err)
	}
	search, err := client.Search(&imap.SearchCriteria{Text: []string{"secret"}}, nil).Wait()
	if err != nil || len(search.AllSeqNums()) != 0 {
		t.Fatalf("secret search=%+v err=%v", search, err)
	}
	var nums imap.SeqSet
	nums.AddNum(1)
	fetched, err := client.Fetch(nums, &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{{Peek: true}}, UID: true, RFC822Size: true}).Collect()
	if err != nil || len(fetched) != 1 || string(fetched[0].BodySection[0].Bytes) == "" {
		t.Fatalf("fetch=%+v err=%v", fetched, err)
	}
	if err := client.Store(nums, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err == nil {
		t.Fatal("write should be denied")
	}
}
