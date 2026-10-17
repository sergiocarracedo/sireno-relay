package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
)

func TestTemporaryHimalayaConfigIsOwnerOnlyAndCleanedUp(t *testing.T) {
	account := config.Provider{
		Address: "owner@example.org",
		IMAP:    config.Server{Address: "imap.example.org:993", Username: "owner@example.org", Password: config.Password{Value: "upstream-password"}},
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
