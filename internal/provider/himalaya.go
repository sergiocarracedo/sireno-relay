package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
)

// TemporaryHimalayaConfig creates the smallest Himalaya v2 configuration that
// can access one configured IMAP provider. The returned cleanup function must
// run as soon as the command exits: the file contains the resolved upstream
// password and is intentionally never logged or persisted in Sireno state.
func TemporaryHimalayaConfig(ctx context.Context, account config.Provider) (path string, cleanup func(), err error) {
	password, err := Secret(ctx, account.IMAP.Password)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "sireno-himalaya-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	if err := os.Chmod(dir, 0700); err != nil {
		cleanup()
		return "", nil, err
	}
	path = filepath.Join(dir, "config.toml")
	contents := fmt.Sprintf(`[accounts.sireno]
default = true
email = %s
imap.server = %s
imap.sasl.plain.username = %s
imap.sasl.plain.password.raw = %s
`, tomlString(account.Address), tomlString("imaps://"+account.IMAP.Address), tomlString(account.IMAP.Username), tomlString(password))
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func tomlString(value string) string { return strconv.Quote(value) }
