package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jhumanj/tailpreview/internal/app"
	"github.com/jhumanj/tailpreview/internal/caddy"
	"github.com/jhumanj/tailpreview/internal/model"
	appPaths "github.com/jhumanj/tailpreview/internal/paths"
	"github.com/jhumanj/tailpreview/internal/process"
	"github.com/jhumanj/tailpreview/internal/state"
	"github.com/jhumanj/tailpreview/internal/tailscale"
)

type cliFakeCaddy struct{}

func (cliFakeCaddy) Apply(context.Context, []model.Preview) error { return nil }
func (cliFakeCaddy) Stop(context.Context) error                   { return nil }
func (cliFakeCaddy) ActiveRequests(context.Context) (map[string]int, error) {
	return map[string]int{}, nil
}

var _ caddy.Controller = cliFakeCaddy{}

type cliFakeTailscale struct{}

func (cliFakeTailscale) Status(context.Context) (tailscale.Status, error) {
	var status tailscale.Status
	status.BackendState = "Running"
	status.Self.DNSName = "test.example.ts.net"
	return status, nil
}
func (cliFakeTailscale) PortAvailable(context.Context, int) (bool, error) { return true, nil }
func (cliFakeTailscale) FunnelEnabled(context.Context) (bool, error)      { return false, nil }
func (cliFakeTailscale) Expose(context.Context, int, int) error           { return nil }
func (cliFakeTailscale) Remove(context.Context, int) error                { return nil }

type cliFakeHealth struct{}

func (cliFakeHealth) Wait(context.Context, []model.HealthCheck) error      { return nil }
func (cliFakeHealth) CheckOnce(context.Context, []model.HealthCheck) error { return nil }

type cliFakeVerifier struct{}

func (cliFakeVerifier) Verify(context.Context, string) error { return nil }

func TestUpAcceptsFlagsAfterPositionalAndWritesAgentJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()
	root := t.TempDir()
	paths := appPaths.Paths{
		ConfigDir:  filepath.Join(root, "config"),
		StateDir:   filepath.Join(root, "state"),
		RuntimeDir: filepath.Join(root, "runtime"),
		LogsDir:    filepath.Join(root, "logs"),
		Registry:   filepath.Join(root, "state", "registry.json"),
		Lock:       filepath.Join(root, "state", "registry.lock"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	service := &app.Service{
		Store:        state.Store{RegistryPath: paths.Registry, LockPath: paths.Lock},
		Paths:        paths,
		Caddy:        cliFakeCaddy{},
		Tailscale:    cliFakeTailscale{},
		Health:       cliFakeHealth{},
		Verifier:     cliFakeVerifier{},
		Now:          func() time.Time { return time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC) },
		PortStart:    18443,
		PortEnd:      18452,
		GatewayStart: 28080,
	}
	var out, errOut bytes.Buffer
	command := cli{out: &out, errOut: &errOut, paths: paths, runner: process.ExecRunner{}, service: service, tailscale: cliFakeTailscale{}, caddyBinary: "caddy", tailscaleBinary: "tailscale"}
	err := command.up(context.Background(), []string{upstream.URL, "--project-root", root, "--name", "CLI Test", "--json"})
	if err != nil {
		t.Fatalf("up failed: %v; stderr=%s", err, errOut.String())
	}
	var result app.UpResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if result.SchemaVersion != 1 || result.Preview.Name != "cli-test" || result.Preview.ExternalPort != 18443 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestUpCLIReplacesAdvancedRouteAndHealthConfiguration(t *testing.T) {
	root := t.TempDir()
	paths := appPaths.Paths{
		ConfigDir:  filepath.Join(root, "config"),
		StateDir:   filepath.Join(root, "state"),
		RuntimeDir: filepath.Join(root, "runtime"),
		LogsDir:    filepath.Join(root, "logs"),
		Registry:   filepath.Join(root, "state", "registry.json"),
		Lock:       filepath.Join(root, "state", "registry.lock"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	service := &app.Service{
		Store:        state.Store{RegistryPath: paths.Registry, LockPath: paths.Lock},
		Paths:        paths,
		Caddy:        cliFakeCaddy{},
		Tailscale:    cliFakeTailscale{},
		Health:       cliFakeHealth{},
		Verifier:     cliFakeVerifier{},
		PortStart:    18443,
		PortEnd:      18452,
		GatewayStart: 28080,
	}
	apiURL := "https://127.0.0.1:3443"
	healthURL := apiURL + "/health"
	var out, errOut bytes.Buffer
	command := cli{out: &out, errOut: &errOut, paths: paths, runner: process.ExecRunner{}, service: service, tailscale: cliFakeTailscale{}}
	err := command.up(context.Background(), []string{
		"--project-root", root,
		"--route", "/api/*=" + apiURL,
		"--strip-prefix", "/api/*",
		"--insecure-upstream", "/api/*",
		"--route", "/*=http://127.0.0.1:3000",
		"--optional-health", healthURL,
		"--health-range", healthURL + "=200-499",
		"--insecure-health", healthURL,
		"--json",
	})
	if err != nil {
		t.Fatalf("up failed: %v; stderr=%s", err, errOut.String())
	}
	var result app.UpResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	apiRoute := result.Preview.Routes[0]
	check := result.Preview.Health[0]
	if !apiRoute.StripPrefix || !apiRoute.InsecureSkipVerify {
		t.Fatalf("advanced route flags not applied: %#v", apiRoute)
	}
	if check.Required || !check.InsecureSkipVerify || check.MinCode != 200 || check.MaxCode != 499 {
		t.Fatalf("advanced health flags not applied: %#v", check)
	}
}

func TestParseHealthRangeAllowsEqualsInsideURL(t *testing.T) {
	url, minCode, maxCode, err := parseHealthRange("http://127.0.0.1:3000/health?mode=full=201-429")
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://127.0.0.1:3000/health?mode=full" || minCode != 201 || maxCode != 429 {
		t.Fatalf("unexpected parse: %q %d %d", url, minCode, maxCode)
	}
}

func TestListJSONUsesEmptyArray(t *testing.T) {
	root := t.TempDir()
	paths := appPaths.Paths{
		StateDir: filepath.Join(root, "state"),
		Registry: filepath.Join(root, "state", "registry.json"),
		Lock:     filepath.Join(root, "state", "registry.lock"),
	}
	service := &app.Service{Store: state.Store{RegistryPath: paths.Registry, LockPath: paths.Lock}}
	var out bytes.Buffer
	command := cli{out: &out, errOut: &out, service: service}
	if err := command.list([]string{"--json"}); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Previews []model.Preview `json:"previews"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Previews == nil || len(payload.Previews) != 0 {
		t.Fatalf("expected [], got %s", out.String())
	}
}
