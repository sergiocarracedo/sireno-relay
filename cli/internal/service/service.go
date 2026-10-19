// Package service installs and controls the owner-scoped SirenoRELAY service.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	servicepkg "github.com/kardianos/service"
)

const (
	name        = "sirenorelay"
	displayName = "SirenoRELAY"
	description = "Policy-enforced local MCP email relay"
)

var (
	executablePath   = os.Executable
	userHomeDir      = os.UserHomeDir
	runUserSystemctl = func(args ...string) error {
		command := exec.Command("systemctl", append([]string{"--user"}, args...)...)
		output, err := command.CombinedOutput()
		if err == nil {
			return nil
		}
		if message := strings.TrimSpace(string(output)); message != "" {
			return fmt.Errorf("%s", message)
		}
		return err
	}
)

type program struct{}

func (*program) Start(servicepkg.Service) error { return nil }
func (*program) Stop(servicepkg.Service) error  { return nil }

// Control applies one lifecycle action to the current owner's service.
func Control(action, configPath string) (string, error) {
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return "", fmt.Errorf("resolve configuration path: %w", err)
	}
	if runtime.GOOS == "linux" {
		return controlLinux(action, configPath)
	}

	svc, err := newService(configPath)
	if err != nil {
		return "", err
	}
	switch action {
	case "install":
		err = svc.Install()
	case "start":
		err = svc.Start()
	case "stop":
		err = svc.Stop()
	case "restart":
		err = svc.Restart()
	case "uninstall":
		err = svc.Uninstall()
	case "status":
		status, statusErr := svc.Status()
		if statusErr != nil {
			return "", fmt.Errorf("service status: %w", statusErr)
		}
		return formatStatus(status), nil
	default:
		return "", fmt.Errorf("unsupported service action %q", action)
	}
	if err != nil {
		return "", fmt.Errorf("%s service: %w", action, err)
	}
	return actioned(action), nil
}

func newService(configPath string) (servicepkg.Service, error) {
	env := map[string]string{}
	if path := os.Getenv("PATH"); path != "" {
		env["PATH"] = path
	}
	return servicepkg.New(&program{}, &servicepkg.Config{
		Name:        name,
		DisplayName: displayName,
		Description: description,
		Arguments:   []string{"serve", "--config", configPath},
		Option: servicepkg.KeyValue{
			"UserService": true,
		},
		EnvVars: env,
	})
}

func controlLinux(action, configPath string) (string, error) {
	unitPath, err := userUnitPath()
	if err != nil {
		return "", err
	}
	switch action {
	case "install":
		if err := installLinuxUnit(unitPath, configPath); err != nil {
			return "", err
		}
	case "start", "restart":
		if err := installLinuxUnit(unitPath, configPath); err != nil {
			return "", err
		}
		if err := runUserSystemctl(action, name+".service"); err != nil {
			return "", fmt.Errorf("%s service: %w", action, err)
		}
	case "stop":
		if err := runUserSystemctl("stop", name+".service"); err != nil {
			return "", fmt.Errorf("stop service: %w", err)
		}
	case "uninstall":
		_ = runUserSystemctl("stop", name+".service")
		_ = runUserSystemctl("disable", name+".service")
		if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("remove service unit: %w", err)
		}
		if err := runUserSystemctl("daemon-reload"); err != nil {
			return "", fmt.Errorf("reload user systemd: %w", err)
		}
	case "status":
		return linuxStatus()
	default:
		return "", fmt.Errorf("unsupported service action %q", action)
	}
	return actioned(action), nil
}

func installLinuxUnit(unitPath, configPath string) error {
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("create user systemd directory: %w", err)
	}
	executable, err := executablePath()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	unit := fmt.Sprintf(`[Unit]
Description=%s

[Service]
Type=simple
ExecStart=%q serve --config %q
Environment=%q
Restart=on-failure
RestartSec=2
NoNewPrivileges=yes
PrivateTmp=yes

[Install]
WantedBy=default.target
`, description, executable, configPath, "PATH="+os.Getenv("PATH"))
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write user service unit: %w", err)
	}
	if err := runUserSystemctl("daemon-reload"); err != nil {
		return fmt.Errorf("reload user systemd: %w", err)
	}
	if err := runUserSystemctl("enable", name+".service"); err != nil {
		return fmt.Errorf("enable user service: %w", err)
	}
	return nil
}

func userUnitPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", name+".service"), nil
}

func linuxStatus() (string, error) {
	command := exec.Command("systemctl", "--user", "is-active", name+".service")
	output, err := command.CombinedOutput()
	status := strings.TrimSpace(string(output))
	if err == nil && status == "active" {
		return "running", nil
	}
	switch status {
	case "inactive":
		return "stopped", nil
	case "failed":
		return "failed", nil
	case "", "unknown":
		return "unknown", nil
	default:
		return "", fmt.Errorf("service status: %s", status)
	}
}

func actioned(action string) string {
	switch action {
	case "install":
		return "installed"
	case "start":
		return "started"
	case "stop":
		return "stopped"
	case "restart":
		return "restarted"
	case "uninstall":
		return "uninstalled"
	}
	return action
}

func formatStatus(status servicepkg.Status) string {
	switch status {
	case servicepkg.StatusRunning:
		return "running"
	case servicepkg.StatusStopped:
		return "stopped"
	default:
		return "unknown"
	}
}
