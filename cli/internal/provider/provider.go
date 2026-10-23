// Package provider defines the upstream mail boundary. Its types deliberately
// avoid exposing protocol identifiers to the relay, policy, or MCP layers.
package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
)

var ErrCapacityUnavailable = errors.New("capacity unavailable")

// Capacity is an independently advertised upstream operation. A provider must
// not claim a capacity merely because another backend can perform it.
type Capacity string

const (
	CapacityFolders     Capacity = "folders"
	CapacityList        Capacity = "list"
	CapacitySearch      Capacity = "search"
	CapacityRead        Capacity = "read"
	CapacitySend        Capacity = "send"
	CapacityUpdateFlags Capacity = "update_flags"
)

// Capabilities is the provider's per-account capability snapshot.
type Capabilities map[Capacity]bool

func (c Capabilities) Has(capacity Capacity) bool { return c[capacity] }

// Flag and FlagAction are protocol-neutral values accepted by Sireno.
type Flag string

const (
	FlagSeen     Flag = "seen"
	FlagFlagged  Flag = "flagged"
	FlagAnswered Flag = "answered"
)

type FlagAction string

const (
	FlagAdd    FlagAction = "add"
	FlagRemove FlagAction = "remove"
)

// Message is raw upstream mail with opaque provider identifiers. ID and
// Generation never cross the provider boundary to an agent.
type Message struct {
	ID         string
	Generation string
	Raw        []byte
	Flags      []Flag
}

// PageRequest contains only provider-neutral pagination/search input. Page is
// one-based and is never exposed as an MCP cursor.
type PageRequest struct {
	Folder   string
	Page     uint32
	PageSize uint32
	Query    string
}

// Page is a bounded upstream candidate page. HasMore deliberately carries no
// total count so callers cannot expose hidden mailbox cardinality.
type Page struct {
	Messages []Message
	HasMore  bool
}

// Client is the complete, provider-neutral contract. Every adapter must
// implement every method; Capabilities determines which operations are safe to
// invoke for a particular account. This keeps provider compatibility a compile
// time guarantee instead of allowing a provider to advertise an operation that
// it cannot implement.
type Client interface {
	Capabilities(context.Context, config.Provider) (Capabilities, error)
	Folders(context.Context, config.Provider) ([]string, error)
	Messages(context.Context, config.Provider, string) ([]Message, error)
	Page(context.Context, config.Provider, PageRequest) (Page, error)
	Send(context.Context, config.Provider, string, []string, []byte) error
	StoreFlags(context.Context, config.Provider, string, Message, []Flag, FlagAction) error
}

func Supports(ctx context.Context, client Client, account config.Provider, capacity Capacity) error {
	capabilities, err := client.Capabilities(ctx, account)
	if err != nil || !capabilities.Has(capacity) {
		return ErrCapacityUnavailable
	}
	return nil
}

func Secret(ctx context.Context, password config.Password) (string, error) {
	if password.Value != "" {
		return password.Value, nil
	}
	if len(password.Command) == 0 || password.Command[0] == "" {
		return "", errors.New("missing password")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, password.Command[0], password.Command[1:]...)
	cmd.Dir = password.Directory
	cmd.Stderr = io.Discard
	var output bytes.Buffer
	cmd.Stdout = &secretWriter{out: &output}
	if err := cmd.Run(); err != nil {
		return "", errors.New("secret lookup failed")
	}
	value := strings.TrimSuffix(output.String(), "\n")
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("invalid secret")
	}
	return value, nil
}

type secretWriter struct{ out *bytes.Buffer }

func (w *secretWriter) Write(p []byte) (int, error) {
	if w.out.Len()+len(p) > 4096 {
		return 0, errors.New("secret too long")
	}
	return w.out.Write(p)
}
