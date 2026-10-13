package relay

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/mail"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/policy"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/view"
)

type Relay struct {
	ConfigPath string
	Provider   provider.Client
	Store      *view.Store
}

type Identity struct {
	Name              string
	AccountName       string
	Account           config.Provider
	Profile           config.Profile
	Policy            *policy.Policy
	TagSent           bool
	CredentialCommand []string
}

func (r *Relay) Identity(name string) (*Identity, error) {
	cfg, _, err := config.Load(r.ConfigPath)
	if err != nil {
		return nil, err
	}
	u, ok := cfg.Users[name]
	if !ok {
		return nil, errors.New("unknown user")
	}
	profile := cfg.Profiles[u.Profile]
	p, err := policy.Compile(profile)
	if err != nil {
		return nil, err
	}
	tag := u.TagSent == nil || *u.TagSent
	return &Identity{Name: name, AccountName: u.Provider, Account: cfg.Providers[u.Provider], Profile: profile, Policy: p, TagSent: tag, CredentialCommand: u.PasswordCommand}, nil
}

func (r *Relay) Authenticate(ctx context.Context, name, password string) (*Identity, error) {
	cfg, _, err := config.Load(r.ConfigPath)
	if err != nil {
		return nil, errors.New("authentication failed")
	}
	u, ok := cfg.Users[name]
	if !ok {
		return nil, errors.New("authentication failed")
	}
	secret, err := provider.Secret(ctx, u.PasswordCommand)
	if err != nil || subtle.ConstantTimeCompare([]byte(secret), []byte(password)) != 1 {
		return nil, errors.New("authentication failed")
	}
	return r.Identity(name)
}

func (r *Relay) Current(identity *Identity) (*Identity, error) {
	current, err := r.Identity(identity.Name)
	if err != nil {
		return nil, errors.New("policy unavailable")
	}
	if !samePolicy(current, identity) {
		return nil, errors.New("policy changed; reconnect")
	}
	return current, nil
}

func (r *Relay) Folders(ctx context.Context, identity *Identity) ([]string, error) {
	current, err := r.Current(identity)
	if err != nil {
		return nil, err
	}
	return (&view.Service{Provider: r.Provider, Store: r.Store}).Folders(ctx, current.Account, current.Policy)
}

func (r *Relay) Snapshot(ctx context.Context, identity *Identity, folder string) (*view.Snapshot, error) {
	current, err := r.Current(identity)
	if err != nil {
		return nil, err
	}
	return (&view.Service{Provider: r.Provider, Store: r.Store}).Snapshot(ctx, current.Name, current.AccountName, folder, current.Account, current.Profile, current.Policy)
}

func (r *Relay) Send(ctx context.Context, identity *Identity, from string, recipients []string, raw []byte) error {
	current, err := r.Current(identity)
	if err != nil {
		return err
	}
	if !strings.EqualFold(from, current.Account.Address) || len(recipients) == 0 {
		return mail.ErrDenied
	}
	message, err := mail.Evaluate(raw, current.Profile.Outbound)
	if err != nil {
		return mail.ErrDenied
	}
	if len(message.From) != 1 || !strings.EqualFold(message.From[0], from) {
		return mail.ErrDenied
	}
	all := append([]string{}, recipients...)
	all = append(all, message.To...)
	all = append(all, message.Cc...)
	all = append(all, message.Bcc...)
	if !current.Policy.Send(all) {
		return mail.ErrDenied
	}
	// Evaluate creates an allowlisted message and drops untrusted identity headers.
	data := message.Bytes
	if current.TagSent {
		data = append([]byte(fmt.Sprintf("X-Sireno-Agent: %s\r\n", current.Name)), data...)
	}
	// A newly tightened policy must be checked again immediately before submission.
	latest, err := r.Current(identity)
	if err != nil || !samePolicy(current, latest) {
		return errors.New("policy changed; retry submission")
	}
	return r.Provider.Send(ctx, latest.Account, from, recipients, data)
}

func samePolicy(a, b *Identity) bool {
	if a == nil || b == nil {
		return false
	}
	return a.AccountName == b.AccountName && reflect.DeepEqual(a.Profile, b.Profile) && reflect.DeepEqual(a.Account, b.Account) && a.TagSent == b.TagSent && reflect.DeepEqual(a.CredentialCommand, b.CredentialCommand)
}
