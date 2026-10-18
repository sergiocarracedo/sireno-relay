package fixture

import (
	"context"
	"testing"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
)

func TestProviderRecordsBoundedPagesAndSends(t *testing.T) {
	p := New()
	p.FoldersValue = []string{"INBOX", "Private"}
	p.Pages["INBOX"] = [][]provider.Message{{{ID: "1", Generation: "g", Raw: []byte("From: a@example.org\r\nSubject: One\r\n\r\nBody")}}, {{ID: "2", Generation: "g", Raw: []byte("From: a@example.org\r\nSubject: Two\r\n\r\nBody")}}}
	first, err := p.Page(context.Background(), config.Provider{}, provider.PageRequest{Folder: "INBOX", Page: 1, PageSize: 20})
	if err != nil || len(first.Messages) != 1 || !first.HasMore {
		t.Fatalf("page=%#v err=%v", first, err)
	}
	if err := p.Send(context.Background(), config.Provider{}, "", nil, []byte("message")); err != nil {
		t.Fatal(err)
	}
	if len(p.Calls) != 1 || len(p.Sent) != 1 {
		t.Fatalf("calls=%d sends=%d", len(p.Calls), len(p.Sent))
	}
}

func TestProviderSeparatesAccountFixturesAndReturnsCopies(t *testing.T) {
	p := New()
	p.FoldersByAddress["one@example.org"] = []string{"INBOX"}
	p.PagesByAddress["one@example.org"] = map[string][][]provider.Message{
		"INBOX": {{{ID: "one", Generation: "g", Raw: []byte("From: one@example.org\r\n\r\none")}}},
	}
	p.PagesByAddress["two@example.org"] = map[string][][]provider.Message{
		"INBOX": {{{ID: "two", Generation: "g", Raw: []byte("From: two@example.org\r\n\r\ntwo")}}},
	}
	one := config.Provider{Address: "one@example.org"}
	two := config.Provider{Address: "two@example.org"}
	first, err := p.Page(context.Background(), one, provider.PageRequest{Folder: "INBOX", Page: 1, PageSize: 20})
	if err != nil || first.Messages[0].ID != "one" {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	first.Messages[0].ID = "changed"
	first.Messages[0].Raw[0] = 'X'
	again, err := p.Page(context.Background(), one, provider.PageRequest{Folder: "INBOX", Page: 1, PageSize: 20})
	if err != nil || again.Messages[0].ID != "one" || again.Messages[0].Raw[0] != 'F' {
		t.Fatalf("again=%#v err=%v", again, err)
	}
	other, err := p.Page(context.Background(), two, provider.PageRequest{Folder: "INBOX", Page: 1, PageSize: 20})
	if err != nil || other.Messages[0].ID != "two" {
		t.Fatalf("other=%#v err=%v", other, err)
	}
	calls := p.PageCallSnapshot()
	if len(calls) != 3 || calls[0].Address != one.Address || calls[2].Address != two.Address {
		t.Fatalf("calls=%#v", calls)
	}
}
