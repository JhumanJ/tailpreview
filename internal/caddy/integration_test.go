package caddy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jhumanj/tailpreview/internal/model"
	appPaths "github.com/jhumanj/tailpreview/internal/paths"
	"github.com/jhumanj/tailpreview/internal/process"
)

func TestGeneratedConfigValidatesWithInstalledCaddy(t *testing.T) {
	binary, err := exec.LookPath("caddy")
	if err != nil {
		for _, candidate := range []string{"/opt/homebrew/bin/caddy", "/usr/local/bin/caddy"} {
			if _, statErr := exec.LookPath(candidate); statErr == nil {
				binary = candidate
				err = nil
				break
			}
		}
	}
	if err != nil {
		t.Skip("Caddy is not installed")
	}
	dir := t.TempDir()
	paths := appPaths.Paths{
		LogsDir:   filepath.Join(dir, "logs"),
		CaddySock: filepath.Join(dir, "admin.sock"),
	}
	if err := os.MkdirAll(paths.LogsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := Generate(paths, []model.Preview{{
		ID:          "integration",
		GatewayPort: 18080,
		Routes: []model.Route{
			{Path: "/api/*", Upstream: "http://127.0.0.1:3001"},
			{Path: "/*", Upstream: "http://127.0.0.1:3000"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "caddy.json")
	if err := osWriteFile(path, raw); err != nil {
		t.Fatal(err)
	}
	runner := process.ExecRunner{}
	if result, err := runner.Run(context.Background(), binary, "validate", "--config", path); err != nil {
		t.Fatalf("Caddy rejected generated config: %v\nstdout: %s\nstderr: %s\nconfig:\n%s", err, result.Stdout, result.Stderr, raw)
	}
}

func osWriteFile(path string, data []byte) error {
	return atomicWrite(path, append(data, '\n'))
}
