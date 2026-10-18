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
