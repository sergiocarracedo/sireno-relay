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
	UID   imap.UID
	Raw   []byte
	Flags []imap.Flag
}

type Client interface {
	Folders(context.Context, config.Provider) ([]string, error)
	Messages(context.Context, config.Provider, string) ([]Message, error)
	Send(context.Context, config.Provider, string, []string, []byte) error
}

type Remote struct{}

func Secret(ctx context.Context, command []string) (string, error) {
	if len(command) == 0 || command[0] == "" {
		return "", errors.New("missing secret command")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
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
	secret, err := Secret(ctx, srv.PasswordCommand)
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
		messages = append(messages, Message{UID: uid, Raw: raw, Flags: flags})
	}
	if err := cmd.Close(); err != nil {
		return nil, err
	}
	return messages, nil
}

func (Remote) Send(ctx context.Context, account config.Provider, from string, recipients []string, message []byte) error {
	password, err := Secret(ctx, account.SMTP.PasswordCommand)
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
