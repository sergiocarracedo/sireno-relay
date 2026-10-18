// Package fixture provides a stateful provider for relay safety tests.
package fixture

import (
	"context"
	"sync"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
)

type Provider struct {
	mu                sync.Mutex
	CapabilitiesValue provider.Capabilities
	FoldersValue      []string
	Pages             map[string][][]provider.Message
	Calls             []provider.PageRequest
	Sent              [][]byte
}

func New() *Provider {
	return &Provider{CapabilitiesValue: provider.Capabilities{provider.CapacityFolders: true, provider.CapacityList: true, provider.CapacitySearch: true, provider.CapacityRead: true, provider.CapacitySend: true}, Pages: map[string][][]provider.Message{}}
}

func (p *Provider) Capabilities(context.Context, config.Provider) (provider.Capabilities, error) {
	return p.CapabilitiesValue, nil
}
func (p *Provider) Folders(context.Context, config.Provider) ([]string, error) {
	return append([]string(nil), p.FoldersValue...), nil
}
func (p *Provider) Messages(ctx context.Context, account config.Provider, folder string) ([]provider.Message, error) {
	page, err := p.Page(ctx, account, provider.PageRequest{Folder: folder, Page: 1, PageSize: 20})
	return page.Messages, err
}
func (p *Provider) Page(_ context.Context, _ config.Provider, request provider.PageRequest) (provider.Page, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Calls = append(p.Calls, request)
	pages := p.Pages[request.Folder]
	if request.Page == 0 || int(request.Page) > len(pages) {
		return provider.Page{}, nil
	}
	return provider.Page{Messages: append([]provider.Message(nil), pages[request.Page-1]...), HasMore: int(request.Page) < len(pages)}, nil
}
func (p *Provider) Send(_ context.Context, _ config.Provider, _ string, _ []string, raw []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Sent = append(p.Sent, append([]byte(nil), raw...))
	return nil
}
