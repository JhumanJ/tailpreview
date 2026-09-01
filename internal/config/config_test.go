package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jhumanj/tailpreview/internal/model"
)

func TestLoadStrictConfigAndSubstitution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	content := `version: 1
name: opnform-pr
routes:
  - path: /api/*
    upstream: ${API_URL}
  - path: /*
    upstream: ${FRONTEND_URL}
health:
  - ${API_URL}/health
verify:
  - path: /
    follow_redirects: same_origin
  - path: /api/health
    min_code: 200
    max_code: 299
ttl:
  idle: 2h
  max_age: 48h
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := Load(path, dir, Values{
		"API_URL":      "http://127.0.0.1:3001",
		"FRONTEND_URL": "http://localhost:3000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Name != "opnform-pr" || len(resolved.Routes) != 2 {
		t.Fatalf("unexpected resolved config: %#v", resolved)
	}
	if resolved.IdleTTL != 2*time.Hour || resolved.MaxAge != 48*time.Hour {
		t.Fatalf("unexpected TTL: %s / %s", resolved.IdleTTL, resolved.MaxAge)
	}
	if !resolved.Health[0].Required || resolved.Health[0].MinCode != 200 || resolved.Health[0].MaxCode != 399 {
		t.Fatalf("unexpected health defaults: %#v", resolved.Health[0])
	}
	if len(resolved.Verify) != 2 || resolved.Verify[0].Path != "/" || resolved.Verify[1].MaxCode != 299 {
		t.Fatalf("unexpected verification checks: %#v", resolved.Verify)
	}
}

func TestValidateVerificationPathsRejectsSensitiveOrUnsafeValues(t *testing.T) {
	for _, raw := range []string{"", "relative", "//other.example/path", "/login?token=secret", "/page#fragment"} {
		if err := ValidateVerificationPath(raw); err == nil {
			t.Fatalf("expected verification path %q to fail", raw)
		}
	}
	if err := ValidateVerificationPath("/api/health/ready"); err != nil {
		t.Fatalf("expected safe path to pass: %v", err)
	}
}

func TestLoadRejectsVerificationStatusOutsideSafeHandoffRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	content := `version: 1
routes:
  - path: /*
    upstream: http://127.0.0.1:3000
verify:
  - path: /
    min_code: 200
    max_code: 499
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, dir, nil)
	if err == nil || !strings.Contains(err.Error(), "200 <= MIN <= MAX <= 399") {
		t.Fatalf("expected unsafe verification range to fail, got %v", err)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte("version: 1\nroutes: []\nsurprise: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, dir, nil)
	if err == nil || !strings.Contains(err.Error(), "field surprise not found") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestValidateRejectsNonLoopbackAndMissingCatchAll(t *testing.T) {
	cfg := Defaults()
	cfg.Routes = []model.Route{{Path: "/api/*", Upstream: "http://127.0.0.1:3000"}}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "catch-all") {
		t.Fatalf("expected catch-all error, got %v", err)
	}
	if err := ValidateUpstream("http://192.168.1.20:3000", false); err == nil {
		t.Fatal("expected LAN upstream to be rejected")
	}
	if err := ValidateUpstream("http://user:pass@localhost:3000", false); err == nil {
		t.Fatal("expected credentials to be rejected")
	}
}

func TestDiscoverStopsAtGitRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "apps", "web")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, FileName)
	if err := os.WriteFile(configPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	found, projectRoot, err := Discover(nested)
	if err != nil {
		t.Fatal(err)
	}
	if found != configPath || projectRoot != root {
		t.Fatalf("got %q / %q", found, projectRoot)
	}
}
