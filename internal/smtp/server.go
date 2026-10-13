package smtp

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	wire "github.com/emersion/go-smtp"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/mail"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/relay"
)

type backend struct{ relay *relay.Relay }

func Server(r *relay.Relay, address string) *wire.Server {
	s := wire.NewServer(&backend{relay: r})
	s.Addr = address
	s.Domain = "localhost"
	s.AllowInsecureAuth = true // loopback-only listeners are validated at config load
	s.MaxMessageBytes = mail.MaxMessage
	s.MaxRecipients = 50
	s.ReadTimeout = 30 * time.Second
	s.WriteTimeout = 30 * time.Second
	return s
}

func (b *backend) NewSession(_ *wire.Conn) (wire.Session, error) {
	return &session{relay: b.relay}, nil
}

type session struct {
	relay      *relay.Relay
	user       *relay.Identity
	from       string
	recipients []string
}

func (s *session) AuthMechanisms() []string { return []string{sasl.Plain} }

func (s *session) Auth(mech string) (sasl.Server, error) {
	if mech != sasl.Plain {
		return nil, errors.New("unsupported authentication")
	}
	return sasl.NewPlainServer(func(identity, username, password string) error {
		if identity != "" && identity != username {
			return errors.New("authentication failed")
		}
		u, err := s.relay.Authenticate(context.Background(), username, password)
		if err != nil {
			return errors.New("authentication failed")
		}
		s.user = u
		return nil
	}), nil
}

func (s *session) Mail(from string, _ *wire.MailOptions) error {
	if s.user == nil {
		return wire.ErrAuthRequired
	}
	current, err := s.relay.Current(s.user)
	if err != nil || !strings.EqualFold(from, current.Account.Address) {
		return mail.ErrDenied
	}
	s.from = from
	s.recipients = nil
	return nil
}

func (s *session) Rcpt(to string, _ *wire.RcptOptions) error {
	if s.user == nil {
		return wire.ErrAuthRequired
	}
	current, err := s.relay.Current(s.user)
	if err != nil || !current.Policy.Send([]string{to}) {
		return mail.ErrDenied
	}
	s.recipients = append(s.recipients, to)
	return nil
}

func (s *session) Data(input io.Reader) error {
	if s.user == nil {
		return wire.ErrAuthRequired
	}
	raw, err := io.ReadAll(io.LimitReader(input, mail.MaxMessage+1))
	if err != nil || len(raw) > mail.MaxMessage {
		return mail.ErrDenied
	}
	return s.relay.Send(context.Background(), s.user, s.from, s.recipients, raw)
}

func (s *session) Reset()        { s.from = ""; s.recipients = nil }
func (s *session) Logout() error { s.Reset(); return nil }
