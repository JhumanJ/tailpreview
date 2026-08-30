package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jhumanj/tailpreview/internal/process"
)

type Result struct {
	Platform string `json:"platform"`
	Path     string `json:"path"`
	Action   string `json:"action"`
}

func Install(ctx context.Context, runner process.Runner, binary string) (Result, error) {
	switch runtime.GOOS {
	case "darwin":
		return installLaunchAgent(ctx, runner, binary)
	case "linux":
		return installSystemd(ctx, runner, binary)
	default:
		return Result{}, fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}

func Uninstall(ctx context.Context, runner process.Runner) (Result, error) {
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		path := filepath.Join(home, "Library", "LaunchAgents", "com.tailpreview.gc.plist")
		_, _ = runner.Run(ctx, "launchctl", "bootout", "gui/"+fmt.Sprint(os.Getuid()), path)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return Result{}, err
		}
		return Result{Platform: "launchd", Path: path, Action: "uninstalled"}, nil
	case "linux":
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".config", "systemd", "user")
		_, _ = runner.Run(ctx, "systemctl", "--user", "disable", "--now", "tailpreview-gc.timer")
		for _, name := range []string{"tailpreview-gc.service", "tailpreview-gc.timer"} {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				return Result{}, err
			}
		}
		_, _ = runner.Run(ctx, "systemctl", "--user", "daemon-reload")
		return Result{Platform: "systemd", Path: dir, Action: "uninstalled"}, nil
	default:
		return Result{}, fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
}

func installLaunchAgent(ctx context.Context, runner process.Runner, binary string) (Result, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Join(home, "Library", "LaunchAgents")
	path := filepath.Join(dir, "com.tailpreview.gc.plist")
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- LaunchAgents is a standard user-readable system directory.
		return Result{}, err
	}
	content := renderLaunchAgent(binary)
	if err := atomicWrite(path, []byte(content), 0o644); err != nil {
		return Result{}, err
	}
	_, _ = runner.Run(ctx, "launchctl", "bootout", "gui/"+fmt.Sprint(os.Getuid()), path)
	if _, err := runner.Run(ctx, "launchctl", "bootstrap", "gui/"+fmt.Sprint(os.Getuid()), path); err != nil {
		return Result{}, err
	}
	return Result{Platform: "launchd", Path: path, Action: "installed"}, nil
}

func renderLaunchAgent(binary string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.tailpreview.gc</string>
  <key>ProgramArguments</key>
  <array><string>%s</string><string>gc</string><string>--json</string><string>--non-interactive</string></array>
  <key>StartInterval</key><integer>300</integer>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
`, xmlEscape(binary))
}

func installSystemd(ctx context.Context, runner process.Runner, binary string) (Result, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- systemd user units conventionally live in a 0755 directory.
		return Result{}, err
	}
	service, timer := renderSystemd(binary)
	if err := atomicWrite(filepath.Join(dir, "tailpreview-gc.service"), []byte(service), 0o644); err != nil {
		return Result{}, err
	}
	if err := atomicWrite(filepath.Join(dir, "tailpreview-gc.timer"), []byte(timer), 0o644); err != nil {
		return Result{}, err
	}
	if _, err := runner.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
		return Result{}, err
	}
	if _, err := runner.Run(ctx, "systemctl", "--user", "enable", "--now", "tailpreview-gc.timer"); err != nil {
		return Result{}, err
	}
	return Result{Platform: "systemd", Path: dir, Action: "installed"}, nil
}

func renderSystemd(binary string) (string, string) {
	service := fmt.Sprintf(`[Unit]
Description=Garbage-collect expired Tailpreview previews

[Service]
Type=oneshot
ExecStart=%s gc --json --non-interactive
`, systemdEscape(binary))
	timer := `[Unit]
Description=Run Tailpreview garbage collection every five minutes

[Timer]
OnBootSec=1min
OnUnitActiveSec=5min
Persistent=true

[Install]
WantedBy=timers.target
`
	return service, timer
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tailpreview-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}

func systemdEscape(value string) string {
	return strings.ReplaceAll(value, " ", "\\x20")
}
