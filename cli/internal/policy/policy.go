package policy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
)

type rule struct {
	mode  string
	match func(string) bool
}

type set struct{ allow, deny []rule }

// Policy is immutable once compiled. Its methods are shared by every agent entry point.
type Policy struct{ folders, addresses set }

func Compile(profile config.Profile) (*Policy, error) {
	folders, err := compile(profile.Folders, false)
	if err != nil {
		return nil, fmt.Errorf("folders: %w", err)
	}
	addresses, err := compile(profile.Addresses, true)
	if err != nil {
		return nil, fmt.Errorf("addresses: %w", err)
	}
	return &Policy{folders: folders, addresses: addresses}, nil
}

func compile(rules config.Rules, address bool) (set, error) {
	var result set
	for _, group := range []struct {
		input  []string
		output *[]rule
	}{{rules.Allow, &result.allow}, {rules.Deny, &result.deny}} {
		for _, text := range group.input {
			parts := strings.SplitN(text, ":", 2)
			if len(parts) != 2 || parts[1] == "" {
				return set{}, fmt.Errorf("invalid rule %q", text)
			}
			if parts[0] != "ro" && parts[0] != "rw" && parts[0] != "wo" {
				return set{}, fmt.Errorf("invalid mode %q", parts[0])
			}
			if !address && parts[0] == "wo" {
				return set{}, fmt.Errorf("wo is only valid for addresses")
			}
			matcher, err := matcher(parts[1], address)
			if err != nil {
				return set{}, err
			}
			*group.output = append(*group.output, rule{parts[0], matcher})
		}
	}
	return result, nil
}

func matcher(pattern string, address bool) (func(string) bool, error) {
	if len(pattern) > 512 {
		return nil, fmt.Errorf("pattern too long")
	}
	if strings.HasPrefix(pattern, "re:") {
		re, err := regexp.Compile(pattern[3:])
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		return re.MatchString, nil
	}
	// Glob '*' matches any sequence, including folder separators and address domains.
	var b strings.Builder
	b.WriteString("(?i)^")
	for _, part := range strings.Split(pattern, "*") {
		b.WriteString(regexp.QuoteMeta(part))
		b.WriteString(".*")
	}
	text := strings.TrimSuffix(b.String(), ".*") + "$"
	re := regexp.MustCompile(text)
	if address {
		return func(value string) bool { return re.MatchString(strings.ToLower(strings.TrimSpace(value))) }, nil
	}
	return func(value string) bool {
		return re.MatchString(canonicalFolder(value))
	}, nil
}

func permits(rules set, value, action string) bool {
	for _, r := range rules.deny {
		if r.match(value) && (r.mode == "rw" || r.mode == action) {
			return false
		}
	}
	for _, r := range rules.allow {
		if r.match(value) && (r.mode == "rw" || r.mode == action) {
			return true
		}
	}
	return false
}

func (p *Policy) ReadFolder(folder string) bool  { return permits(p.folders, folder, "ro") }
func (p *Policy) WriteFolder(folder string) bool { return permits(p.folders, folder, "wo") }

// ReadMessage requires a permitted folder and optionally a matching address.
// A deny rule for ANY visible address vetoes an otherwise allowed message.
func (p *Policy) ReadMessage(folder string, addresses []string) bool {
	if !p.ReadFolder(folder) {
		return false
	}
	for _, address := range addresses {
		for _, r := range p.addresses.deny {
			if (r.mode == "ro" || r.mode == "rw") && r.match(address) {
				return false
			}
		}
	}
	hasReadAllow := false
	for _, r := range p.addresses.allow {
		if r.mode == "ro" || r.mode == "rw" {
			hasReadAllow = true
			break
		}
	}
	if !hasReadAllow {
		return true
	}
	for _, address := range addresses {
		allowed := false
		for _, r := range p.addresses.allow {
			if (r.mode == "ro" || r.mode == "rw") && r.match(address) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return len(addresses) > 0
}

func canonicalFolder(value string) string {
	if len(value) >= 5 && strings.EqualFold(value[:5], "INBOX") && (len(value) == 5 || value[5] == '/') {
		return "INBOX" + value[5:]
	}
	return value
}

// Send requires an explicit allow for every destination; an empty list is denied.
func (p *Policy) Send(addresses []string) bool {
	if len(addresses) == 0 {
		return false
	}
	for _, address := range addresses {
		if !permits(p.addresses, address, "wo") {
			return false
		}
	}
	return true
}
