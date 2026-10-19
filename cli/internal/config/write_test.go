package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndRotateToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sireno", "config.yml")
	_, firstHash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Providers: map[string]Provider{"home": {Address: "me@example.org", IMAP: Server{Address: "imap.example.org:993", Username: "me", Password: Password{Command: []string{"cat", "/safe/imap"}}}, SMTP: Server{Address: "smtp.example.org:465", Username: "me", Password: Password{Command: []string{"cat", "/safe/smtp"}}}}}, Profiles: map[string]Profile{"default": {Folders: Rules{Allow: []string{"ro:INBOX"}}}}, Users: map[string]User{"agent": {Provider: "home", Profile: "default", TokenHash: firstHash}}}
	if err := Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("config mode=%v err=%v", info.Mode(), err)
	}
	rotated, err := RotateToken(path, "agent")
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(rotated))
	if loaded.Users["agent"].TokenHash != hex.EncodeToString(hash[:]) || loaded.Users["agent"].TokenHash == firstHash {
		t.Fatal("token was not rotated")
	}
	if got := loaded.Providers["home"].IMAP.Password.Command; len(got) != 2 || got[1] != "/safe/imap" {
		t.Fatalf("password command changed: %v", got)
	}
}
