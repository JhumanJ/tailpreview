package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Paths struct {
	ConfigDir  string `json:"config_dir"`
	StateDir   string `json:"state_dir"`
	RuntimeDir string `json:"runtime_dir"`
	LogsDir    string `json:"logs_dir"`
	Registry   string `json:"registry"`
	Lock       string `json:"lock"`
	CaddyJSON  string `json:"caddy_json"`
	CaddySock  string `json:"caddy_socket"`
	CaddyPID   string `json:"caddy_pid"`
}

func Resolve() (Paths, error) {
	if root := os.Getenv("TAILPREVIEW_HOME"); root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return Paths{}, err
		}
		return fromRoots(filepath.Join(abs, "config"), filepath.Join(abs, "state")), nil
	}
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve config directory: %w", err)
	}
	configDir := filepath.Join(configRoot, "tailpreview")
	var stateDir string
	if configured := os.Getenv("XDG_STATE_HOME"); configured != "" {
		stateDir = filepath.Join(configured, "tailpreview")
	} else if runtime.GOOS == "darwin" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return Paths{}, homeErr
		}
		stateDir = filepath.Join(home, "Library", "Application Support", "tailpreview")
	} else {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return Paths{}, homeErr
		}
		stateDir = filepath.Join(home, ".local", "state", "tailpreview")
	}
	return fromRoots(configDir, stateDir), nil
}

func (p Paths) Ensure() error {
	for _, dir := range []string{p.ConfigDir, p.StateDir, p.RuntimeDir, p.LogsDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- this is a directory and 0700 is intentionally owner-only.
			return err
		}
	}
	return nil
}

func fromRoots(configDir, stateDir string) Paths {
	runtimeDir := filepath.Join(stateDir, "runtime")
	return Paths{
		ConfigDir:  configDir,
		StateDir:   stateDir,
		RuntimeDir: runtimeDir,
		LogsDir:    filepath.Join(stateDir, "logs"),
		Registry:   filepath.Join(stateDir, "registry.json"),
		Lock:       filepath.Join(stateDir, "registry.lock"),
		CaddyJSON:  filepath.Join(runtimeDir, "caddy.json"),
		CaddySock:  filepath.Join(runtimeDir, "caddy-admin.sock"),
		CaddyPID:   filepath.Join(runtimeDir, "caddy.pid"),
	}
}
