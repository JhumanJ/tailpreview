package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jhumanj/tailpreview/internal/config"
	"github.com/jhumanj/tailpreview/internal/model"
	appPaths "github.com/jhumanj/tailpreview/internal/paths"
	"github.com/jhumanj/tailpreview/internal/process"
	"github.com/jhumanj/tailpreview/internal/state"
	"github.com/jhumanj/tailpreview/internal/tailscale"
)

type fakeBinaryCaddy struct {
	binary string
	runner process.Runner
}

func (f fakeBinaryCaddy) Apply(ctx context.Context, previews []model.Preview) error {
	_, err := f.runner.Run(ctx, f.binary, "apply", strconv.Itoa(len(previews)))
	return err
}

func (f fakeBinaryCaddy) Stop(ctx context.Context) error {
	_, err := f.runner.Run(ctx, f.binary, "stop")
	return err
}

func (f fakeBinaryCaddy) ActiveRequests(context.Context) (map[string]int, error) {
	return map[string]int{}, nil
}

func TestUpWithFakeCaddyAndTailscaleBinariesRollsBackStructuredHostRejection(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	origin, transport := publicTLSTestOrigin(t, server, "dev-mini.example.ts.net")
	serverURL := strings.TrimPrefix(server.URL, "https://")
	_, rawPort, err := net.SplitHostPort(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	tailscaleState := filepath.Join(root, "tailscale-state.json")
	caddyLog := filepath.Join(root, "caddy.log")
	tailscaleBinary := filepath.Join(root, "tailscale")
	caddyBinary := filepath.Join(root, "caddy")
	writeExecutable(t, tailscaleBinary, fmt.Sprintf(`#!/bin/sh
state=%q
if [ "$1" = "status" ]; then
  printf '%%s\n' '{"BackendState":"Running","Self":{"DNSName":"dev-mini.example.ts.net.","Online":true},"CurrentTailnet":{"MagicDNSEnabled":true}}'
  exit 0
fi
if [ "$1" = "serve" ] && [ "$2" = "status" ]; then
  if [ -s "$state" ]; then cat "$state"; else printf '%%s\n' '{}'; fi
  exit 0
fi
if [ "$1" = "serve" ]; then
  case " $* " in
    *" off "*) printf '%%s\n' '{}' > "$state" ;;
    *)
      selected=""
      for arg in "$@"; do case "$arg" in --https=*) selected=${arg#--https=} ;; esac; done
      printf '{"Web":{"https://127.0.0.1:%%s":{"Handlers":{}}}}\n' "$selected" > "$state"
      printf '%%s\n' 'Available within your tailnet'
      ;;
  esac
  exit 0
fi
exit 1
`, tailscaleState))
	writeExecutable(t, caddyBinary, fmt.Sprintf("#!/bin/sh\nprintf '%%s %%s\\n' \"$1\" \"$2\" >> %q\n", caddyLog))

	paths := appPaths.Paths{
		ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"),
		RuntimeDir: filepath.Join(root, "runtime"), LogsDir: filepath.Join(root, "logs"),
		Registry: filepath.Join(root, "state", "registry.json"), Lock: filepath.Join(root, "state", "registry.lock"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	runner := process.ExecRunner{}
	service := &Service{
		Store: state.Store{RegistryPath: paths.Registry, LockPath: paths.Lock}, Paths: paths,
		Caddy:     fakeBinaryCaddy{binary: caddyBinary, runner: runner},
		Tailscale: tailscale.CLI{Binary: tailscaleBinary, Runner: runner},
		Health:    fakeHealth{}, Verifier: HTTPVerifier{Timeout: time.Second, Transport: transport},
		PortStart: port, PortEnd: port, GatewayStart: 28080, PortFree: func(int) bool { return true },
	}
	cfg := config.Defaults()
	cfg.ProjectRoot = root
	cfg.Routes = []model.Route{{Path: "/*", Upstream: "http://127.0.0.1:3000"}}
	_, err = service.Up(context.Background(), UpRequest{Config: cfg, Name: "integration"})
	var safe *SafeHandoffError
	if !errors.As(err, &safe) || safe.Code != "external_forbidden" || safe.StatusCode != http.StatusForbidden {
		t.Fatalf("expected structured host rejection, got %v", err)
	}
	if safe.Hostname != "dev-mini.example.ts.net" || origin == "" {
		t.Fatalf("expected exact public hostname diagnostics, got %#v", safe)
	}
	rawState, err := os.ReadFile(tailscaleState)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(rawState)) != "{}" {
		t.Fatalf("fake Tailscale port was not rolled back: %s", rawState)
	}
	rawCaddy, err := os.ReadFile(caddyLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawCaddy), "apply 1") || !strings.Contains(string(rawCaddy), "apply 0") {
		t.Fatalf("fake Caddy state was not rolled back: %s", rawCaddy)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}
