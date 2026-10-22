// Package onboard creates a minimal, owner-only Sireno setup interactively.
package onboard

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"atomicgo.dev/keyboard/keys"
	"github.com/pterm/pterm"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
)

type FolderLister func(context.Context, config.Provider) ([]string, error)
type ReadSecret func(string) (string, error)
type FolderSelector func([]string) ([]string, error)
type TextPrompt func(string, string) (string, error)

type Options struct {
	ConfigPath    string
	MarkerPath    string
	Input         io.Reader
	Output        io.Writer
	ReadSecret    ReadSecret
	ListFolders   FolderLister
	SelectFolders FolderSelector
	TextPrompt    TextPrompt
}

func Run(ctx context.Context, options Options) (string, error) {
	if options.ConfigPath == "" || options.MarkerPath == "" {
		return "", errors.New("onboarding paths are required")
	}
	if _, err := os.Stat(options.MarkerPath); err == nil {
		return "", fmt.Errorf("onboarding is already complete; edit %s or use `sirenorelay user token rotate --user <name>`", options.ConfigPath)
	}
	if _, err := os.Stat(options.ConfigPath); err == nil {
		return "", fmt.Errorf("configuration already exists at %s; onboarding will not overwrite it", options.ConfigPath)
	}
	if options.Input == nil || options.Output == nil || options.ReadSecret == nil {
		return "", errors.New("interactive onboarding requires terminal input and output")
	}
	if options.ListFolders == nil {
		options.ListFolders = provider.Himalaya{}.Folders
	}
	if options.SelectFolders == nil {
		options.SelectFolders = selectFolders
	}
	prompt := newPrompter(options.Input, options.Output, options.TextPrompt)
	fmt.Fprintln(options.Output, "Sireno onboard configures IMAP/SMTP only. For other Himalaya backends, use the manual configuration documentation.")
	const name = "default"
	var account config.Provider
	var imapPassword, smtpPassword string
	var folders []string
	for {
		var err error
		account, imapPassword, smtpPassword, err = prompt.provider(options.ReadSecret, account, imapPassword, smtpPassword)
		if err != nil {
			return "", err
		}
		spinner, spinnerErr := pterm.DefaultSpinner.WithWriter(options.Output).Start("Checking connection")
		if spinnerErr == nil {
			spinner.UpdateText("Loading IMAP folders")
		}
		folders, err = options.ListFolders(ctx, accountWithSecrets(account, imapPassword, smtpPassword))
		if spinnerErr == nil {
			if err == nil && len(folders) > 0 {
				spinner.Success(fmt.Sprintf("Loaded %d IMAP folders", len(folders)))
			} else {
				spinner.Fail("Could not load IMAP folders")
			}
		}
		if err == nil && len(folders) > 0 {
			break
		}
		if err != nil {
			fmt.Fprintf(options.Output, "Could not load IMAP folders: %v\n", err)
		} else {
			fmt.Fprintln(options.Output, "Connected to IMAP, but no folders were returned.")
		}
		retry, promptErr := prompt.text("Edit provider credentials and try again? Type yes to continue", "yes")
		if promptErr != nil {
			return "", promptErr
		}
		if retry != "yes" {
			return "", errors.New("mail server connection was not completed")
		}
	}

	secretsDir := filepath.Join(filepath.Dir(options.ConfigPath), "secrets")
	imapSecretPath := filepath.Join(secretsDir, name+"-imap")
	smtpSecretPath := filepath.Join(secretsDir, name+"-smtp")
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(imapSecretPath)
			_ = os.Remove(smtpSecretPath)
			_ = os.Remove(options.ConfigPath)
		}
	}()
	sort.Strings(folders)
	fmt.Fprintln(options.Output, "Use ↑/↓ to move, Space to select, and Enter to continue.")
	chosen, err := options.SelectFolders(folders)
	if err != nil {
		return "", err
	}
	if len(chosen) == 0 {
		return "", errors.New("select at least one folder")
	}
	inbound, err := prompt.addresses("Allowed inbound senders (comma-separated; blank denies all; * allows any sender)")
	if err != nil {
		return "", err
	}
	outbound, err := prompt.addresses("Allowed outbound recipients (comma-separated; blank denies all; * allows any recipient)")
	if err != nil {
		return "", err
	}
	user, err := prompt.text("Agent user name", "agent")
	if err != nil {
		return "", err
	}
	token, hash, err := config.NewToken()
	if err != nil {
		return "", err
	}
	profile := config.Profile{Inbound: config.ContentRule{Addresses: config.Rules{Allow: inbound}}, Outbound: config.ContentRule{Addresses: config.Rules{Allow: outbound}}}
	for _, folder := range chosen {
		profile.Folders.Allow = append(profile.Folders.Allow, "ro:"+folder)
	}
	cfg := &config.Config{Providers: map[string]config.Provider{name: account}, Profiles: map[string]config.Profile{"default": profile}, Users: map[string]config.User{user: {Provider: name, Profile: "default", TokenHash: hash}}}
	if err := config.WriteSecret(imapSecretPath, imapPassword); err != nil {
		return "", err
	}
	if err := config.WriteSecret(smtpSecretPath, smtpPassword); err != nil {
		return "", err
	}
	cfg.Providers[name] = accountWithSecretCommands(account, imapSecretPath, smtpSecretPath)
	if err := config.Write(options.ConfigPath, cfg); err != nil {
		return "", err
	}
	if err := config.WriteSecret(options.MarkerPath, "complete"); err != nil {
		return "", err
	}
	complete = true
	fmt.Fprintf(options.Output, "\nOnboarding complete. Give this bearer token to the agent once:\n%s\n\nRotate it later with: sirenorelay user token rotate --user %s --config %s\n", token, user, options.ConfigPath)
	return token, nil
}

func accountWithSecrets(account config.Provider, imap, smtp string) config.Provider {
	account.IMAP.Password.Value, account.SMTP.Password.Value = imap, smtp
	return account
}
func accountWithSecretCommands(account config.Provider, imap, smtp string) config.Provider {
	account.IMAP.Password.Command, account.SMTP.Password.Command = []string{"cat", imap}, []string{"cat", smtp}
	return account
}

func selectFolders(folders []string) ([]string, error) {
	return pterm.DefaultInteractiveMultiselect.WithOptions(folders).WithMaxHeight(12).WithFilter(false).WithKeyConfirm(keys.Enter).WithKeySelect(keys.Space).Show()
}

// PromptText matches the interactive text-entry behavior used by
// skill-organizer: editable text input with the default value shown in place.
func PromptText(label, fallback string) (string, error) {
	value, err := pterm.DefaultInteractiveTextInput.WithDefaultValue(fallback).Show(label)
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	return strings.TrimSpace(value), nil
}

type prompter struct {
	scanner *bufio.Scanner
	output  io.Writer
	textFn  TextPrompt
}

func newPrompter(input io.Reader, output io.Writer, textFn TextPrompt) *prompter {
	return &prompter{scanner: bufio.NewScanner(input), output: output, textFn: textFn}
}
func (p *prompter) text(label, fallback string) (string, error) {
	if p.textFn != nil {
		return p.textFn(label, fallback)
	}
	fmt.Fprintf(p.output, "%s [%s]: ", label, fallback)
	if !p.scanner.Scan() {
		return "", errors.New("onboarding cancelled")
	}
	value := strings.TrimSpace(p.scanner.Text())
	if value == "" {
		value = fallback
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("invalid input")
	}
	return value, nil
}
func (p *prompter) required(label string) (string, error) {
	for {
		value, err := p.text(label, "")
		if err != nil {
			return "", err
		}
		if value != "" {
			return value, nil
		}
		p.errorf("Error: %s is required. Please try again.", strings.ToLower(label))
	}
}
func (p *prompter) provider(readSecret ReadSecret, previous config.Provider, previousIMAPPassword, previousSMTPPassword string) (config.Provider, string, string, error) {
	address, err := p.emailDefault("Account email address", previous.Address)
	if err != nil {
		return config.Provider{}, "", "", err
	}
	imapHost, err := p.requiredDefault("IMAP host (for example imap.example.org:993)", previous.IMAP.Address)
	if err != nil {
		return config.Provider{}, "", "", err
	}
	imapUser, err := p.text("IMAP username", firstNonEmpty(previous.IMAP.Username, address))
	if err != nil {
		return config.Provider{}, "", "", err
	}
	imapPassword, err := p.secret(readSecret, passwordPrompt("IMAP password or app password", previousIMAPPassword), previousIMAPPassword)
	if err != nil {
		return config.Provider{}, "", "", err
	}
	smtpHost, err := p.requiredDefault("SMTP host (for example smtp.example.org:465)", previous.SMTP.Address)
	if err != nil {
		return config.Provider{}, "", "", err
	}
	smtpUser, err := p.text("SMTP username", firstNonEmpty(previous.SMTP.Username, address))
	if err != nil {
		return config.Provider{}, "", "", err
	}
	smtpFallback := imapPassword
	smtpLabel := "SMTP password or app password (leave blank to use IMAP password)"
	if previousSMTPPassword != "" {
		smtpFallback = previousSMTPPassword
		smtpLabel = passwordPrompt("SMTP password or app password", previousSMTPPassword)
	}
	smtpPassword, err := p.optionalSecret(readSecret, smtpLabel, smtpFallback)
	if err != nil {
		return config.Provider{}, "", "", err
	}
	return config.Provider{Address: address, IMAP: config.Server{Address: imapHost, Username: imapUser}, SMTP: config.Server{Address: smtpHost, Username: smtpUser}}, imapPassword, smtpPassword, nil
}

func (p *prompter) email(label string) (string, error) {
	return p.emailDefault(label, "")
}

func (p *prompter) emailDefault(label, fallback string) (string, error) {
	previous := fallback
	for {
		value, err := p.text(label, previous)
		if err != nil {
			return "", err
		}
		if value == "" {
			p.errorf("Error: %s is required. Please try again.", strings.ToLower(label))
			continue
		}
		parsed, err := mail.ParseAddress(value)
		if err == nil && parsed.Address == value {
			return value, nil
		}
		p.errorf("Error: enter a valid email address. Please try again.")
		previous = value
	}
}

func (p *prompter) requiredDefault(label, fallback string) (string, error) {
	for {
		value, err := p.text(label, fallback)
		if err != nil {
			return "", err
		}
		if value != "" {
			return value, nil
		}
		p.errorf("Error: %s is required. Please try again.", strings.ToLower(label))
	}
}
func (p *prompter) addresses(label string) ([]string, error) {
	for {
		value, err := p.text(label, "")
		if err != nil {
			return nil, err
		}
		if value == "" {
			return nil, nil
		}
		values := strings.Split(value, ",")
		valid := true
		for i := range values {
			values[i] = strings.TrimSpace(values[i])
			if values[i] == "" || strings.ContainsAny(values[i], "\r\n") {
				valid = false
			}
		}
		if !valid {
			p.errorf("Error: enter one or more non-empty address patterns separated by commas.")
			continue
		}
		confirmed := true
		for _, value := range values {
			if value == "*" {
				confirmation, err := p.text("Allow every address? Type yes to confirm", "no")
				if err != nil {
					return nil, err
				}
				confirmed = confirmation == "yes"
			}
		}
		if confirmed {
			return values, nil
		}
		p.errorf("Error: broad address access was not confirmed. Please enter the addresses again.")
	}
}

func (p *prompter) secret(readSecret ReadSecret, label, fallback string) (string, error) {
	for {
		value, err := readSecret(label)
		if err == nil && value != "" {
			return value, nil
		}
		if err == nil && fallback != "" {
			return fallback, nil
		}
		if err != nil {
			p.errorf("Error: %v. Please try again.", err)
		} else {
			p.errorf("Error: a value is required. Please try again.")
		}
	}
}

func (p *prompter) optionalSecret(readSecret ReadSecret, label, fallback string) (string, error) {
	value, err := readSecret(label)
	if err != nil {
		return "", err
	}
	if value == "" {
		return fallback, nil
	}
	return value, nil
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func passwordPrompt(label, previous string) string {
	if previous == "" {
		return label
	}
	return label + " (leave blank to reuse the previous password)"
}

func (p *prompter) errorf(format string, values ...any) {
	fmt.Fprintln(p.output, pterm.Red(fmt.Sprintf(format, values...)))
}
