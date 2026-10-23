package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pterm/pterm"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/audit"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/mail"
	proxymcp "github.com/sergiocarracedo/sireno-ai-relay/internal/mcp"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/onboard"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/policy"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/relay"
	serviceinternal "github.com/sergiocarracedo/sireno-ai-relay/internal/service"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/view"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

var (
	findExecutable = exec.LookPath
	isTerminal     = term.IsTerminal
	version        = "dev"
)

type usageError struct{ error }

func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRootCmd(os.Stdout, os.Stderr).ExecuteContext(ctx); err != nil {
		pterm.Error.WithWriter(os.Stderr).Println(err)
		if _, ok := err.(usageError); ok {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// run remains small and test-friendly while main owns process exit behavior.
func run(args []string, out io.Writer) error {
	cmd := newRootCmd(out, io.Discard)
	cmd.SetArgs(args)
	return cmd.ExecuteContext(context.Background())
}

func newRootCmd(out, errOut io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "sirenorelay",
		Short:         "Run a policy-enforced local email relay",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Parent() != nil && cmd.Parent().Name() == "service" {
			return nil
		}
		if _, err := findExecutable("himalaya"); err != nil {
			return errors.New("Himalaya CLI is required but was not found in PATH; install it from https://github.com/pimalaya/himalaya#installation")
		}
		return nil
	}
	root.AddCommand(newVersionCmd(), newCompletionCmd(root), newConfigCheckCmd(newViper()), newPolicyCheckCmd(newViper()), newServeCmd(newViper()), newOnboardCmd(), newUserCmd(), newServiceCmd(newViper()))
	return root
}

func newViper() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("SIRENO")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	return v
}

func bindString(v *viper.Viper, flags *pflag.FlagSet, name, value, usage string) {
	flags.String(name, value, usage)
	_ = v.BindPFlag(name, flags.Lookup(name))
}

func configFlag(v *viper.Viper, cmd *cobra.Command) {
	bindString(v, cmd.Flags(), "config", defaultConfigPath(), "owner-only YAML config")
}

func defaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "config.yml"
	}
	path := filepath.Join(dir, "sirenorelay", "config.yml")
	if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return path
	}
	legacyPath := filepath.Join(dir, "sireno-relay", "config.yml")
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath
	}
	return path
}

func newConfigCheckCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{Use: "config-check", Short: "Validate the owner configuration", Args: usageArgs(cobra.NoArgs)}
	configFlag(v, cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		_, _, err := loadConfig(v.GetString("config"), cmd.OutOrStdout())
		if err != nil {
			return err
		}
		success(cmd.OutOrStdout(), "configuration valid")
		return nil
	}
	return cmd
}

func newPolicyCheckCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{Use: "policy-check", Short: "Check a user's configured mail policy", Args: usageArgs(cobra.NoArgs)}
	configFlag(v, cmd)
	bindString(v, cmd.Flags(), "user", "", "agent name")
	bindString(v, cmd.Flags(), "folder", "", "mailbox folder for inbound checks")
	cmd.Flags().Bool("live", false, "count real messages visible in --folder")
	cmd.Flags().Bool("inbound", false, "evaluate an inbound message")
	cmd.Flags().Bool("outbound", false, "evaluate an outbound message")
	cmd.Flags().String("message", "", "RFC 822 message file to evaluate")
	cmd.Flags().StringArray("sender", nil, "inbound sender (repeatable)")
	cmd.Flags().StringArray("recipient", nil, "outbound envelope recipient (repeatable)")
	cmd.Flags().String("subject", "", "message subject")
	cmd.Flags().String("body", "", "plain-text message body")
	cmd.Flags().StringArray("attachment", nil, "attachment file (repeatable)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		user, folder := v.GetString("user"), v.GetString("folder")
		live, _ := cmd.Flags().GetBool("live")
		inbound, _ := cmd.Flags().GetBool("inbound")
		outbound, _ := cmd.Flags().GetBool("outbound")
		message, _ := cmd.Flags().GetString("message")
		senders, _ := cmd.Flags().GetStringArray("sender")
		recipients, _ := cmd.Flags().GetStringArray("recipient")
		subject, _ := cmd.Flags().GetString("subject")
		body, _ := cmd.Flags().GetString("body")
		attachments, _ := cmd.Flags().GetStringArray("attachment")

		if user == "" {
			return usageError{errors.New("policy-check requires --user")}
		}
		if live {
			if inbound || outbound || message != "" || len(senders) > 0 || len(recipients) > 0 || cmd.Flags().Changed("subject") || cmd.Flags().Changed("body") || len(attachments) > 0 {
				return usageError{errors.New("--live cannot be combined with message evaluation flags")}
			}
			if folder == "" {
				return usageError{errors.New("--live requires --folder")}
			}
			return policyCheckLive(cmd.Context(), cmd.OutOrStdout(), v.GetString("config"), user, folder)
		}
		if inbound == outbound {
			return usageError{errors.New("policy-check requires exactly one of --live, --inbound, or --outbound")}
		}
		if inbound && folder == "" {
			return usageError{errors.New("--inbound requires --folder")}
		}
		if message != "" && (len(senders) > 0 || cmd.Flags().Changed("subject") || cmd.Flags().Changed("body") || len(attachments) > 0) {
			return usageError{errors.New("--message cannot be combined with --sender, --subject, --body, or --attachment")}
		}
		if message == "" {
			if inbound && len(senders) == 0 {
				return usageError{errors.New("simulated inbound checks require --sender")}
			}
			if outbound && len(recipients) == 0 {
				return usageError{errors.New("simulated outbound checks require --recipient")}
			}
			if body == "" && len(attachments) == 0 {
				return usageError{errors.New("simulated checks require --body or --attachment")}
			}
		}
		return policyCheckMessage(cmd.OutOrStdout(), v.GetString("config"), user, folder, inbound, message, senders, recipients, subject, body, attachments)
	}
	return cmd
}

func newServeCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{Use: "serve", Short: "Serve the token-authenticated MCP relay", Args: usageArgs(cobra.NoArgs)}
	configFlag(v, cmd)
	cmd.Flags().Bool("debug", false, "write sanitized MCP metadata to stdout")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		debug, err := cmd.Flags().GetBool("debug")
		if err != nil {
			return err
		}
		return serve(cmd.Context(), cmd.OutOrStdout(), v.GetString("config"), debug)
	}
	return cmd
}

func newServiceCmd(v *viper.Viper) *cobra.Command {
	serviceCmd := &cobra.Command{Use: "service", Short: "Manage the owner-scoped MCP relay service"}
	bindString(v, serviceCmd.PersistentFlags(), "config", defaultConfigPath(), "owner-only YAML config")
	for _, action := range []string{"install", "start", "stop", "restart", "status", "uninstall"} {
		action := action
		command := &cobra.Command{
			Use:   action,
			Short: serviceCommandDescription(action),
			Args:  usageArgs(cobra.NoArgs),
			RunE: func(cmd *cobra.Command, _ []string) error {
				path := v.GetString("config")
				if action == "install" || action == "start" || action == "restart" {
					if _, _, err := loadConfig(path, cmd.OutOrStdout()); err != nil {
						return err
					}
					if _, err := findExecutable("himalaya"); err != nil {
						return errors.New("Himalaya CLI is required but was not found in PATH; install it from https://github.com/pimalaya/himalaya#installation")
					}
				}
				status, err := serviceinternal.Control(action, path)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Service %s: %s\n", action, status)
				return nil
			},
		}
		serviceCmd.AddCommand(command)
	}
	return serviceCmd
}

func serviceCommandDescription(action string) string {
	return map[string]string{
		"install":   "Install the owner-scoped MCP relay service",
		"start":     "Start the MCP relay service",
		"stop":      "Stop the MCP relay service",
		"restart":   "Restart the MCP relay service",
		"status":    "Show MCP relay service status",
		"uninstall": "Uninstall the MCP relay service",
	}[action]
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print build version", Args: usageArgs(cobra.NoArgs), Run: func(cmd *cobra.Command, _ []string) { fmt.Fprintln(cmd.OutOrStdout(), version) }}
}

func newOnboardCmd() *cobra.Command {
	return &cobra.Command{Use: "onboard", Short: "Interactively create an owner-only IMAP/SMTP configuration", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if !isTerminal(int(os.Stdin.Fd())) || !isTerminal(int(os.Stdout.Fd())) {
			return errors.New("onboard requires an interactive terminal")
		}
		path := defaultConfigPath()
		dir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		_, err = onboard.Run(cmd.Context(), onboard.Options{ConfigPath: path, MarkerPath: filepath.Join(dir, "sirenorelay", "onboard"), Input: os.Stdin, Output: cmd.OutOrStdout(), ReadSecret: terminalSecret, TextPrompt: onboard.PromptText})
		return err
	}}
}

func terminalSecret(label string) (string, error) {
	value, err := pterm.DefaultInteractiveTextInput.WithMask("*").Show(label)
	if err != nil {
		return "", errors.New("could not read secret")
	}
	return value, nil
}

func newUserCmd() *cobra.Command {
	userCmd := &cobra.Command{Use: "user", Short: "Manage agent bearer tokens"}
	tokenCmd := &cobra.Command{Use: "token", Short: "Manage bearer tokens"}
	v := newViper()
	rotate := &cobra.Command{Use: "rotate", Short: "Rotate an agent bearer token", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		if !isTerminal(int(os.Stdout.Fd())) {
			return errors.New("token rotation requires an interactive terminal")
		}
		user := v.GetString("user")
		if user == "" {
			return usageError{errors.New("rotate requires --user")}
		}
		token, err := config.RotateToken(v.GetString("config"), user)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "New bearer token for %s (shown once):\n%s\n", user, token)
		return nil
	}}
	configFlag(v, rotate)
	bindString(v, rotate.Flags(), "user", "", "agent user name")
	tokenCmd.AddCommand(rotate)
	userCmd.AddCommand(tokenCmd)
	return userCmd
}

func newCompletionCmd(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{Use: "completion [bash|zsh|fish|powershell]", Short: "Generate shell completion", Args: usageArgs(cobra.ExactArgs(1))}
	cmd.ValidArgs = []string{"bash", "zsh", "fish", "powershell"}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return root.GenBashCompletion(cmd.OutOrStdout())
		case "zsh":
			return root.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			return root.GenFishCompletion(cmd.OutOrStdout(), true)
		case "powershell":
			return root.GenPowerShellCompletion(cmd.OutOrStdout())
		default:
			return usageError{fmt.Errorf("unsupported shell %q", args[0])}
		}
	}
	return cmd
}

func loadConfig(path string, out io.Writer) (*config.Config, []string, error) {
	cfg, warnings, err := config.Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, errors.New("no configuration found; run `sirenorelay onboard`")
		}
		return nil, nil, err
	}
	for name, profile := range cfg.Profiles {
		if _, err := policy.Compile(profile); err != nil {
			return nil, nil, fmt.Errorf("profile %q: %w", name, err)
		}
	}
	for _, text := range warnings {
		warning(out, text)
	}
	return cfg, warnings, nil
}

func policyCheckLive(ctx context.Context, out io.Writer, path, user, folder string) error {
	if _, _, err := loadConfig(path, out); err != nil {
		return err
	}
	r, err := openRelay(path)
	if err != nil {
		return err
	}
	identity, err := r.Identity(user)
	if err != nil {
		return err
	}
	if !identity.Policy.ReadFolder(folder) {
		warning(out, "folder denied")
		return nil
	}
	snapshot, err := r.Snapshot(ctx, identity, folder)
	if err != nil {
		return err
	}
	info(out, fmt.Sprintf("folder allowed; visible messages: %d", len(snapshot.Messages)))
	return nil
}

func policyCheckMessage(out io.Writer, path, user, folder string, inbound bool, messagePath string, senders, recipients []string, subject, body string, attachmentPaths []string) error {
	if _, _, err := loadConfig(path, out); err != nil {
		return err
	}
	r, err := openRelay(path)
	if err != nil {
		return err
	}
	identity, err := r.Identity(user)
	if err != nil {
		return err
	}
	raw, err := policyCheckRawMessage(messagePath, identity.Account.Address, inbound, senders, recipients, subject, body, attachmentPaths)
	if err != nil {
		return err
	}
	rule := identity.Profile.Outbound
	if inbound {
		rule = identity.Profile.Inbound
	}
	message, err := mail.Evaluate(raw, rule)
	if err != nil {
		warning(out, "message denied by content policy")
		return nil
	}
	if inbound {
		if !identity.Policy.ReadFolder(folder) {
			warning(out, "folder denied")
			return nil
		}
		if !identity.Policy.ReadMessage(folder, message.From) {
			warning(out, "message denied by inbound address policy")
			return nil
		}
		policyCheckAllowed(out, "inbound", message.RemovedAttachments)
		return nil
	}
	if len(message.From) != 1 || !strings.EqualFold(message.From[0], identity.Account.Address) {
		warning(out, "message denied: From must be the configured provider address")
		return nil
	}
	all := append([]string{}, recipients...)
	all = append(all, message.To...)
	all = append(all, message.Cc...)
	all = append(all, message.Bcc...)
	if !identity.Policy.Send(all) {
		warning(out, "message denied by outbound address policy")
		return nil
	}
	policyCheckAllowed(out, "outbound", message.RemovedAttachments)
	return nil
}

func policyCheckAllowed(out io.Writer, direction string, removedAttachments bool) {
	message := direction + " message allowed"
	if removedAttachments {
		message += "; one or more attachments removed by policy"
	}
	info(out, message)
}

func policyCheckRawMessage(messagePath, from string, inbound bool, senders, recipients []string, subject, body string, attachmentPaths []string) ([]byte, error) {
	if messagePath != "" {
		file, err := os.Open(messagePath)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		return io.ReadAll(io.LimitReader(file, mail.MaxMessage+1))
	}
	if !safePolicyCheckHeader(subject) || !safePolicyCheckHeaders(senders) || !safePolicyCheckHeaders(recipients) {
		return nil, usageError{errors.New("sender, recipient, and subject must not contain line breaks")}
	}
	if inbound {
		from = strings.Join(senders, ", ")
	}
	var raw bytes.Buffer
	fmt.Fprintf(&raw, "From: %s\r\n", from)
	if len(recipients) > 0 {
		fmt.Fprintf(&raw, "To: %s\r\n", strings.Join(recipients, ", "))
	}
	fmt.Fprintf(&raw, "Subject: %s\r\n", subject)
	if len(attachmentPaths) == 0 {
		raw.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		raw.WriteString(body)
		return raw.Bytes(), nil
	}
	writer := multipart.NewWriter(&raw)
	fmt.Fprintf(&raw, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", writer.Boundary())
	textHeader := textproto.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}}
	text, err := writer.CreatePart(textHeader)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(text, body); err != nil {
		return nil, err
	}
	for _, path := range attachmentPaths {
		data, err := readPolicyCheckAttachment(path)
		if err != nil {
			return nil, err
		}
		mediaType := mime.TypeByExtension(filepath.Ext(path))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		mediaType, _, err = mime.ParseMediaType(mediaType)
		if err != nil {
			return nil, err
		}
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)})
		part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {mediaType}, "Content-Disposition": {disposition}})
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return raw.Bytes(), nil
}

func readPolicyCheckAttachment(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, mail.MaxAttachment+1))
	if err != nil {
		return nil, err
	}
	if len(data) > mail.MaxAttachment {
		return nil, fmt.Errorf("attachment %q exceeds %d bytes", path, mail.MaxAttachment)
	}
	return data, nil
}

func safePolicyCheckHeader(value string) bool {
	return !strings.ContainsAny(value, "\r\n")
}

func safePolicyCheckHeaders(values []string) bool {
	for _, value := range values {
		if !safePolicyCheckHeader(value) {
			return false
		}
	}
	return true
}

func serve(ctx context.Context, out io.Writer, path string, debug bool) error {
	cfg, _, err := loadConfig(path, out)
	if err != nil {
		return err
	}
	r, err := openRelay(path)
	if err != nil {
		return err
	}
	if cfg.Listen.MCP == "" {
		cfg.Listen.MCP = "127.0.0.1:8080"
	}
	logging := cfg.Logging
	if debug {
		logging.Level = "debug"
		logging.Target = "stdout"
		logging.MCP.CaptureContent = true
	}
	logger, closeLogger, err := audit.Open(logging)
	if err != nil {
		return err
	}
	defer closeLogger.Close()
	listener, err := net.Listen("tcp", cfg.Listen.MCP)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: proxymcp.HandlerWithAudit(r, logger, logging.MCP.CaptureContent)}
	failures := make(chan error, 1)
	go func() { failures <- server.Serve(listener) }()
	go watchConfigChanges(ctx, path, out)
	success(out, fmt.Sprintf("MCP relay listening on %s", cfg.Listen.MCP))
	select {
	case <-ctx.Done():
		return server.Shutdown(context.Background())
	case err := <-failures:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// watchConfigChanges reports owner edits. Relay operations independently reload
// this file, so a successful notice means subsequent requests use the change.
func watchConfigChanges(ctx context.Context, path string, out io.Writer) {
	current, err := configHash(path)
	if err != nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next, err := configHash(path)
			if err != nil || next == current {
				continue
			}
			current = next
			if _, _, err := loadConfig(path, io.Discard); err != nil {
				warning(out, fmt.Sprintf("configuration changed but is invalid: %v", err))
				continue
			}
			info(out, fmt.Sprintf("configuration changed and reloaded: %s", filepath.Base(path)))
		}
	}
}

func configHash(path string) ([sha256.Size]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(data), nil
}

func openRelay(path string) (*relay.Relay, error) {
	store, err := view.Open(filepath.Join(filepath.Dir(path), "sireno-state.json"))
	if err != nil {
		return nil, err
	}
	return &relay.Relay{ConfigPath: path, Provider: provider.Himalaya{}, Store: store}, nil
}

func info(out io.Writer, values ...any)    { pterm.Info.WithWriter(out).Println(values...) }
func success(out io.Writer, values ...any) { pterm.Success.WithWriter(out).Println(values...) }
func warning(out io.Writer, values ...any) { pterm.Warning.WithWriter(out).Println(values...) }
