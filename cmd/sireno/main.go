package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	wiresmtp "github.com/emersion/go-smtp"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	proxyimap "github.com/sergiocarracedo/sireno-ai-relay/internal/imap"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/policy"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/relay"
	proxysmtp "github.com/sergiocarracedo/sireno-ai-relay/internal/smtp"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/view"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: sireno serve|config-check|policy-check|mail <folders|list|search|read|send>")
	}
	switch args[0] {
	case "serve", "config-check", "policy-check":
		return owner(args, out)
	case "mail":
		return agent(args[1:], out)
	default:
		return errors.New("unknown command")
	}
}

func owner(args []string, out io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	path := flags.String("config", "config.yml", "owner-only YAML config")
	user := flags.String("user", "", "agent name for policy inspection")
	folder := flags.String("folder", "", "folder for policy inspection")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	cfg, warnings, err := config.Load(*path)
	if err != nil {
		return err
	}
	for name, profile := range cfg.Profiles {
		if _, err := policy.Compile(profile); err != nil {
			return fmt.Errorf("profile %q: %w", name, err)
		}
	}
	for _, warning := range warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
	if args[0] == "config-check" {
		fmt.Fprintln(out, "configuration valid")
		return nil
	}
	store, err := view.Open(filepath.Join(filepath.Dir(*path), "sireno-state.json"))
	if err != nil {
		return err
	}
	r := &relay.Relay{ConfigPath: *path, Provider: provider.Remote{}, Store: store}
	if args[0] == "policy-check" {
		if *user == "" || *folder == "" {
			return errors.New("policy-check needs --user and --folder")
		}
		identity, err := r.Identity(*user)
		if err != nil {
			return err
		}
		if !identity.Policy.ReadFolder(*folder) {
			fmt.Fprintln(out, "folder: denied")
			return nil
		}
		snapshot, err := r.Snapshot(context.Background(), identity, *folder)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "folder: allowed; visible messages: %d\n", len(snapshot.Messages))
		return nil
	}
	if cfg.Listen.IMAP == "" {
		cfg.Listen.IMAP = "127.0.0.1:1143"
	}
	if cfg.Listen.SMTP == "" {
		cfg.Listen.SMTP = "127.0.0.1:1025"
	}
	imapLn, err := net.Listen("tcp", cfg.Listen.IMAP)
	if err != nil {
		return err
	}
	defer imapLn.Close()
	smtpLn, err := net.Listen("tcp", cfg.Listen.SMTP)
	if err != nil {
		return err
	}
	defer smtpLn.Close()
	imapSrv := proxyimap.Server(r)
	smtpSrv := proxysmtp.Server(r, cfg.Listen.SMTP)
	defer imapSrv.Close()
	defer smtpSrv.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	failures := make(chan error, 2)
	go func() { failures <- imapSrv.Serve(imapLn) }()
	go func() { failures <- smtpSrv.Serve(smtpLn) }()
	fmt.Fprintf(out, "relay listening on %s (IMAP) and %s (SMTP)\n", cfg.Listen.IMAP, cfg.Listen.SMTP)
	select {
	case <-ctx.Done():
		return nil
	case err := <-failures:
		return err
	}
}

func agent(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: sireno mail folders|list|search|read|send")
	}
	flags := flag.NewFlagSet("mail "+args[0], flag.ContinueOnError)
	username := flags.String("user", "", "agent login")
	imapAddress := flags.String("imap", "127.0.0.1:1143", "relay IMAP address")
	smtpAddress := flags.String("smtp", "127.0.0.1:1025", "relay SMTP address")
	folder := flags.String("folder", "INBOX", "mailbox")
	number := flags.Uint("uid", 0, "virtual message UID")
	from := flags.String("from", "", "sender address")
	to := flags.String("to", "", "comma-separated recipients")
	query := flags.String("query", "", "search visible message text")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	password := os.Getenv("SIRENO_AGENT_PASSWORD")
	if *username == "" || password == "" {
		return errors.New("--user and SIRENO_AGENT_PASSWORD are required")
	}
	if args[0] == "send" {
		if *from == "" || *to == "" {
			return errors.New("send requires --from and --to")
		}
		message, err := io.ReadAll(io.LimitReader(os.Stdin, 4*1024*1024+1))
		if err != nil {
			return err
		}
		client, err := wiresmtp.Dial(*smtpAddress)
		if err != nil {
			return err
		}
		defer client.Close()
		if err := client.Auth(sasl.NewPlainClient("", *username, password)); err != nil {
			return err
		}
		recipients := strings.Split(*to, ",")
		for i := range recipients {
			recipients[i] = strings.TrimSpace(recipients[i])
		}
		return client.SendMail(*from, recipients, bytes.NewReader(message))
	}
	client, err := imapclient.DialInsecure(*imapAddress, nil)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Login(*username, password).Wait(); err != nil {
		return err
	}
	if args[0] == "folders" {
		boxes, err := client.List("", "*", nil).Collect()
		if err != nil {
			return err
		}
		for _, box := range boxes {
			fmt.Fprintln(out, box.Mailbox)
		}
		return nil
	}
	if _, err := client.Select(*folder, nil).Wait(); err != nil {
		return err
	}
	if args[0] == "list" || args[0] == "search" {
		criteria := &imap.SearchCriteria{}
		if args[0] == "search" {
			if *query == "" {
				return errors.New("search requires --query")
			}
			criteria.Text = []string{*query}
		}
		found, err := client.UIDSearch(criteria, nil).Wait()
		if err != nil {
			return err
		}
		for _, uid := range found.AllUIDs() {
			var one imap.UIDSet
			one.AddNum(uid)
			items, err := client.Fetch(one, &imap.FetchOptions{UID: true, Envelope: true}).Collect()
			if err != nil {
				return err
			}
			if len(items) != 1 || items[0].Envelope == nil {
				return errors.New("mailbox view changed; retry")
			}
			fmt.Fprintf(out, "%d\t%s\n", uid, items[0].Envelope.Subject)
		}
		return nil
	}
	if args[0] != "read" || *number == 0 {
		return errors.New("read requires --uid; supported mail commands: folders, list, search, read, send")
	}
	var ids imap.UIDSet
	ids.AddNum(imap.UID(*number))
	message, err := client.Fetch(ids, &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}}).Collect()
	if err != nil {
		return err
	}
	if len(message) != 1 || len(message[0].BodySection) != 1 {
		return errors.New("message unavailable")
	}
	_, err = out.Write(message[0].BodySection[0].Bytes)
	return err
}
