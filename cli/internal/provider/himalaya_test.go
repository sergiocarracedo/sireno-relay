package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
)

type recordingRunner struct {
	args   [][]string
	stdin  [][]byte
	output []byte
	err    error
}

type sequenceRunner struct {
	args    [][]string
	outputs [][]byte
}

type failingSecondRunner struct{ calls int }

func (r *failingSecondRunner) Run(_ context.Context, _ []byte, _ ...string) ([]byte, error) {
	r.calls++
	if r.calls == 1 {
		return []byte(`{"envelopes":[{"id":42}]}`), nil
	}
	return nil, errors.New("upstream secret")
}

func (r *sequenceRunner) Run(_ context.Context, _ []byte, args ...string) ([]byte, error) {
	r.args = append(r.args, append([]string(nil), args...))
	if len(r.outputs) == 0 {
		return nil, errors.New("unexpected command")
	}
	output := r.outputs[0]
	r.outputs = r.outputs[1:]
	return output, nil
}

func (r *recordingRunner) Run(_ context.Context, stdin []byte, args ...string) ([]byte, error) {
	r.args = append(r.args, append([]string(nil), args...))
	r.stdin = append(r.stdin, append([]byte(nil), stdin...))
	if r.err != nil {
		return nil, r.err
	}
	return r.output, nil
}

func TestTemporaryHimalayaConfigIsOwnerOnlyAndCleanedUp(t *testing.T) {
	account := config.Provider{
		Address: "owner@example.org",
		IMAP:    config.Server{Address: "imap.example.org:993", Username: "owner@example.org", Password: config.Password{Value: "upstream-password"}},
		SMTP:    config.Server{Address: "smtp.example.org:465", Username: "owner@example.org", Password: config.Password{Value: "smtp-password"}},
	}
	path, cleanup, err := TemporaryHimalayaConfig(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("temporary directory permissions: info=%v err=%v", info, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("temporary file permissions: info=%v err=%v", info, err)
	}
	text := string(contents)
	for _, want := range []string{
		`imap.server = "imaps://imap.example.org:993"`,
		`imap.sasl.plain.username = "owner@example.org"`,
		`imap.sasl.plain.password.raw = "upstream-password"`,
		`smtp.server = "smtps://smtp.example.org:465"`,
		`smtp.sasl.plain.password.raw = "smtp-password"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing config entry %q", want)
		}
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary configuration still exists: %v", err)
	}
}

func TestHimalayaFoldersUsesJSONAndDoesNotExposeConfigPath(t *testing.T) {
	runner := &recordingRunner{output: []byte(`{"mailboxes":[{"name":"INBOX"},{"id":"archive"}]}`)}
	account := config.Provider{
		Address: "owner@example.org",
		IMAP:    config.Server{Address: "imap.example.org:993", Username: "owner@example.org", Password: config.Password{Value: "upstream-password"}},
		SMTP:    config.Server{Address: "smtp.example.org:465", Username: "owner@example.org", Password: config.Password{Value: "smtp-password"}},
	}
	folders, err := (Himalaya{Runner: runner}).Folders(context.Background(), account)
	if err != nil || strings.Join(folders, ",") != "INBOX,archive" {
		t.Fatalf("folders=%v err=%v", folders, err)
	}
	if len(runner.args) != 1 || !containsSequence(runner.args[0], "--json", "mailbox", "list") {
		t.Fatalf("unexpected command: %q", runner.args)
	}
	for _, arg := range runner.args[0] {
		if strings.Contains(arg, "upstream-password") || strings.Contains(arg, "smtp-password") {
			t.Fatalf("secret appeared in command arguments: %q", runner.args)
		}
	}
}

func TestHimalayaCommandFailureIsRedacted(t *testing.T) {
	runner := &recordingRunner{err: errors.New("bad password secret-value")}
	account := config.Provider{Address: "owner@example.org", IMAP: config.Server{Address: "imap:993", Username: "owner", Password: config.Password{Value: "secret-value"}}, SMTP: config.Server{Address: "smtp:465", Username: "owner", Password: config.Password{Value: "smtp-secret"}}}
	if _, err := (Himalaya{Runner: runner}).Folders(context.Background(), account); err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("error leaked secret: %v", err)
	}
}

func TestHimalayaDoesNotAdvertiseUnprovenWrites(t *testing.T) {
	capabilities, err := (Himalaya{}).Capabilities(context.Background(), config.Provider{})
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Has(CapacityUpdateFlags) {
		t.Fatal("Himalaya advertised uncharacterized flag updates")
	}
}

func TestHimalayaPageUsesBoundedSearchAndTargetedReads(t *testing.T) {
	runner := &sequenceRunner{outputs: [][]byte{
		[]byte(`{"envelopes":[{"id":42,"flags":[{"raw":"\\Seen","iana":"seen"}]}]}`),
		[]byte("From: sender@example.org\r\nSubject: Receipt\r\n\r\nBody"),
	}}
	account := config.Provider{Address: "owner@example.org", IMAP: config.Server{Address: "imap:993", Username: "owner", Password: config.Password{Value: "secret"}}, SMTP: config.Server{Address: "smtp:465", Username: "owner", Password: config.Password{Value: "smtp-secret"}}}
	page, err := (Himalaya{Runner: runner}).Page(context.Background(), account, PageRequest{Folder: "INBOX", Page: 2, PageSize: 20, Query: "from billing@example.org"})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != "42" || page.Messages[0].Flags[0] != FlagSeen || page.HasMore {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	if len(runner.args) != 2 || !containsSequence(runner.args[0], "envelope", "search", "--mailbox", "INBOX", "--page", "2", "--page-size", "20", "from", "billing@example.org") || !containsSequence(runner.args[1], "message", "read", "--mailbox", "INBOX", "--raw", "42") {
		t.Fatalf("unexpected commands: %#v", runner.args)
	}
}

func TestKnownFlagsAcceptsLegacyStrings(t *testing.T) {
	flags := knownFlags([]json.RawMessage{json.RawMessage(`"seen"`), json.RawMessage(`"flagged"`)})
	if len(flags) != 2 || flags[0] != FlagSeen || flags[1] != FlagFlagged {
		t.Fatalf("flags=%#v", flags)
	}
}

func TestHimalayaPageClassifiesSafeOperationFailures(t *testing.T) {
	account := config.Provider{Address: "owner@example.org", IMAP: config.Server{Address: "imap.example.org:993", Username: "owner", Password: config.Password{Value: "secret"}}, SMTP: config.Server{Address: "smtp.example.org:465", Username: "owner", Password: config.Password{Value: "smtp-secret"}}}
	for name, runner := range map[string]CommandRunner{
		"envelope listing": &recordingRunner{err: errors.New("upstream secret")},
		"message read":     &failingSecondRunner{},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (Himalaya{Runner: runner}).Page(context.Background(), account, PageRequest{Folder: "INBOX", Page: 1, PageSize: 20})
			want := "himalaya " + name + " failed"
			if err == nil || err.Error() != want || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestDecodeJSONArrayAcceptsHimalayaV2AndLegacyResults(t *testing.T) {
	type item struct {
		Name string `json:"name"`
	}
	for name, output := range map[string]string{
		"v2 wrapper":   `{"mailboxes":[{"name":"INBOX"}]}`,
		"legacy array": `[ {"name":"INBOX"} ]`,
	} {
		t.Run(name, func(t *testing.T) {
			var items []item
			if err := decodeJSONArray([]byte(output), "mailboxes", &items); err != nil || len(items) != 1 || items[0].Name != "INBOX" {
				t.Fatalf("items=%#v err=%v", items, err)
			}
		})
	}
}

func TestDecodeJSONArrayRedactsHimalayaJSONErrors(t *testing.T) {
	type item struct{ Name string }
	var items []item
	err := decodeJSONArray([]byte(`{"error":"upstream password must not leak"}`), "mailboxes", &items)
	if err == nil || !strings.Contains(err.Error(), "JSON error") || strings.Contains(err.Error(), "password") {
		t.Fatalf("error=%v", err)
	}
}

func containsSequence(values []string, sequence ...string) bool {
	for i := range values {
		if i+len(sequence) > len(values) {
			break
		}
		matched := true
		for j := range sequence {
			if values[i+j] != sequence[j] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
