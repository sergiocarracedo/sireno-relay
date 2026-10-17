package main

import (
	"bytes"
	"context"
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

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	wiresmtp "github.com/emersion/go-smtp"
	"github.com/pterm/pterm"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/audit"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/config"
	proxymcp "github.com/sergiocarracedo/sireno-ai-relay/internal/mcp"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/policy"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/provider"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/relay"
	"github.com/sergiocarracedo/sireno-ai-relay/internal/view"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var (
	findExecutable = exec.LookPath
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
		Use:           "sireno-relay",
		Short:         "Run a policy-enforced local email relay",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		if _, err := findExecutable("himalaya"); err != nil {
			return errors.New("Himalaya CLI is required but was not found in PATH; install it from https://github.com/pimalaya/himalaya#installation, then run `himalaya` to complete its initial setup")
		}
		return nil
	}
	root.AddCommand(newVersionCmd(), newCompletionCmd(root), newConfigCheckCmd(newViper()), newPolicyCheckCmd(newViper()), newServeCmd(newViper()), newMailCmd(newViper()))
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
	bindString(v, cmd.Flags(), "config", "config.yml", "owner-only YAML config")
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
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return serve(cmd.Context(), cmd.OutOrStdout(), v.GetString("config"))
	}
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print build version", Args: usageArgs(cobra.NoArgs), Run: func(cmd *cobra.Command, _ []string) { fmt.Fprintln(cmd.OutOrStdout(), version) }}
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

func serve(ctx context.Context, out io.Writer, path string) error {
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
	logger, closeLogger, err := audit.Open(cfg.Logging)
	if err != nil {
		return err
	}
	defer closeLogger.Close()
	listener, err := net.Listen("tcp", cfg.Listen.MCP)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: proxymcp.HandlerWithAudit(r, logger, cfg.Logging.MCP.CaptureContent)}
	failures := make(chan error, 1)
	go func() { failures <- server.Serve(listener) }()
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

func openRelay(path string) (*relay.Relay, error) {
	store, err := view.Open(filepath.Join(filepath.Dir(path), "sireno-state.json"))
	if err != nil {
		return nil, err
	}
	return &relay.Relay{ConfigPath: path, Provider: provider.Remote{}, Store: store}, nil
}

func info(out io.Writer, values ...any)    { pterm.Info.WithWriter(out).Println(values...) }
func success(out io.Writer, values ...any) { pterm.Success.WithWriter(out).Println(values...) }
func warning(out io.Writer, values ...any) { pterm.Warning.WithWriter(out).Println(values...) }

func newMailCmd(v *viper.Viper) *cobra.Command {
	mail := &cobra.Command{Use: "mail", Short: "Access the relay as an agent"}
	agentFlags(v, mail)
	mail.AddCommand(newMailFoldersCmd(v), newMailListCmd(v), newMailSearchCmd(v), newMailReadCmd(v), newMailSendCmd(v))
	return mail
}

func agentFlags(v *viper.Viper, cmd *cobra.Command) {
	bindString(v, cmd.PersistentFlags(), "user", "", "agent login")
	bindString(v, cmd.PersistentFlags(), "imap", "127.0.0.1:1143", "relay IMAP address")
	bindString(v, cmd.PersistentFlags(), "smtp", "127.0.0.1:1025", "relay SMTP address")
	bindString(v, cmd.PersistentFlags(), "folder", "INBOX", "mailbox")
}

func agentPassword() (string, error) {
	if password := os.Getenv("SIRENO_AGENT_PASSWORD"); password != "" {
		return password, nil
	}
	return "", errors.New("SIRENO_AGENT_PASSWORD is required")
}
func agentUser(v *viper.Viper) (string, error) {
	if user := v.GetString("user"); user != "" {
		return user, nil
	}
	return "", usageError{errors.New("--user is required")}
}

func imapClient(v *viper.Viper) (*imapclient.Client, error) {
	user, err := agentUser(v)
	if err != nil {
		return nil, err
	}
	password, err := agentPassword()
	if err != nil {
		return nil, err
	}
	client, err := imapclient.DialInsecure(v.GetString("imap"), nil)
	if err != nil {
		return nil, err
	}
	if err := client.Login(user, password).Wait(); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

func newMailFoldersCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{Use: "folders", Short: "List visible folders", Args: usageArgs(cobra.NoArgs)}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := imapClient(v)
		if err != nil {
			return err
		}
		defer client.Close()
		boxes, err := client.List("", "*", nil).Collect()
		if err != nil {
			return err
		}
		for _, box := range boxes {
			fmt.Fprintln(cmd.OutOrStdout(), box.Mailbox)
		}
		return nil
	}
	return cmd
}

func newMailListCmd(v *viper.Viper) *cobra.Command   { return newMailSearchLikeCmd(v, "list", false) }
func newMailSearchCmd(v *viper.Viper) *cobra.Command { return newMailSearchLikeCmd(v, "search", true) }
func newMailSearchLikeCmd(v *viper.Viper, name string, needsQuery bool) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: "List visible messages", Args: usageArgs(cobra.NoArgs)}
	if needsQuery {
		bindString(v, cmd.Flags(), "query", "", "search visible message text")
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if needsQuery && v.GetString("query") == "" {
			return usageError{errors.New("search requires --query")}
		}
		client, err := imapClient(v)
		if err != nil {
			return err
		}
		defer client.Close()
		if _, err := client.Select(v.GetString("folder"), nil).Wait(); err != nil {
			return err
		}
		criteria := &imap.SearchCriteria{}
		if needsQuery {
			criteria.Text = []string{v.GetString("query")}
		}
		found, err := client.UIDSearch(criteria, nil).Wait()
		if err != nil {
			return err
		}
		return printEnvelopes(cmd.OutOrStdout(), client, found.AllUIDs())
	}
	return cmd
}

func printEnvelopes(out io.Writer, client *imapclient.Client, ids []imap.UID) error {
	if len(ids) == 0 {
		return nil
	}
	var matches imap.UIDSet
	for _, uid := range ids {
		matches.AddNum(uid)
	}
	items, err := client.Fetch(matches, &imap.FetchOptions{UID: true, Envelope: true}).Collect()
	if err != nil {
		return err
	}
	if len(items) != len(ids) {
		return errors.New("mailbox view changed; retry")
	}
	for _, item := range items {
		if item.Envelope == nil {
			return errors.New("mailbox view changed; retry")
		}
		fmt.Fprintf(out, "%d\t%s\n", item.UID, item.Envelope.Subject)
	}
	return nil
}

func newMailReadCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{Use: "read", Short: "Read a visible message", Args: usageArgs(cobra.NoArgs)}
	cmd.Flags().Uint("uid", 0, "virtual message UID")
	_ = v.BindPFlag("uid", cmd.Flags().Lookup("uid"))
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if v.GetUint("uid") == 0 {
			return usageError{errors.New("read requires --uid")}
		}
		client, err := imapClient(v)
		if err != nil {
			return err
		}
		defer client.Close()
		if _, err := client.Select(v.GetString("folder"), nil).Wait(); err != nil {
			return err
		}
		var ids imap.UIDSet
		ids.AddNum(imap.UID(v.GetUint("uid")))
		message, err := client.Fetch(ids, &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}}).Collect()
		if err != nil {
			return err
		}
		if len(message) != 1 || len(message[0].BodySection) != 1 {
			return errors.New("message unavailable")
		}
		_, err = cmd.OutOrStdout().Write(message[0].BodySection[0].Bytes)
		return err
	}
	return cmd
}

func newMailSendCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{Use: "send", Short: "Send a policy-checked message", Args: usageArgs(cobra.NoArgs)}
	bindString(v, cmd.Flags(), "from", "", "sender address")
	bindString(v, cmd.Flags(), "to", "", "comma-separated recipients")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		user, err := agentUser(v)
		if err != nil {
			return err
		}
		password, err := agentPassword()
		if err != nil {
			return err
		}
		if v.GetString("from") == "" || v.GetString("to") == "" {
			return usageError{errors.New("send requires --from and --to")}
		}
		message, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4*1024*1024+1))
		if err != nil {
			return err
		}
		client, err := wiresmtp.Dial(v.GetString("smtp"))
		if err != nil {
			return err
		}
		defer client.Close()
		if err := client.Auth(sasl.NewPlainClient("", user, password)); err != nil {
			return err
		}
		recipients := strings.Split(v.GetString("to"), ",")
		for i := range recipients {
			recipients[i] = strings.TrimSpace(recipients[i])
		}
		return client.SendMail(v.GetString("from"), recipients, bytes.NewReader(message))
	}
	return cmd
}
