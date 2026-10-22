package onboard

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
)

func TestRunCreatesReadOnlyProfileAndSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sireno", "config.yml")
	marker := filepath.Join(dir, "sireno", "onboard")
	input := strings.NewReader("me@example.org\nimap.example.org:993\n\nsmtp.example.org:465\n\nmerchant@example.org\nrecipient@example.org\n\n")
	var output bytes.Buffer
	secrets := []string{"imap-secret", "smtp-secret"}
	_, err := Run(context.Background(), Options{ConfigPath: path, MarkerPath: marker, Input: input, Output: &output, ReadSecret: func(string) (string, error) { value := secrets[0]; secrets = secrets[1:]; return value, nil }, ListFolders: func(context.Context, config.Provider) ([]string, error) { return []string{"INBOX", "Receipts"}, nil }, SelectFolders: func(folders []string) ([]string, error) { return folders, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "imap-secret") || strings.Contains(output.String(), "smtp-secret") {
		t.Fatal("secret was printed")
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := cfg.Profiles["default"]
	if len(profile.Folders.Allow) != 2 || profile.Folders.Allow[0] != "ro:INBOX" || len(profile.Inbound.Addresses.Allow) != 1 || profile.Inbound.Addresses.Allow[0] != "merchant@example.org" {
		t.Fatalf("profile=%#v", profile)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustRead(t, path)), "imap-secret") {
		t.Fatal("secret written to config")
	}
}

func TestRunRetriesProviderCredentialsAfterFolderFailure(t *testing.T) {
	dir := t.TempDir()
	input := strings.NewReader("bad@example.org\nimap.bad:993\n\nsmtp.bad:465\n\nyes\ngood@example.org\nimap.good:993\n\nsmtp.good:465\n\n\n\n\n")
	secrets := []string{"bad-imap", "bad-smtp", "good-imap", "good-smtp"}
	var attempts int
	_, err := Run(context.Background(), Options{ConfigPath: filepath.Join(dir, "sireno", "config.yml"), MarkerPath: filepath.Join(dir, "sireno", "onboard"), Input: input, Output: &bytes.Buffer{}, ReadSecret: func(string) (string, error) { value := secrets[0]; secrets = secrets[1:]; return value, nil }, ListFolders: func(_ context.Context, account config.Provider) ([]string, error) {
		attempts++
		if attempts == 1 {
			return nil, os.ErrNotExist
		}
		if account.Address != "good@example.org" {
			t.Fatalf("account=%q", account.Address)
		}
		return []string{"INBOX"}, nil
	}, SelectFolders: func(folders []string) ([]string, error) { return folders, nil }})
	if err != nil || attempts != 2 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}

func TestProviderUsesIMAPPasswordWhenSMTPPasswordIsBlank(t *testing.T) {
	input := strings.NewReader("me@example.org\nimap.example.org:993\n\nsmtp.example.org:465\n\n")
	secrets := []string{"imap-secret", ""}
	prompt := newPrompter(input, io.Discard, nil)
	_, imapPassword, smtpPassword, err := prompt.provider(func(string) (string, error) {
		value := secrets[0]
		secrets = secrets[1:]
		return value, nil
	}, config.Provider{}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if imapPassword != "imap-secret" || smtpPassword != imapPassword {
		t.Fatalf("imapPassword=%q smtpPassword=%q", imapPassword, smtpPassword)
	}
}

func TestProviderReusesPreviousValuesWhenRetrying(t *testing.T) {
	input := strings.NewReader("\n\n\n\n\n")
	secrets := []string{"", ""}
	previous := config.Provider{Address: "me@example.org", IMAP: config.Server{Address: "imap.example.org:993", Username: "imap-user"}, SMTP: config.Server{Address: "smtp.example.org:465", Username: "smtp-user"}}
	prompt := newPrompter(input, io.Discard, nil)
	provider, imapPassword, smtpPassword, err := prompt.provider(func(string) (string, error) {
		value := secrets[0]
		secrets = secrets[1:]
		return value, nil
	}, previous, "imap-secret", "smtp-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(provider, previous) || imapPassword != "imap-secret" || smtpPassword != "smtp-secret" {
		t.Fatalf("provider=%#v imapPassword=%q smtpPassword=%q", provider, imapPassword, smtpPassword)
	}
}

func TestRequiredPromptRetriesAfterBlankInput(t *testing.T) {
	var output bytes.Buffer
	prompt := newPrompter(strings.NewReader("\nvalid@example.org\n"), &output, nil)
	value, err := prompt.required("Account email address")
	if err != nil || value != "valid@example.org" || !strings.Contains(output.String(), "is required") {
		t.Fatalf("value=%q err=%v output=%q", value, err, output.String())
	}
}

func TestEmailPromptRetriesAfterInvalidInput(t *testing.T) {
	var output bytes.Buffer
	prompt := newPrompter(strings.NewReader("not-an-email\nvalid@example.org\n"), &output, nil)
	value, err := prompt.email("Account email address")
	if err != nil || value != "valid@example.org" || !strings.Contains(output.String(), "valid email") {
		t.Fatalf("value=%q err=%v output=%q", value, err, output.String())
	}
}

func TestEmailPromptKeepsInvalidValueEditable(t *testing.T) {
	var defaults []string
	values := []string{"not-an-email", "valid@example.org"}
	prompt := newPrompter(strings.NewReader(""), io.Discard, func(_ string, fallback string) (string, error) {
		defaults = append(defaults, fallback)
		value := values[0]
		values = values[1:]
		return value, nil
	})
	if _, err := prompt.email("Account email address"); err != nil {
		t.Fatal(err)
	}
	if len(defaults) != 2 || defaults[1] != "not-an-email" {
		t.Fatalf("defaults=%q", defaults)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
