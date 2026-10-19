package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
)

const maxCommandOutput = 4 * 1024 * 1024

// CommandRunner isolates Himalaya process execution for fixture-based tests.
type CommandRunner interface {
	Run(context.Context, []byte, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "himalaya", args...)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stderr = io.Discard
	var output limitedBuffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return nil, errors.New("himalaya command failed")
	}
	return output.Bytes(), nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxCommandOutput {
		return 0, errors.New("himalaya output too large")
	}
	return b.Buffer.Write(p)
}

// Himalaya is the sole production upstream provider. Command details remain
// here; relay, policy, views, and MCP tools use only provider-neutral types.
type Himalaya struct{ Runner CommandRunner }

var _ Client = Himalaya{}

func (h Himalaya) runner() CommandRunner {
	if h.Runner != nil {
		return h.Runner
	}
	return execRunner{}
}

func (Himalaya) Capabilities(context.Context, config.Provider) (Capabilities, error) {
	// Copy, move, deletion, append, and mailbox mutation stay disabled until
	// target-scoped behavior is proven across supported upstream backends.
	return Capabilities{
		CapacityFolders:     true,
		CapacityList:        true,
		CapacitySearch:      true,
		CapacityRead:        true,
		CapacitySend:        true,
		CapacityUpdateFlags: true,
	}, nil
}

func (h Himalaya) Folders(ctx context.Context, account config.Provider) ([]string, error) {
	output, err := h.run(ctx, account, nil, "--json", "mailbox", "list")
	if err != nil {
		return nil, err
	}
	type mailbox struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	var items []mailbox
	if err := decodeJSONArray(output, "mailboxes", &items); err != nil {
		return nil, fmt.Errorf("invalid himalaya mailbox response: %w", err)
	}
	folders := make([]string, 0, len(items))
	for _, item := range items {
		name := item.Name
		if name == "" {
			name = item.ID
		}
		if name == "" {
			return nil, errors.New("invalid himalaya mailbox response")
		}
		folders = append(folders, name)
	}
	return folders, nil
}

// Messages preserves the legacy first-page read used by targeted virtual-ID
// resolution. New list/search callers use Page.
func (h Himalaya) Messages(ctx context.Context, account config.Provider, folder string) ([]Message, error) {
	page, err := h.Page(ctx, account, PageRequest{Folder: folder, Page: 1, PageSize: 20})
	if err != nil {
		return nil, err
	}
	return page.Messages, nil
}

// Page asks Himalaya for bounded candidates, then reads each candidate without
// marking it seen. Query uses Himalaya's documented shared query language and
// is passed as arguments (never to a shell).
func (h Himalaya) Page(ctx context.Context, account config.Provider, request PageRequest) (Page, error) {
	if request.Folder == "" || request.Page == 0 || request.PageSize == 0 || request.PageSize > 20 || len(request.Query) > 512 || strings.ContainsAny(request.Query, "\r\n\x00") {
		return Page{}, errors.New("invalid page request")
	}
	command := "list"
	if request.Query != "" {
		command = "search"
	}
	args := []string{"--json", "envelope", command, "--mailbox", request.Folder, "--page", strconv.FormatUint(uint64(request.Page), 10), "--page-size", strconv.FormatUint(uint64(request.PageSize), 10)}
	if request.Query != "" {
		// Query terms are agent-controlled. The option terminator keeps them from
		// overriding the temporary owner-controlled Himalaya configuration.
		args = append(args, "--")
		args = append(args, strings.Fields(request.Query)...)
	}
	output, err := h.run(ctx, account, nil, args...)
	if err != nil {
		return Page{}, errors.New("himalaya envelope listing failed")
	}
	type envelope struct {
		ID    json.RawMessage   `json:"id"`
		Flags []json.RawMessage `json:"flags"`
	}
	var envelopes []envelope
	if err := decodeJSONArray(output, "envelopes", &envelopes); err != nil {
		return Page{}, fmt.Errorf("invalid himalaya envelope response: %w", err)
	}
	if len(envelopes) > int(request.PageSize) {
		return Page{}, errors.New("invalid himalaya envelope response")
	}
	messages := make([]Message, 0, len(envelopes))
	for _, envelope := range envelopes {
		id, err := jsonID(envelope.ID)
		if err != nil {
			return Page{}, errors.New("invalid himalaya envelope response")
		}
		raw, err := h.run(ctx, account, nil, "message", "read", "--mailbox", request.Folder, "--raw", id)
		if err != nil {
			return Page{}, errors.New("himalaya message read failed")
		}
		if len(raw) == 0 || len(raw) > maxCommandOutput {
			continue
		}
		messages = append(messages, Message{ID: id, Generation: request.Folder, Raw: raw, Flags: knownFlags(envelope.Flags)})
	}
	return Page{Messages: messages, HasMore: len(envelopes) == int(request.PageSize)}, nil
}

// decodeJSONArray accepts both Himalaya v2's named result object and the
// bare-array format emitted by earlier supported releases.
func decodeJSONArray[T any](output []byte, field string, result *[]T) error {
	if err := json.Unmarshal(output, result); err == nil {
		return nil
	}
	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal(output, &wrapped); err != nil {
		return err
	}
	value, ok := wrapped[field]
	if !ok {
		if _, hasError := wrapped["error"]; hasError {
			return errors.New("himalaya returned a JSON error")
		}
		return errors.New("missing result field")
	}
	return json.Unmarshal(value, result)
}

func (h Himalaya) Send(ctx context.Context, account config.Provider, _ string, _ []string, raw []byte) error {
	if len(raw) == 0 || len(raw) > maxCommandOutput {
		return errors.New("invalid message")
	}
	_, err := h.run(ctx, account, raw, "message", "send", "--no-save")
	return err
}

func (h Himalaya) StoreFlags(ctx context.Context, account config.Provider, folder string, target Message, flags []Flag, action FlagAction) error {
	if folder == "" || target.ID == "" || len(flags) == 0 || (action != FlagAdd && action != FlagRemove) {
		return errors.New("invalid flag update")
	}
	command := "add"
	if action == FlagRemove {
		command = "remove"
	}
	args := []string{"flag", command, "--mailbox", folder}
	for _, flag := range flags {
		args = append(args, "--flag", string(flag))
	}
	args = append(args, target.ID)
	_, err := h.run(ctx, account, nil, args...)
	return err
}

func (h Himalaya) run(ctx context.Context, account config.Provider, stdin []byte, args ...string) ([]byte, error) {
	path, cleanup, err := TemporaryHimalayaConfig(ctx, account)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	base := []string{"--config", path, "--account", "sireno", "--log-level", "off"}
	output, err := h.runner().Run(ctx, stdin, append(base, args...)...)
	if err != nil {
		return nil, errors.New("himalaya command failed")
	}
	return output, nil
}

func jsonID(value json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(value, &text); err == nil && text != "" {
		return text, nil
	}
	var number json.Number
	if err := json.Unmarshal(value, &number); err == nil && number.String() != "" {
		return number.String(), nil
	}
	return "", errors.New("missing ID")
}

func knownFlags(values []json.RawMessage) []Flag {
	flags := make([]Flag, 0, len(values))
	for _, value := range values {
		var name string
		if json.Unmarshal(value, &name) != nil {
			var wrapped struct {
				Raw  string `json:"raw"`
				IANA string `json:"iana"`
			}
			if json.Unmarshal(value, &wrapped) != nil {
				continue
			}
			name = wrapped.IANA
			if name == "" {
				name = wrapped.Raw
			}
		}
		name = strings.TrimPrefix(strings.ToLower(name), "\\")
		switch Flag(name) {
		case FlagSeen, FlagFlagged, FlagAnswered:
			flags = append(flags, Flag(name))
		}
	}
	return flags
}

// TemporaryHimalayaConfig creates an owner-only ephemeral configuration. It
// contains credentials and is never logged or persisted in Sireno state.
func TemporaryHimalayaConfig(ctx context.Context, account config.Provider) (path string, cleanup func(), err error) {
	password, err := Secret(ctx, account.IMAP.Password)
	if err != nil {
		return "", nil, err
	}
	smtpPassword, err := Secret(ctx, account.SMTP.Password)
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
smtp.server = %s
smtp.sasl.plain.username = %s
smtp.sasl.plain.password.raw = %s
`, tomlString(account.Address), tomlString("imaps://"+account.IMAP.Address), tomlString(account.IMAP.Username), tomlString(password), tomlString("smtps://"+account.SMTP.Address), tomlString(account.SMTP.Username), tomlString(smtpPassword))
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func tomlString(value string) string { return strconv.Quote(value) }
