package caddy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jhumanj/tailpreview/internal/model"
	appPaths "github.com/jhumanj/tailpreview/internal/paths"
	"github.com/jhumanj/tailpreview/internal/process"
)

func TestManagerRunsDedicatedCaddyAndRoutesRequests(t *testing.T) {
	binary := findCaddy(t)
	root, err := os.MkdirTemp("/tmp", "tailpreview-caddy-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	paths := appPaths.Paths{
		ConfigDir:  filepath.Join(root, "config"),
		StateDir:   filepath.Join(root, "state"),
		RuntimeDir: filepath.Join(root, "run"),
		LogsDir:    filepath.Join(root, "logs"),
		CaddyJSON:  filepath.Join(root, "run", "caddy.json"),
		CaddySock:  filepath.Join(root, "run", "admin.sock"),
		CaddyPID:   filepath.Join(root, "run", "caddy.pid"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s %s", r.Method, r.URL.Path)
	}))
	defer upstream.Close()
	gatewayPort := freePort(t)
	preview := model.Preview{
		ID:          "real-caddy",
		GatewayPort: gatewayPort,
		Routes: []model.Route{
			{Path: "/api/*", Upstream: upstream.URL, StripPrefix: true},
			{Path: "/*", Upstream: upstream.URL},
		},
	}
	manager := Manager{Binary: binary, Paths: paths, Runner: process.ExecRunner{}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	defer manager.Stop(context.Background())
	if err := manager.Apply(ctx, []model.Preview{preview}); err != nil {
		t.Fatal(err)
	}
	active, err := manager.ActiveRequests(ctx)
	if err != nil {
		t.Fatalf("query active requests: %v", err)
	}
	upstreamHost := strings.TrimPrefix(upstream.URL, "http://")
	if _, ok := active[upstreamHost]; !ok {
		t.Fatalf("active-request map does not include %s: %#v", upstreamHost, active)
	}
	assertBody := func(path, expected string) {
		t.Helper()
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", gatewayPort, path))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != expected {
			t.Fatalf("%s returned %q, expected %q", path, body, expected)
		}
	}
	assertBody("/api/users?secret=must-not-persist", "GET /users")
	assertBody("/dashboard", "GET /dashboard")
	if err := manager.Apply(ctx, []model.Preview{preview}); err != nil {
		t.Fatalf("idempotent apply failed: %v", err)
	}
	logPath := filepath.Join(paths.LogsDir, preview.ID+".jsonl")
	deadline := time.Now().Add(2 * time.Second)
	for {
		raw, readErr := os.ReadFile(logPath)
		if readErr == nil && len(raw) > 0 {
			text := string(raw)
			if strings.Contains(text, "must-not-persist") || strings.Contains(text, "remote_ip") || strings.Contains(text, "headers") {
				t.Fatalf("privacy filter leaked sensitive request metadata: %s", text)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("access log was not written: %v", readErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func findCaddy(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"caddy", "/opt/homebrew/bin/caddy", "/usr/local/bin/caddy"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	t.Skip("Caddy is not installed")
	return ""
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
