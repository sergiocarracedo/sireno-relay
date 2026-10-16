package relay

import (
	"context"
	"crypto/sha256"
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
	Name        string
	AccountName string
	Account     config.Provider
	Profile     config.Profile
	Policy      *policy.Policy
	TagSent     bool
	Credential  config.Password
}

func (r *Relay) Identity(name string) (*Identity, error) {
	cfg, _, err := config.Load(r.ConfigPath)
	if err != nil {
		return nil, err
	}
	return identityFromConfig(cfg, name)
}

func identityFromConfig(cfg *config.Config, name string) (*Identity, error) {
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
	return &Identity{Name: name, AccountName: u.Provider, Account: cfg.Providers[u.Provider], Profile: profile, Policy: p, TagSent: tag, Credential: u.Password}, nil
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
	secret, err := provider.Secret(ctx, u.Password)
	if err != nil || subtle.ConstantTimeCompare([]byte(secret), []byte(password)) != 1 {
		return nil, errors.New("authentication failed")
	}
	return identityFromConfig(cfg, name)
}

// AuthenticateToken resolves a user only from an opaque bearer token. Tokens
// are never retained in Identity or returned through agent-facing operations.
func (r *Relay) AuthenticateToken(token string) (*Identity, error) {
	cfg, _, err := config.Load(r.ConfigPath)
	if err != nil || token == "" {
		return nil, errors.New("authentication failed")
	}
	digest := sha256.Sum256([]byte(token))
	want := fmt.Sprintf("%x", digest)
	var matched string
	for name, user := range cfg.Users {
		if user.TokenHash != "" && subtle.ConstantTimeCompare([]byte(user.TokenHash), []byte(want)) == 1 {
			matched = name
		}
	}
	if matched == "" {
		return nil, errors.New("authentication failed")
	}
	return identityFromConfig(cfg, matched)
}

func (r *Relay) Current(identity *Identity) (*Identity, error) {
	if identity == nil || identity.Name == "" {
		return nil, errors.New("policy unavailable")
	}
	current, err := r.Identity(identity.Name)
	if err != nil {
		return nil, errors.New("policy unavailable")
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

// Attachment re-builds the filtered view before returning bytes, so an
// attachment is never valid after its containing message becomes hidden.
func (r *Relay) Attachment(ctx context.Context, identity *Identity, folder string, messageUID, attachmentID uint32) (*mail.Attachment, error) {
	snapshot, err := r.Snapshot(ctx, identity, folder)
	if err != nil {
		return nil, errors.New("attachment unavailable")
	}
	for _, message := range snapshot.Messages {
		if message.UID != messageUID {
			continue
		}
		for _, attachment := range message.Mail.Attachments {
			if attachment.ID == attachmentID {
				return &attachment, nil
			}
		}
	}
	return nil, errors.New("attachment unavailable")
}

// Message resolves a virtual UID through a fresh filtered snapshot. It never
// accepts an upstream UID or returns mail that is no longer policy-visible.
func (r *Relay) Message(ctx context.Context, identity *Identity, folder string, messageUID uint32) (*view.Message, error) {
	messages, err := r.Messages(ctx, identity, folder, []uint32{messageUID})
	if err != nil {
		return nil, err
	}
	return &messages[0], nil
}

// Messages resolves a batch from one fresh filtered snapshot. This keeps a
// batch internally consistent and avoids repeated upstream mailbox scans.
func (r *Relay) Messages(ctx context.Context, identity *Identity, folder string, messageUIDs []uint32) ([]view.Message, error) {
	snapshot, err := r.Snapshot(ctx, identity, folder)
	if err != nil {
		return nil, errors.New("message unavailable")
	}
	byUID := make(map[uint32]view.Message, len(snapshot.Messages))
	for _, message := range snapshot.Messages {
		byUID[message.UID] = message
	}
	result := make([]view.Message, 0, len(messageUIDs))
	for _, messageUID := range messageUIDs {
		message, ok := byUID[messageUID]
		if !ok {
			return nil, errors.New("message unavailable")
		}
		result = append(result, message)
	}
	return result, nil
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
	return a.AccountName == b.AccountName && reflect.DeepEqual(a.Profile, b.Profile) && reflect.DeepEqual(a.Account, b.Account) && a.TagSent == b.TagSent && reflect.DeepEqual(a.Credential, b.Credential)
}
