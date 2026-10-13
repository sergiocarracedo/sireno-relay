package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

// Config is owned by the relay operator, never by an agent process.
type Config struct {
	Providers map[string]Provider `yaml:"providers"`
	Profiles  map[string]Profile  `yaml:"profiles"`
	Users     map[string]User     `yaml:"users"`
	Listen    Listen              `yaml:"listen"`
}

type Provider struct {
	Address string `yaml:"address"`
	IMAP    Server `yaml:"imap"`
	SMTP    Server `yaml:"smtp"`
}

type Server struct {
	Address         string   `yaml:"address"`
	Username        string   `yaml:"username"`
	PasswordCommand []string `yaml:"password_command"`
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
	DeniedSubjects    []string `yaml:"denied_subjects"`
	DeniedBodies      []string `yaml:"denied_bodies"`
	DeniedExtensions  []string `yaml:"denied_extensions"`
	DenyAttachments   bool     `yaml:"deny_attachments"`
	MaxAttachmentSize int64    `yaml:"max_attachment_size"`
	RemoveLinks       bool     `yaml:"remove_links"`
	Scanner           []string `yaml:"scanner"`
}

type User struct {
	Provider        string   `yaml:"provider"`
	Profile         string   `yaml:"profile"`
	PasswordCommand []string `yaml:"password_command"`
	TagSent         *bool    `yaml:"tag_sent"`
}

type Listen struct {
	IMAP string `yaml:"imap"`
	SMTP string `yaml:"smtp"`
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
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, nil, err
	}
	warnings, err := cfg.Validate()
	return &cfg, warnings, err
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
			if srv.Address == "" || srv.Username == "" || len(srv.PasswordCommand) == 0 || srv.PasswordCommand[0] == "" {
				return nil, fmt.Errorf("provider %q needs IMAP and SMTP addresses, usernames, and password commands", name)
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
		if len(u.PasswordCommand) == 0 || u.PasswordCommand[0] == "" {
			return nil, fmt.Errorf("user %q needs a password command", name)
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
			if content.MaxAttachmentSize < 0 {
				return nil, fmt.Errorf("negative attachment size in profile %q", name)
			}
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
	return warnings, nil
}
