package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallLinuxUnitWritesOwnerScopedUnit(t *testing.T) {
	previousExecutable, previousSystemctl := executablePath, runUserSystemctl
	executablePath = func() (string, error) { return "/opt/bin/sirenorelay", nil }
	var calls []string
	runUserSystemctl = func(args ...string) error { calls = append(calls, strings.Join(args, " ")); return nil }
	t.Cleanup(func() { executablePath, runUserSystemctl = previousExecutable, previousSystemctl })

	unitPath := filepath.Join(t.TempDir(), "sirenorelay.service")
	configPath := "/home/owner/.config/sirenorelay/config.yml"
	if err := installLinuxUnit(unitPath, configPath); err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	for _, want := range []string{
		`ExecStart="/opt/bin/sirenorelay" serve --config "/home/owner/.config/sirenorelay/config.yml"`,
		"Restart=on-failure",
		"Environment=\"PATH=",
		"NoNewPrivileges=yes",
		"PrivateTmp=yes",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("unit missing %q:\n%s", want, text)
		}
	}
	if got, want := strings.Join(calls, ","), "daemon-reload,enable sirenorelay.service"; got != want {
		t.Fatalf("systemctl calls = %q, want %q", got, want)
	}
}

func TestActioned(t *testing.T) {
	for action, want := range map[string]string{"install": "installed", "start": "started", "stop": "stopped", "restart": "restarted", "uninstall": "uninstalled"} {
		if got := actioned(action); got != want {
			t.Errorf("actioned(%q) = %q, want %q", action, got, want)
		}
	}
}
