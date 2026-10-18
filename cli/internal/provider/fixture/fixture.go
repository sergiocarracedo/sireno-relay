// Package fixture provides a stateful provider for relay safety tests.
package fixture

import (
	"context"
	"sync"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
)

type Provider struct {
	mu                    sync.Mutex
	CapabilitiesValue     provider.Capabilities
	CapabilitiesByAddress map[string]provider.Capabilities
	FoldersValue          []string
	FoldersByAddress      map[string][]string
	Pages                 map[string][][]provider.Message
	PagesByAddress        map[string]map[string][][]provider.Message
	Calls                 []provider.PageRequest
	PageCalls             []PageCall
	Sent                  []SentMessage
}

// PageCall and SentMessage make authorization assertions observable without
// exposing any production provider implementation details to tests.
type PageCall struct {
	Address string
	Request provider.PageRequest
}

type SentMessage struct {
	Address    string
	From       string
	Recipients []string
	Raw        []byte
}

func New() *Provider {
	return &Provider{
		CapabilitiesValue:     provider.Capabilities{provider.CapacityFolders: true, provider.CapacityList: true, provider.CapacitySearch: true, provider.CapacityRead: true, provider.CapacitySend: true},
		CapabilitiesByAddress: make(map[string]provider.Capabilities),
		FoldersByAddress:      make(map[string][]string),
		Pages:                 make(map[string][][]provider.Message),
		PagesByAddress:        make(map[string]map[string][][]provider.Message),
	}
}

func (p *Provider) Capabilities(_ context.Context, account config.Provider) (provider.Capabilities, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if capabilities, ok := p.CapabilitiesByAddress[account.Address]; ok {
		return cloneCapabilities(capabilities), nil
	}
	return cloneCapabilities(p.CapabilitiesValue), nil
}

func (p *Provider) Folders(_ context.Context, account config.Provider) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if folders, ok := p.FoldersByAddress[account.Address]; ok {
		return append([]string(nil), folders...), nil
	}
	return append([]string(nil), p.FoldersValue...), nil
}
func (p *Provider) Messages(ctx context.Context, account config.Provider, folder string) ([]provider.Message, error) {
	page, err := p.Page(ctx, account, provider.PageRequest{Folder: folder, Page: 1, PageSize: 20})
	return page.Messages, err
}
func (p *Provider) Page(_ context.Context, account config.Provider, request provider.PageRequest) (provider.Page, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Calls = append(p.Calls, request)
	p.PageCalls = append(p.PageCalls, PageCall{Address: account.Address, Request: request})
	pages := p.Pages[request.Folder]
	if byFolder, ok := p.PagesByAddress[account.Address]; ok {
		pages = byFolder[request.Folder]
	}
	if request.Page == 0 || int(request.Page) > len(pages) {
		return provider.Page{}, nil
	}
	return provider.Page{Messages: cloneMessages(pages[request.Page-1]), HasMore: int(request.Page) < len(pages)}, nil
}
func (p *Provider) Send(_ context.Context, account config.Provider, from string, recipients []string, raw []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Sent = append(p.Sent, SentMessage{Address: account.Address, From: from, Recipients: append([]string(nil), recipients...), Raw: append([]byte(nil), raw...)})
	return nil
}

func (p *Provider) ResetCalls() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Calls = nil
	p.PageCalls = nil
}

func (p *Provider) PageCallSnapshot() []PageCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]PageCall(nil), p.PageCalls...)
}

func (p *Provider) SentSnapshot() []SentMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]SentMessage(nil), p.Sent...)
}

func cloneCapabilities(input provider.Capabilities) provider.Capabilities {
	result := make(provider.Capabilities, len(input))
	for capacity, available := range input {
		result[capacity] = available
	}
	return result
}

func cloneMessages(input []provider.Message) []provider.Message {
	result := make([]provider.Message, len(input))
	for i, message := range input {
		result[i] = message
		result[i].Raw = append([]byte(nil), message.Raw...)
		result[i].Flags = append([]provider.Flag(nil), message.Flags...)
	}
	return result
}
