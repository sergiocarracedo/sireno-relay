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

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/mail"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/policy"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
)

type Message struct {
	UID      uint32
	Upstream provider.Message // never expose upstream IDs to an agent
	Mail     *mail.Message
	Flags    []provider.Flag
}

type Snapshot struct {
	Folder             string
	UIDValidity        uint32
	UpstreamGeneration string
	UIDNext            uint32
	Messages           []Message
}

type idState struct {
	UIDValidity        uint32            `json:"uid_validity"`
	UpstreamGeneration string            `json:"upstream_generation"`
	Next               uint32            `json:"next"`
	Visible            map[string]uint32 `json:"visible"`
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

// Page builds one filtered virtual page from a bounded provider candidate page.
// The boolean reports only whether another candidate page may exist; it never
// represents a total message count.
func (s *Service) Page(ctx context.Context, user, providerName, folder string, account config.Provider, profile config.Profile, p *policy.Policy, page uint32, query string) (*Snapshot, bool, error) {
	if page == 0 || !p.ReadFolder(folder) {
		return nil, false, errors.New("mailbox unavailable")
	}
	capacity := provider.CapacityList
	if query != "" {
		capacity = provider.CapacitySearch
	}
	if err := provider.Supports(ctx, s.Provider, account, capacity); err != nil {
		return nil, false, err
	}
	upstream, err := s.Provider.Page(ctx, account, provider.PageRequest{Folder: folder, Page: page, PageSize: 20, Query: query})
	if err != nil {
		return nil, false, fmt.Errorf("mailbox unavailable: %w", err)
	}
	wrapped := pagedClient{Client: s.Provider, messages: upstream.Messages}
	result, err := (&Service{Provider: wrapped, Store: s.Store}).Snapshot(ctx, user, providerName, folder, account, profile, p)
	return result, upstream.HasMore, err
}

type pagedClient struct {
	provider.Client
	messages []provider.Message
}

func (p pagedClient) Messages(context.Context, config.Provider, string) ([]provider.Message, error) {
	return p.messages, nil
}

func (s *Service) Folders(ctx context.Context, account config.Provider, p *policy.Policy) ([]string, error) {
	if err := provider.Supports(ctx, s.Provider, account, provider.CapacityFolders); err != nil {
		return nil, err
	}
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
	if err := provider.Supports(ctx, s.Provider, account, provider.CapacityList); err != nil {
		return nil, err
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
		if item.ID == "" || item.Generation == "" {
			continue
		}
		key := fmt.Sprintf("%s:%s:%x", item.Generation, item.ID, digest)
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
	upstreamGeneration := ""
	for _, item := range items {
		if item.Generation == "" {
			continue
		}
		if upstreamGeneration != "" && upstreamGeneration != item.Generation {
			return nil, errors.New("mailbox unavailable")
		}
		upstreamGeneration = item.Generation
	}
	created := state == nil
	if state == nil || (upstreamGeneration != "" && state.UpstreamGeneration != "" && state.UpstreamGeneration != upstreamGeneration) {
		// MCP message reads accept only a virtual UID (not UIDVALIDITY). Keep
		// virtual UIDs monotonic across an upstream generation change so a stale
		// UID cannot resolve to a replacement message in the new mailbox.
		nextUID := uint32(1)
		if state != nil && state.Next > nextUID {
			nextUID = state.Next
		}
		state, err = newIDState(upstreamGeneration, nextUID)
		if err != nil {
			return nil, err
		}
	} else if upstreamGeneration != "" && state.UpstreamGeneration == "" {
		// State created before a mailbox had messages gains its upstream
		// generation as soon as one becomes available.
		state = &idState{UIDValidity: state.UIDValidity, UpstreamGeneration: upstreamGeneration, Next: state.Next, Visible: state.Visible}
	}
	next := &idState{UIDValidity: state.UIDValidity, UpstreamGeneration: state.UpstreamGeneration, Next: state.Next, Visible: make(map[string]uint32, len(state.Visible)+len(candidates))}
	for key, uid := range state.Visible {
		next.Visible[key] = uid
	}
	result := &Snapshot{Folder: folder, UIDValidity: next.UIDValidity, UpstreamGeneration: next.UpstreamGeneration}
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
		result.Messages = append(result.Messages, Message{UID: uid, Upstream: candidate.item, Mail: candidate.parsed, Flags: candidate.item.Flags})
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

func newIDState(upstreamGeneration string, next uint32) (*idState, error) {
	var value [4]byte
	if _, err := rand.Read(value[:]); err != nil {
		return nil, err
	}
	validity := binary.BigEndian.Uint32(value[:])
	if validity == 0 {
		validity = 1
	}
	if next == 0 {
		next = 1
	}
	return &idState{UIDValidity: validity, UpstreamGeneration: upstreamGeneration, Next: next, Visible: make(map[string]uint32)}, nil
}

func sameState(a, b *idState) bool {
	if a == nil || b == nil || a.UIDValidity != b.UIDValidity || a.UpstreamGeneration != b.UpstreamGeneration || a.Next != b.Next || len(a.Visible) != len(b.Visible) {
		return false
	}
	for key, uid := range a.Visible {
		if b.Visible[key] != uid {
			return false
		}
	}
	return true
}
