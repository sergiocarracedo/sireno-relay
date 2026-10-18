package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"gopkg.in/yaml.v3"
)

// NewToken returns an opaque bearer token and the only value stored in config.
func NewToken() (string, string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(value)
	hash := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(hash[:]), nil
}

// Write creates an owner-only configuration without replacing an existing file.
func Write(path string, cfg *Config) error {
	if cfg == nil {
		return errors.New("configuration is required")
	}
	if _, err := cfg.Validate(); err != nil {
		return err
	}
	if err := secureParent(filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("configuration already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// RotateToken atomically replaces one user's verifier and returns the new raw
// token only after the change is durable.
func RotateToken(path, user string) (string, error) {
	cfg, _, err := Load(path)
	if err != nil {
		return "", err
	}
	if _, ok := cfg.Users[user]; !ok {
		return "", fmt.Errorf("unknown user %q", user)
	}
	token, hash, err := NewToken()
	if err != nil {
		return "", err
	}
	u := cfg.Users[user]
	u.TokenHash = hash
	cfg.Users[user] = u
	if _, err := cfg.Validate(); err != nil {
		return "", err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	if err := writeAtomic(path, data); err != nil {
		return "", err
	}
	return token, nil
}

func WriteSecret(path, value string) error {
	if value == "" {
		return errors.New("secret is required")
	}
	if err := secureParent(filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("secret already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeAtomic(path, []byte(value+"\n"))
}

func secureParent(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("configuration directory must be owner-only")
	}
	if owner, ok := info.Sys().(*syscall.Stat_t); ok && owner.Uid != uint32(os.Geteuid()) {
		return errors.New("configuration directory must be owned by the relay process")
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".sireno-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
