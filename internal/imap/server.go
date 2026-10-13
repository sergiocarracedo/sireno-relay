package imap

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/mail"
	"strings"
	"time"

	wire "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-message/textproto"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/relay"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/view"
)

var unavailable = &wire.Error{Type: wire.StatusResponseTypeNo, Text: "mailbox unavailable"}
var readonly = &wire.Error{Type: wire.StatusResponseTypeNo, Text: "operation not supported by safe mailbox"}

func Server(r *relay.Relay) *imapserver.Server {
	return imapserver.New(&imapserver.Options{
		Caps:         wire.CapSet{wire.CapIMAP4rev1: {}},
		InsecureAuth: true, // local loopback-only listener is validated by config
		NewSession: func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &session{relay: r, conn: conn}, nil, nil
		},
	})
}

type session struct {
	relay    *relay.Relay
	conn     *imapserver.Conn
	identity *relay.Identity
	selected *view.Snapshot
}

func (s *session) Close() error { return nil }
func (s *session) Login(name, password string) error {
	identity, err := s.relay.Authenticate(context.Background(), name, password)
	if err != nil {
		return imapserver.ErrAuthFailed
	}
	s.identity = identity
	return nil
}

func (s *session) snapshot(folder string) (*view.Snapshot, error) {
	if s.identity == nil {
		return nil, unavailable
	}
	result, err := s.relay.Snapshot(context.Background(), s.identity, folder)
	if err != nil {
		return nil, unavailable
	}
	return result, nil
}

func (s *session) selectedNow() (*view.Snapshot, error) {
	if s.selected == nil {
		return nil, unavailable
	}
	current, err := s.snapshot(s.selected.Folder)
	if err != nil || current.UIDValidity != s.selected.UIDValidity || len(current.Messages) != len(s.selected.Messages) {
		s.conn.Bye("mailbox view changed; reconnect")
		return nil, unavailable
	}
	for i := range current.Messages {
		if current.Messages[i].UID != s.selected.Messages[i].UID || !bytes.Equal(current.Messages[i].Mail.Bytes, s.selected.Messages[i].Mail.Bytes) {
			s.conn.Bye("mailbox view changed; reconnect")
			return nil, unavailable
		}
	}
	s.selected = current
	return current, nil
}

func (s *session) Select(folder string, _ *wire.SelectOptions) (*wire.SelectData, error) {
	snapshot, err := s.snapshot(folder)
	if err != nil {
		return nil, err
	}
	s.selected = snapshot
	return &wire.SelectData{NumMessages: uint32(len(snapshot.Messages)), UIDValidity: snapshot.UIDValidity, UIDNext: wire.UID(snapshot.UIDNext), Flags: []wire.Flag{wire.FlagSeen, wire.FlagFlagged, wire.FlagDeleted}}, nil
}

func (s *session) Status(folder string, _ *wire.StatusOptions) (*wire.StatusData, error) {
	snapshot, err := s.snapshot(folder)
	if err != nil {
		return nil, err
	}
	count := uint32(len(snapshot.Messages))
	var unseen uint32
	var size int64
	for _, item := range snapshot.Messages {
		size += int64(len(item.Mail.Bytes))
		if !hasFlag(item.Flags, wire.FlagSeen) {
			unseen++
		}
	}
	return &wire.StatusData{Mailbox: folder, NumMessages: &count, NumUnseen: &unseen, UIDNext: wire.UID(snapshot.UIDNext), UIDValidity: snapshot.UIDValidity, Size: &size}, nil
}

func hasFlag(flags []wire.Flag, flag wire.Flag) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}

func (s *session) List(w *imapserver.ListWriter, ref string, patterns []string, _ *wire.ListOptions) error {
	if s.identity == nil {
		return unavailable
	}
	folders, err := s.relay.Folders(context.Background(), s.identity)
	if err != nil {
		return unavailable
	}
	for _, folder := range folders {
		for _, pattern := range patterns {
			if imapserver.MatchList(folder, '/', ref, pattern) {
				if err := w.WriteList(&wire.ListData{Mailbox: folder, Delim: '/'}); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

func (s *session) Unselect() error { s.selected = nil; return nil }
func (s *session) Poll(_ *imapserver.UpdateWriter, _ bool) error {
	_, err := s.selectedNow()
	if s.selected == nil {
		return nil
	}
	return err
}
func (s *session) Idle(_ *imapserver.UpdateWriter, stop <-chan struct{}) error {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-tick.C:
			if s.selected != nil {
				if _, err := s.selectedNow(); err != nil {
					return err
				}
			}
		}
	}
}

func matchesSet(numSet wire.NumSet, seq uint32, uid wire.UID) bool {
	switch set := numSet.(type) {
	case wire.SeqSet:
		return set.Contains(seq)
	case wire.UIDSet:
		return set.Contains(uid)
	default:
		return false
	}
}

func (s *session) Fetch(w *imapserver.FetchWriter, nums wire.NumSet, opts *wire.FetchOptions) error {
	snapshot, err := s.selectedNow()
	if err != nil {
		return err
	}
	if opts == nil {
		return readonly
	}
	if opts.ModSeq || opts.ChangedSince != 0 || len(opts.BinarySection) > 0 || len(opts.BinarySectionSize) > 0 {
		return readonly
	}
	for i, item := range snapshot.Messages {
		seq := uint32(i + 1)
		if !matchesSet(nums, seq, wire.UID(item.UID)) {
			continue
		}
		response := w.CreateMessage(seq)
		if opts.UID {
			response.WriteUID(wire.UID(item.UID))
		}
		if opts.Flags {
			response.WriteFlags(item.Flags)
		}
		if opts.RFC822Size {
			response.WriteRFC822Size(int64(len(item.Mail.Bytes)))
		}
		if opts.Envelope {
			header, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(item.Mail.Bytes)))
			if err != nil {
				return unavailable
			}
			// ExtractEnvelope operates only on the allowlisted, sanitized headers.
			response.WriteEnvelope(imapserver.ExtractEnvelope(header))
		}
		if opts.BodyStructure != nil {
			response.WriteBodyStructure(imapserver.ExtractBodyStructure(bytes.NewReader(item.Mail.Bytes)))
		}
		for _, section := range opts.BodySection {
			data := imapserver.ExtractBodySection(bytes.NewReader(item.Mail.Bytes), section)
			if data == nil {
				return readonly
			}
			writer := response.WriteBodySection(section, int64(len(data)))
			if _, err := writer.Write(data); err != nil {
				return err
			}
			if err := writer.Close(); err != nil {
				return err
			}
		}
		if err := response.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) Search(kind imapserver.NumKind, criteria *wire.SearchCriteria, _ *wire.SearchOptions) (*wire.SearchData, error) {
	snapshot, err := s.selectedNow()
	if err != nil {
		return nil, err
	}
	if criteria == nil {
		return nil, readonly
	}
	var seq wire.SeqSet
	var uids wire.UIDSet
	for i, item := range snapshot.Messages {
		matched, err := searchMatch(criteria, item, uint32(i+1))
		if err != nil {
			return nil, readonly
		}
		if matched {
			seq.AddNum(uint32(i + 1))
			uids.AddNum(wire.UID(item.UID))
		}
	}
	if kind == imapserver.NumKindUID {
		return &wire.SearchData{All: uids}, nil
	}
	return &wire.SearchData{All: seq}, nil
}

func searchMatch(criteria *wire.SearchCriteria, item view.Message, seq uint32) (bool, error) {
	if criteria.ModSeq != nil {
		return false, errors.New("unsupported search")
	}
	for _, set := range criteria.SeqNum {
		if !set.Contains(seq) {
			return false, nil
		}
	}
	for _, set := range criteria.UID {
		if !set.Contains(wire.UID(item.UID)) {
			return false, nil
		}
	}
	for _, flag := range criteria.Flag {
		if !hasFlag(item.Flags, flag) {
			return false, nil
		}
	}
	for _, flag := range criteria.NotFlag {
		if hasFlag(item.Flags, flag) {
			return false, nil
		}
	}
	if criteria.Larger > 0 && int64(len(item.Mail.Bytes)) <= criteria.Larger {
		return false, nil
	}
	if criteria.Smaller > 0 && int64(len(item.Mail.Bytes)) >= criteria.Smaller {
		return false, nil
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(item.Mail.Bytes))
	if err != nil {
		return false, err
	}
	date, dateErr := mail.ParseDate(parsed.Header.Get("Date"))
	if !criteria.Since.IsZero() || !criteria.Before.IsZero() || !criteria.SentSince.IsZero() || !criteria.SentBefore.IsZero() {
		if dateErr != nil {
			return false, nil
		}
		if !criteria.Since.IsZero() && date.Before(criteria.Since) || !criteria.Before.IsZero() && !date.Before(criteria.Before) || !criteria.SentSince.IsZero() && date.Before(criteria.SentSince) || !criteria.SentBefore.IsZero() && !date.Before(criteria.SentBefore) {
			return false, nil
		}
	}
	for _, field := range criteria.Header {
		if !strings.Contains(strings.ToLower(parsed.Header.Get(field.Key)), strings.ToLower(field.Value)) {
			return false, nil
		}
	}
	body, _ := io.ReadAll(parsed.Body)
	for _, value := range criteria.Body {
		if !strings.Contains(strings.ToLower(string(body)), strings.ToLower(value)) {
			return false, nil
		}
	}
	for _, value := range criteria.Text {
		if !strings.Contains(strings.ToLower(string(item.Mail.Bytes)), strings.ToLower(value)) {
			return false, nil
		}
	}
	for _, clause := range criteria.Not {
		matched, err := searchMatch(&clause, item, seq)
		if err != nil {
			return false, err
		}
		if matched {
			return false, nil
		}
	}
	for _, pair := range criteria.Or {
		a, err := searchMatch(&pair[0], item, seq)
		if err != nil {
			return false, err
		}
		b, err := searchMatch(&pair[1], item, seq)
		if err != nil {
			return false, err
		}
		if !a && !b {
			return false, nil
		}
	}
	return true, nil
}

func (s *session) Create(string, *wire.CreateOptions) error         { return readonly }
func (s *session) Delete(string) error                              { return readonly }
func (s *session) Rename(string, string, *wire.RenameOptions) error { return readonly }
func (s *session) Subscribe(string) error                           { return readonly }
func (s *session) Unsubscribe(string) error                         { return readonly }
func (s *session) Append(string, wire.LiteralReader, *wire.AppendOptions) (*wire.AppendData, error) {
	return nil, readonly
}
func (s *session) Expunge(*imapserver.ExpungeWriter, *wire.UIDSet) error { return readonly }
func (s *session) Store(*imapserver.FetchWriter, wire.NumSet, *wire.StoreFlags, *wire.StoreOptions) error {
	return readonly
}
func (s *session) Copy(wire.NumSet, string) (*wire.CopyData, error) { return nil, readonly }
