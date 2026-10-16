package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

var environmentReference = regexp.MustCompile(`\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

// Config is owned by the relay operator, never by an agent process.
type Config struct {
	Providers map[string]Provider `yaml:"providers"`
	Profiles  map[string]Profile  `yaml:"profiles"`
	Users     map[string]User     `yaml:"users"`
	Listen    Listen              `yaml:"listen"`
	Logging   Logging             `yaml:"logging"`
}

type Provider struct {
	Address string `yaml:"address"`
	IMAP    Server `yaml:"imap"`
	SMTP    Server `yaml:"smtp"`
}

type Server struct {
	Address  string   `yaml:"address"`
	Username string   `yaml:"username"`
	Password Password `yaml:"password"`
}

// Password accepts either a scalar value (usually ${ENVIRONMENT_VARIABLE})
// or a command argument array. Command values are never passed through a shell.
type Password struct {
	Value   string
	Command []string
}

func (p *Password) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag != "!!str" || node.Value == "" {
			return errors.New("password must be a non-empty string or command array")
		}
		p.Value = node.Value
		p.Command = nil
		return nil
	case yaml.SequenceNode:
		var command []string
		if err := node.Decode(&command); err != nil || len(command) == 0 || command[0] == "" {
			return errors.New("password command must have a program name")
		}
		for _, argument := range command {
			if argument == "" {
				return errors.New("password command cannot contain empty arguments")
			}
		}
		p.Command = command
		p.Value = ""
		return nil
	default:
		return errors.New("password must be a string or command array")
	}
}

type Profile struct {
	Folders   Rules       `yaml:"folders"`
	Addresses Rules       `yaml:"addresses"`
	Inbound   ContentRule `yaml:"inbound"`
	Outbound  ContentRule `yaml:"outbound"`
}

type Rules struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

type ContentRule struct {
	DeniedSubjects []string        `yaml:"denied_subjects"`
	DeniedBodies   []string        `yaml:"denied_bodies"`
	RemoveLinks    bool            `yaml:"remove_links"`
	Scanner        []string        `yaml:"scanner"`
	Attachments    AttachmentRules `yaml:"attachments"`
}

// AttachmentRules is default-deny: an attachment must match allow and must
// not match deny. Patterns are exact MIME types, * globs, or re: expressions.
type AttachmentRules struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
	Mode  string   `yaml:"mode"`
}

type User struct {
	Provider  string   `yaml:"provider"`
	Profile   string   `yaml:"profile"`
	Password  Password `yaml:"password"`
	TokenHash string   `yaml:"token_hash"`
	TagSent   *bool    `yaml:"tag_sent"`
}

type Listen struct {
	IMAP string `yaml:"imap"`
	SMTP string `yaml:"smtp"`
	MCP  string `yaml:"mcp"`
}

type Logging struct {
	Level    string   `yaml:"level"`
	Target   string   `yaml:"target"`
	Rotation Rotation `yaml:"rotation"`
	MCP      MCPLog   `yaml:"mcp"`
}

type Rotation struct {
	RetentionDays int      `yaml:"retention_days"`
	MaxFileSize   ByteSize `yaml:"max_file_size"`
}

// ByteSize accepts a byte count or a human-readable KiB, MiB, or GiB value.
type ByteSize int64

func (s *ByteSize) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return errors.New("log file size must be a scalar")
	}
	value := strings.TrimSpace(node.Value)
	if value == "" {
		return errors.New("log file size cannot be empty")
	}
	multiplier := int64(1)
	upper := strings.ToUpper(value)
	for suffix, factor := range map[string]int64{"KIB": 1 << 10, "MIB": 1 << 20, "GIB": 1 << 30} {
		if strings.HasSuffix(upper, suffix) {
			value, multiplier = strings.TrimSpace(value[:len(value)-len(suffix)]), factor
			break
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || parsed > (1<<63-1)/multiplier {
		return errors.New("invalid log file size")
	}
	*s = ByteSize(parsed * multiplier)
	return nil
}

type MCPLog struct {
	CaptureContent bool `yaml:"capture_content"`
}

func Load(path string) (*Config, []string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
		return nil, nil, errors.New("config must be a regular file accessible only to its owner (mode 0600)")
	}
	if owner, ok := stat.Sys().(*syscall.Stat_t); ok && owner.Uid != uint32(os.Geteuid()) {
		return nil, nil, errors.New("config must be owned by the relay process")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, nil, err
	}
	if err := uniqueKeys(&node); err != nil {
		return nil, nil, err
	}
	if err := expandEnvironment(&node); err != nil {
		return nil, nil, err
	}
	expanded, err := yaml.Marshal(&node)
	if err != nil {
		return nil, nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, nil, err
	}
	warnings, err := cfg.Validate()
	return &cfg, warnings, err
}

// expandEnvironment supports {env:NAME} in string values only. Config keys and
// typed YAML values stay literal so interpolation cannot change its structure.
func expandEnvironment(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		var missing string
		node.Value = environmentReference.ReplaceAllStringFunc(node.Value, func(reference string) string {
			name := environmentReference.FindStringSubmatch(reference)[1]
			value, ok := os.LookupEnv(name)
			if !ok {
				missing = name
				return reference
			}
			return value
		})
		if missing != "" {
			return fmt.Errorf("environment variable %q is not set", missing)
		}
	}
	for _, child := range node.Content {
		if err := expandEnvironment(child); err != nil {
			return err
		}
	}
	return nil
}

func uniqueKeys(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]bool)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if seen[key] {
				return fmt.Errorf("duplicate config key %q", key)
			}
			seen[key] = true
		}
	}
	for _, child := range node.Content {
		if err := uniqueKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) Validate() ([]string, error) {
	var warnings []string
	if len(c.Providers) == 0 || len(c.Users) == 0 {
		return nil, errors.New("at least one provider and user are required")
	}
	for name, p := range c.Providers {
		if p.Address == "" {
			return nil, fmt.Errorf("provider %q needs an email address", name)
		}
		for _, srv := range []Server{p.IMAP, p.SMTP} {
			if srv.Address == "" || srv.Username == "" || (srv.Password.Value == "" && len(srv.Password.Command) == 0) {
				return nil, fmt.Errorf("provider %q needs IMAP and SMTP addresses, usernames, and passwords", name)
			}
		}
	}
	for name, u := range c.Users {
		if strings.ContainsAny(name, "\r\n:") || name == "" {
			return nil, fmt.Errorf("invalid user name %q", name)
		}
		if _, ok := c.Providers[u.Provider]; !ok {
			return nil, fmt.Errorf("user %q references unknown provider", name)
		}
		if _, ok := c.Profiles[u.Profile]; !ok {
			return nil, fmt.Errorf("user %q references unknown profile", name)
		}
		if u.Password.Value == "" && len(u.Password.Command) == 0 && u.TokenHash == "" {
			return nil, fmt.Errorf("user %q needs a password or token_hash", name)
		}
		if u.TokenHash != "" && !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(u.TokenHash) {
			return nil, fmt.Errorf("user %q has an invalid token_hash", name)
		}
	}
	for name, profile := range c.Profiles {
		entries := append([]string{}, profile.Folders.Allow...)
		entries = append(entries, profile.Folders.Deny...)
		entries = append(entries, profile.Addresses.Allow...)
		entries = append(entries, profile.Addresses.Deny...)
		for _, entry := range entries {
			parts := strings.SplitN(entry, ":", 2)
			if len(parts) != 2 || parts[1] == "" || (parts[0] != "ro" && parts[0] != "rw" && parts[0] != "wo") {
				return nil, fmt.Errorf("invalid rule in profile %q", name)
			}
			if len(parts[1]) > 512 {
				return nil, fmt.Errorf("oversized rule in profile %q", name)
			}
			if strings.HasPrefix(parts[1], "re:") {
				if _, err := regexp.Compile(parts[1][3:]); err != nil {
					return nil, fmt.Errorf("invalid regex in profile %q: %w", name, err)
				}
			}
		}
		for _, entry := range append(append([]string{}, profile.Folders.Allow...), profile.Folders.Deny...) {
			if strings.HasPrefix(entry, "wo:") {
				return nil, fmt.Errorf("write-only folder rule in profile %q", name)
			}
		}
		for _, content := range []ContentRule{profile.Inbound, profile.Outbound} {
			for _, expr := range append(append([]string{}, content.DeniedSubjects...), content.DeniedBodies...) {
				if len(expr) > 512 {
					return nil, fmt.Errorf("oversized content rule in profile %q", name)
				}
				if strings.HasPrefix(expr, "re:") {
					if _, err := regexp.Compile(expr[3:]); err != nil {
						return nil, fmt.Errorf("invalid content regex in profile %q: %w", name, err)
					}
				}
			}
			if len(content.Scanner) > 0 && content.Scanner[0] == "" {
				return nil, fmt.Errorf("empty scanner command in profile %q", name)
			}
			for _, pattern := range append(append([]string{}, content.Attachments.Allow...), content.Attachments.Deny...) {
				if len(pattern) == 0 || len(pattern) > 512 {
					return nil, fmt.Errorf("invalid attachment MIME pattern in profile %q", name)
				}
				if strings.HasPrefix(pattern, "re:") {
					if _, err := regexp.Compile(pattern[3:]); err != nil {
						return nil, fmt.Errorf("invalid attachment MIME regex in profile %q: %w", name, err)
					}
				}
			}
			if mode := content.Attachments.Mode; mode != "" && mode != "reject" && mode != "remove" && mode != "silent-remove" {
				return nil, fmt.Errorf("invalid attachment mode in profile %q", name)
			}
		}
		for _, rule := range profile.Folders.Allow {
			if rule == "ro:*" || rule == "rw:*" {
				warnings = append(warnings, fmt.Sprintf("profile %q grants read access to all folders", name))
			}
		}
	}
	for _, addr := range []string{c.Listen.IMAP, c.Listen.SMTP} {
		if addr == "" {
			continue
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid listener %q: %w", addr, err)
		}
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("listener %q must bind loopback", addr)
		}
	}
	if c.Listen.MCP != "" {
		if _, _, err := net.SplitHostPort(c.Listen.MCP); err != nil {
			return nil, fmt.Errorf("invalid MCP listener %q: %w", c.Listen.MCP, err)
		}
	}
	if c.Logging.Level != "" && c.Logging.Level != "error" && c.Logging.Level != "warn" && c.Logging.Level != "info" && c.Logging.Level != "debug" {
		return nil, fmt.Errorf("invalid logging level %q", c.Logging.Level)
	}
	if c.Logging.Target != "" && c.Logging.Target != "stdout" && !filepath.IsAbs(c.Logging.Target) {
		return nil, errors.New("logging target must be stdout or an absolute path")
	}
	if c.Logging.MCP.CaptureContent && (c.Logging.Target == "" || c.Logging.Target == "stdout") {
		return nil, errors.New("MCP content capture requires an owner-only file logging target")
	}
	if c.Logging.Rotation.RetentionDays < 0 || c.Logging.Rotation.MaxFileSize < 0 {
		return nil, errors.New("logging rotation values cannot be negative")
	}
	return warnings, nil
}
