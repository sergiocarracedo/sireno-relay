package view

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/emersion/go-imap/v2"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/mail"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/policy"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
)

type Message struct {
	UID         uint32
	UpstreamUID imap.UID // never expose to an agent
	Mail        *mail.Message
	Flags       []imap.Flag
}

type Snapshot struct {
	Folder              string
	UIDValidity         uint32
	UpstreamUIDValidity uint32
	UIDNext             uint32
	Messages            []Message
}

type idState struct {
	UIDValidity         uint32            `json:"uid_validity"`
	UpstreamUIDValidity uint32            `json:"upstream_uid_validity"`
	Next                uint32            `json:"next"`
	Visible             map[string]uint32 `json:"visible"`
}

type Store struct {
	mu     sync.Mutex
	path   string
	states map[string]*idState
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, states: make(map[string]*idState)}
	if path == "" {
		return s, nil
	}
	stat, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
		return nil, errors.New("view state must be owner-only")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.states); err != nil {
		return nil, fmt.Errorf("corrupt view state: %w", err)
	}
	for _, state := range s.states {
		if state == nil || state.UIDValidity == 0 || state.Next == 0 || state.Visible == nil {
			return nil, errors.New("corrupt view state")
		}
	}
	return s, nil
}

func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	data, err := json.Marshal(s.states)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".relay-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.path)
}

type Service struct {
	Provider provider.Client
	Store    *Store
}

func (s *Service) Folders(ctx context.Context, account config.Provider, p *policy.Policy) ([]string, error) {
	folders, err := s.Provider.Folders(ctx, account)
	if err != nil {
		return nil, err
	}
	var visible []string
	for _, folder := range folders {
		if p.ReadFolder(folder) {
			visible = append(visible, folder)
		}
	}
	return visible, nil
}

func (s *Service) Snapshot(ctx context.Context, user, providerName, folder string, account config.Provider, profile config.Profile, p *policy.Policy) (*Snapshot, error) {
	if !p.ReadFolder(folder) {
		return nil, errors.New("mailbox unavailable")
	}
	items, err := s.Provider.Messages(ctx, account, folder)
	if err != nil {
		return nil, errors.New("mailbox unavailable")
	}
	type candidate struct {
		key    string
		item   provider.Message
		parsed *mail.Message
	}
	var candidates []candidate
	for _, item := range items {
		parsed, err := mail.Evaluate(item.Raw, profile.Inbound)
		if err != nil {
			continue
		} // unavailable messages must never be observable
		addresses := append(append(append([]string{}, parsed.From...), parsed.To...), parsed.Cc...)
		addresses = append(addresses, parsed.Bcc...)
		if !p.ReadMessage(folder, addresses) {
			continue
		}
		digest := sha256.Sum256(item.Raw)
		key := fmt.Sprintf("%d:%x", item.UID, digest)
		candidates = append(candidates, candidate{key, item, parsed})
	}
	s.Store.mu.Lock()
	defer s.Store.mu.Unlock()
	key, err := json.Marshal([]string{user, providerName, folder})
	if err != nil {
		return nil, err
	}
	name := string(key)
	state := s.Store.states[name]
	upstreamUIDValidity := uint32(0)
	for _, item := range items {
		if item.UIDValidity == 0 {
			continue
		}
		if upstreamUIDValidity != 0 && upstreamUIDValidity != item.UIDValidity {
			return nil, errors.New("mailbox unavailable")
		}
		upstreamUIDValidity = item.UIDValidity
	}
	created := state == nil
	if state == nil || (upstreamUIDValidity != 0 && state.UpstreamUIDValidity != 0 && state.UpstreamUIDValidity != upstreamUIDValidity) {
		state, err = newIDState(upstreamUIDValidity)
		if err != nil {
			return nil, err
		}
	} else if upstreamUIDValidity != 0 && state.UpstreamUIDValidity == 0 {
		// State created before a mailbox had messages gains its upstream
		// generation as soon as one becomes available.
		state = &idState{UIDValidity: state.UIDValidity, UpstreamUIDValidity: upstreamUIDValidity, Next: state.Next, Visible: state.Visible}
	}
	next := &idState{UIDValidity: state.UIDValidity, UpstreamUIDValidity: state.UpstreamUIDValidity, Next: state.Next, Visible: make(map[string]uint32)}
	result := &Snapshot{Folder: folder, UIDValidity: next.UIDValidity, UpstreamUIDValidity: next.UpstreamUIDValidity}
	for _, candidate := range candidates {
		uid, ok := state.Visible[candidate.key]
		if !ok {
			if next.Next == ^uint32(0) {
				return nil, errors.New("virtual UID space exhausted")
			}
			uid = next.Next
			next.Next++
		}
		next.Visible[candidate.key] = uid
		result.Messages = append(result.Messages, Message{UID: uid, UpstreamUID: candidate.item.UID, Mail: candidate.parsed, Flags: candidate.item.Flags})
	}
	result.UIDNext = next.Next
	sort.Slice(result.Messages, func(i, j int) bool { return result.Messages[i].UID < result.Messages[j].UID })
	s.Store.states[name] = next
	if !sameState(state, next) || created {
		if err := s.Store.save(); err != nil {
			s.Store.states[name] = state
			return nil, err
		}
	}
	return result, nil
}

func newIDState(upstreamUIDValidity uint32) (*idState, error) {
	var value [4]byte
	if _, err := rand.Read(value[:]); err != nil {
		return nil, err
	}
	validity := binary.BigEndian.Uint32(value[:])
	if validity == 0 {
		validity = 1
	}
	return &idState{UIDValidity: validity, UpstreamUIDValidity: upstreamUIDValidity, Next: 1, Visible: make(map[string]uint32)}, nil
}

func sameState(a, b *idState) bool {
	if a == nil || b == nil || a.UIDValidity != b.UIDValidity || a.UpstreamUIDValidity != b.UpstreamUIDValidity || a.Next != b.Next || len(a.Visible) != len(b.Visible) {
		return false
	}
	for key, uid := range a.Visible {
		if b.Visible[key] != uid {
			return false
		}
	}
	return true
}
