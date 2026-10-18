package view

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/policy"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
)

type fakeProvider struct{ messages []provider.Message }

func (*fakeProvider) Capabilities(context.Context, config.Provider) (provider.Capabilities, error) {
	return provider.Capabilities{provider.CapacityFolders: true, provider.CapacityList: true, provider.CapacityRead: true, provider.CapacitySend: true}, nil
}
func (f *fakeProvider) Folders(context.Context, config.Provider) ([]string, error) {
	return []string{"INBOX/A", "INBOX/Private"}, nil
}
func (f *fakeProvider) Messages(context.Context, config.Provider, string) ([]provider.Message, error) {
	return f.messages, nil
}
func (f *fakeProvider) Page(_ context.Context, _ config.Provider, request provider.PageRequest) (provider.Page, error) {
	if request.Page != 1 {
		return provider.Page{}, nil
	}
	return provider.Page{Messages: f.messages}, nil
}
func (f *fakeProvider) Send(context.Context, config.Provider, string, []string, []byte) error {
	return nil
}
func (*fakeProvider) StoreFlags(context.Context, config.Provider, string, provider.Message, []provider.Flag, provider.FlagAction) error {
	return nil
}

func TestVirtualIdentityAndHiddenMail(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := config.Profile{Folders: config.Rules{Allow: []string{"ro:INBOX/A"}}, Inbound: config.ContentRule{DeniedSubjects: []string{"Password recovery"}}}
	p, err := policy.Compile(profile)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeProvider{messages: []provider.Message{
		{ID: "100", Generation: "101", Raw: []byte("From: a@example.org\r\nSubject: Invoice\r\n\r\nhello")},
		{ID: "101", Generation: "101", Raw: []byte("From: a@example.org\r\nSubject: Password recovery\r\n\r\nsecret")},
	}}
	svc := Service{Provider: f, Store: store}
	folders, err := svc.Folders(ctx, config.Provider{}, p)
	if err != nil || len(folders) != 1 || folders[0] != "INBOX/A" {
		t.Fatalf("folders=%v err=%v", folders, err)
	}
	read := func() *Snapshot {
		t.Helper()
		result, err := svc.Snapshot(ctx, "bot", "home", "INBOX/A", config.Provider{}, profile, p)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := read()
	if len(first.Messages) != 1 || first.Messages[0].UID != 1 || first.UIDNext != 2 {
		t.Fatalf("unexpected snapshot %+v", first)
	}
	f.messages = append(f.messages, provider.Message{ID: "102", Generation: "101", Raw: []byte("Subject: Password recovery\r\n\r\nsecret2")})
	second := read()
	if second.UIDNext != first.UIDNext || len(second.Messages) != 1 {
		t.Fatal("hidden mail changed virtual counts or IDs")
	}
	restarted, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	svc.Store = restarted
	third := read()
	if third.UIDValidity != first.UIDValidity || third.Messages[0].UID != first.Messages[0].UID {
		t.Fatal("identity changed across restart")
	}
	if _, err := svc.Snapshot(ctx, "bot", "home", "INBOX/Private", config.Provider{}, profile, p); err == nil {
		t.Fatal("private folder available")
	}
}

func TestVirtualIdentityResetsWhenUpstreamUIDValidityChanges(t *testing.T) {
	ctx := context.Background()
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	profile := config.Profile{Folders: config.Rules{Allow: []string{"rw:INBOX"}}}
	p, err := policy.Compile(profile)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeProvider{messages: []provider.Message{{
		ID: "42", Generation: "100", Raw: []byte("From: a@example.org\r\nSubject: Invoice\r\n\r\nfirst"),
	}}}
	svc := Service{Provider: f, Store: store}
	first, err := svc.Snapshot(ctx, "bot", "home", "INBOX", config.Provider{}, profile, p)
	if err != nil {
		t.Fatal(err)
	}
	f.messages = []provider.Message{{
		ID: "42", Generation: "101", Raw: []byte("From: a@example.org\r\nSubject: Replacement\r\n\r\nsecond"),
	}}
	second, err := svc.Snapshot(ctx, "bot", "home", "INBOX", config.Provider{}, profile, p)
	if err != nil {
		t.Fatal(err)
	}
	if first.UIDValidity == second.UIDValidity {
		t.Fatal("upstream UIDVALIDITY change retained virtual namespace")
	}
	if len(second.Messages) != 1 || second.Messages[0].UID == first.Messages[0].UID || second.Messages[0].Mail.Subject != "Replacement" {
		t.Fatalf("unexpected reset snapshot: %+v", second)
	}
}

func TestPermittedRecipientDoesNotAuthorizeUnpermittedSender(t *testing.T) {
	profile := config.Profile{
		Folders: config.Rules{Allow: []string{"ro:INBOX"}},
		Inbound: config.ContentRule{Addresses: config.Rules{Allow: []string{"bot@example.org"}}},
	}
	p, err := policy.Compile(profile)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Provider: &fakeProvider{messages: []provider.Message{{
		ID: "1", Generation: "g", Raw: []byte("From: unapproved@example.org\r\nTo: bot@example.org\r\nSubject: Do not expose\r\n\r\nBody"),
	}}}, Store: store}
	snapshot, err := service.Snapshot(context.Background(), "bot", "home", "INBOX", config.Provider{}, profile, p)
	if err != nil || len(snapshot.Messages) != 0 {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
}
