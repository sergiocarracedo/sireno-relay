package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
	proxymcp "github.com/sergiocarracedo/sireno-ai-relay/internal/mcp"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/onboard"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/policy"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/relay"
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
	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		if _, err := findExecutable("himalaya"); err != nil {
			return errors.New("Himalaya CLI is required but was not found in PATH; install it from https://github.com/pimalaya/himalaya#installation")
		}
		return nil
	}
	root.AddCommand(newVersionCmd(), newCompletionCmd(root), newConfigCheckCmd(newViper()), newPolicyCheckCmd(newViper()), newServeCmd(newViper()), newOnboardCmd(), newUserCmd())
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
	return filepath.Join(dir, "sirenorelay", "config.yml")
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
	cmd := &cobra.Command{Use: "policy-check", Short: "Check a user's policy against a mailbox", Args: usageArgs(cobra.NoArgs)}
	configFlag(v, cmd)
	bindString(v, cmd.Flags(), "user", "", "agent name")
	bindString(v, cmd.Flags(), "folder", "", "folder to inspect")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if v.GetString("user") == "" || v.GetString("folder") == "" {
			return usageError{errors.New("policy-check requires --user and --folder")}
		}
		return policyCheck(cmd.Context(), cmd.OutOrStdout(), v.GetString("config"), v.GetString("user"), v.GetString("folder"))
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
	fmt.Fprintf(os.Stderr, "%s: ", label)
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", errors.New("could not read secret")
	}
	if len(value) == 0 {
		return "", errors.New("secret is required")
	}
	return string(value), nil
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

func policyCheck(ctx context.Context, out io.Writer, path, user, folder string) error {
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
