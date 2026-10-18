package policy

import (
	"testing"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
)

func TestReadAndSend(t *testing.T) {
	p, err := Compile(config.Profile{
		Folders:  config.Rules{Allow: []string{"rw:INBOX/*"}, Deny: []string{"rw:INBOX/Private"}},
		Inbound:  config.ContentRule{Addresses: config.Rules{Allow: []string{"alice@example.org", "bob@example.org"}, Deny: []string{"blocked@example.org"}}},
		Outbound: config.ContentRule{Addresses: config.Rules{Allow: []string{"trusted@example.org"}, Deny: []string{"bad@example.org"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		folder    string
		addresses []string
		want      bool
	}{
		{"INBOX/Invoices", []string{"alice@example.org"}, true},
		{"INBOX/Invoices", []string{"alice@example.org", "bob@example.org"}, true},
		{"INBOX/Private", []string{"alice@example.org"}, false},
		{"INBOX/Invoices", []string{"other@example.org"}, false},
		{"INBOX/Invoices", []string{"alice@example.org", "other@example.org"}, false},
		{"INBOX/Invoices", []string{"alice@example.org", "blocked@example.org"}, false},
	} {
		if got := p.ReadMessage(tc.folder, tc.addresses); got != tc.want {
			t.Errorf("read %q %v: got %v want %v", tc.folder, tc.addresses, got, tc.want)
		}
	}
	if !p.WriteFolder("INBOX/Invoices") || p.WriteFolder("INBOX/Private") {
		t.Fatal("folder write boundary failed")
	}
	for _, tc := range []struct {
		addresses []string
		want      bool
	}{
		{[]string{"trusted@example.org"}, true},
		{[]string{"trusted@example.org", "bad@example.org"}, false},
		{[]string{"alice@example.org"}, false},
		{nil, false},
	} {
		if got := p.Send(tc.addresses); got != tc.want {
			t.Errorf("send %v: got %v want %v", tc.addresses, got, tc.want)
		}
	}
}

func TestDefaultDenyAndPatterns(t *testing.T) {
	p, err := Compile(config.Profile{Folders: config.Rules{Allow: []string{"ro:INBOX/Fact*"}, Deny: []string{"ro:re:(?i)private"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.ReadMessage("INBOX/Facturas", nil) || p.ReadMessage("INBOX/FactPrivate", nil) || p.ReadMessage("INBOX/Other", nil) {
		t.Fatal("unexpected read access")
	}
	if p.WriteFolder("INBOX/Facturas") || p.Send([]string{"x@example.org"}) {
		t.Fatal("implicit write access")
	}
	if _, err := Compile(config.Profile{Folders: config.Rules{Allow: []string{"wo:INBOX"}}}); err == nil {
		t.Fatal("accepted write-only folder")
	}
	if _, err := Compile(config.Profile{Inbound: config.ContentRule{Addresses: config.Rules{Allow: []string{"re:["}}}}); err == nil {
		t.Fatal("accepted invalid regex")
	}
	onlySend, err := Compile(config.Profile{Folders: config.Rules{Allow: []string{"ro:INBOX"}}, Outbound: config.ContentRule{Addresses: config.Rules{Allow: []string{"recipient@example.org"}}}})
	if err != nil || !onlySend.ReadMessage("INBOX", []string{"unknown@example.org"}) {
		t.Fatal("send-only address rule should not constrain incoming reads")
	}
	caseSafe, err := Compile(config.Profile{Folders: config.Rules{Allow: []string{"ro:*"}, Deny: []string{"ro:INBOX/Private"}}})
	if err != nil || caseSafe.ReadMessage("inbox/Private", nil) {
		t.Fatal("mixed case INBOX bypassed deny")
	}
}
