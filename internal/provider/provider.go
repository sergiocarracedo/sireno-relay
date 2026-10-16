package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	smtp "github.com/emersion/go-smtp"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/mail"
)

type Message struct {
	UID         imap.UID
	UIDValidity uint32
	Raw         []byte
	Flags       []imap.Flag
}

type Client interface {
	Folders(context.Context, config.Provider) ([]string, error)
	Messages(context.Context, config.Provider, string) ([]Message, error)
	Send(context.Context, config.Provider, string, []string, []byte) error
}

// FlagStore is an optional targeted mutation capability. Keeping it separate
// from Client makes unsupported provider operations unavailable by default.
type FlagStore interface {
	StoreFlags(context.Context, config.Provider, string, imap.UID, uint32, []imap.Flag, imap.StoreFlagsOp) error
}

type Remote struct{}

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

func connect(ctx context.Context, srv config.Server) (*imapclient.Client, error) {
	secret, err := Secret(ctx, srv.Password)
	if err != nil {
		return nil, err
	}
	client, err := imapclient.DialTLS(srv.Address, nil)
	if err != nil {
		return nil, err
	}
	if err := client.Login(srv.Username, secret).Wait(); err != nil {
		client.Close()
		return nil, errors.New("upstream IMAP authentication failed")
	}
	return client, nil
}

func (Remote) Folders(ctx context.Context, account config.Provider) ([]string, error) {
	client, err := connect(ctx, account.IMAP)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	cmd := client.List("", "*", nil)
	var folders []string
	for m := cmd.Next(); m != nil; m = cmd.Next() {
		folders = append(folders, m.Mailbox)
	}
	if err := cmd.Close(); err != nil {
		return nil, err
	}
	return folders, nil
}

func (Remote) Messages(ctx context.Context, account config.Provider, folder string) ([]Message, error) {
	client, err := connect(ctx, account.IMAP)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	selected, err := client.Select(folder, nil).Wait()
	if err != nil {
		return nil, err
	}
	if selected.NumMessages == 0 {
		return nil, nil
	}
	var seq imap.SeqSet
	seq.AddRange(1, selected.NumMessages)
	cmd := client.Fetch(seq, &imap.FetchOptions{UID: true, Flags: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}, RFC822Size: true})
	var messages []Message
	for item := cmd.Next(); item != nil; item = cmd.Next() {
		var uid imap.UID
		var flags []imap.Flag
		var raw []byte
		var size int64
		for part := item.Next(); part != nil; part = item.Next() {
			switch v := part.(type) {
			case imapclient.FetchItemDataUID:
				uid = v.UID
			case imapclient.FetchItemDataFlags:
				flags = v.Flags
			case imapclient.FetchItemDataRFC822Size:
				size = v.Size
			case imapclient.FetchItemDataBodySection:
				if v.Literal == nil {
					cmd.Close()
					return nil, errors.New("upstream message has no body")
				}
				read, e := io.ReadAll(io.LimitReader(v.Literal, mail.MaxMessage+1))
				if e != nil {
					cmd.Close()
					return nil, e
				}
				raw = read
			}
		}
		if size > mail.MaxMessage || len(raw) > mail.MaxMessage || uid == 0 {
			continue
		}
		messages = append(messages, Message{UID: uid, UIDValidity: selected.UIDValidity, Raw: raw, Flags: flags})
	}
	if err := cmd.Close(); err != nil {
		return nil, err
	}
	return messages, nil
}

func (Remote) Send(ctx context.Context, account config.Provider, from string, recipients []string, message []byte) error {
	password, err := Secret(ctx, account.SMTP.Password)
	if err != nil {
		return err
	}
	client, err := smtp.DialTLS(account.SMTP.Address, nil)
	if err != nil {
		return err
	}
	defer client.Close()
	client.CommandTimeout = 15 * time.Second
	client.SubmissionTimeout = 30 * time.Second
	if err := client.Auth(sasl.NewPlainClient("", account.SMTP.Username, password)); err != nil {
		return errors.New("upstream SMTP authentication failed")
	}
	if err := client.SendMail(from, recipients, bytes.NewReader(message)); err != nil {
		return fmt.Errorf("upstream SMTP submission failed (delivery status may be unknown): %w", err)
	}
	return nil
}

// StoreFlags changes flags for exactly one upstream UID. It selects and checks
// the mailbox generation immediately before issuing the UID STORE command, so
// a virtual ID from an earlier UIDVALIDITY generation cannot mutate new mail.
func (Remote) StoreFlags(ctx context.Context, account config.Provider, folder string, uid imap.UID, uidValidity uint32, flags []imap.Flag, op imap.StoreFlagsOp) error {
	if folder == "" || uid == 0 || uidValidity == 0 || len(flags) == 0 {
		return errors.New("invalid flag update")
	}
	if op != imap.StoreFlagsAdd && op != imap.StoreFlagsDel {
		return errors.New("unsupported flag update")
	}
	client, err := connect(ctx, account.IMAP)
	if err != nil {
		return err
	}
	defer client.Close()
	selected, err := client.Select(folder, nil).Wait()
	if err != nil {
		return err
	}
	if selected.UIDValidity != uidValidity {
		return errors.New("mailbox changed")
	}
	command := client.Store(imap.UIDSetNum(uid), &imap.StoreFlags{Op: op, Silent: true, Flags: flags}, nil)
	return command.Close()
}
