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
	"strings"
	"testing"
	"time"

	"github.com/jhumanj/tailpreview/internal/config"
	"github.com/jhumanj/tailpreview/internal/model"
	appPaths "github.com/jhumanj/tailpreview/internal/paths"
	"github.com/jhumanj/tailpreview/internal/state"
	"github.com/jhumanj/tailpreview/internal/tailscale"
)

func TestHTTPVerifierAcceptsSuccessAndSameOriginRedirect(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ready", http.StatusPermanentRedirect)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	origin, transport := publicTLSTestOrigin(t, server, "preview.example.ts.net")

	report, err := (HTTPVerifier{Timeout: time.Second, Transport: transport}).Verify(
		context.Background(), origin, []model.VerificationCheck{{Path: "/", MinCode: 200, MaxCode: 299}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Checks) != 1 || report.Checks[0].StatusCode != http.StatusNoContent || report.Checks[0].FinalOrigin != origin {
		t.Fatalf("unexpected verification report: %#v", report)
	}
}

func TestHTTPVerifierRejectsForbiddenImmediately(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	origin, transport := publicTLSTestOrigin(t, server, "preview.example.ts.net")

	started := time.Now()
	_, err := (HTTPVerifier{Timeout: time.Second, Transport: transport}).Verify(
		context.Background(), origin, []model.VerificationCheck{{Path: "/", MinCode: 200, MaxCode: 399}},
	)
	var safe *SafeHandoffError
	if !errors.As(err, &safe) || safe.Code != "external_forbidden" || safe.StatusCode != http.StatusForbidden {
		t.Fatalf("expected structured 403 rejection, got %v", err)
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("403 should fail immediately, took %s", time.Since(started))
	}
}

func TestHTTPVerifierRejectsLoopbackRedirectWithoutLeakingQuery(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost:3867/login?token=must-not-leak", http.StatusPermanentRedirect)
	}))
	defer server.Close()
	origin, transport := publicTLSTestOrigin(t, server, "preview.example.ts.net")

	_, err := (HTTPVerifier{Timeout: time.Second, Transport: transport}).Verify(
		context.Background(), origin, []model.VerificationCheck{{Path: "/", MinCode: 200, MaxCode: 399}},
	)
	var safe *SafeHandoffError
	if !errors.As(err, &safe) || safe.Code != "loopback_redirect" || safe.RedirectOrigin != "http://localhost:3867" {
		t.Fatalf("expected sanitized loopback redirect, got %#v / %v", safe, err)
	}
	payload := ErrorPayloadFor(err)
	if strings.Contains(payload.Error+payload.RedirectOrigin+payload.Remediation, "must-not-leak") {
		t.Fatalf("structured error leaked redirect query: %#v", payload)
	}
}

func TestHTTPVerifierRejectsCredentialedOriginsAndRedirects(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := strings.Replace(serverURLWithoutCredentials(r), "https://", "https://user:secret@", 1) + "/ready"
		http.Redirect(w, r, target, http.StatusFound)
	}))
	defer server.Close()
	origin, transport := publicTLSTestOrigin(t, server, "preview.example.ts.net")

	verifier := HTTPVerifier{Timeout: time.Second, Transport: transport}
	_, err := verifier.Verify(context.Background(), origin, []model.VerificationCheck{{Path: "/", MinCode: 200, MaxCode: 399}})
	var safe *SafeHandoffError
	if !errors.As(err, &safe) || safe.Code != "credentialed_redirect" || strings.Contains(safe.RedirectOrigin, "secret") {
		t.Fatalf("expected sanitized credentialed redirect rejection, got %#v / %v", safe, err)
	}

	credentialed := strings.Replace(origin, "https://", "https://user:secret@", 1)
	_, err = verifier.Verify(context.Background(), credentialed, []model.VerificationCheck{{Path: "/", MinCode: 200, MaxCode: 399}})
	if !errors.As(err, &safe) || safe.Code != "invalid_handoff_url" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("expected credentialed base origin rejection without leakage, got %#v / %v", safe, err)
	}
}

func TestHTTPVerifierRejectsLoopbackHandoffOrigin(t *testing.T) {
	_, err := (HTTPVerifier{}).Verify(context.Background(), "https://localhost:8443", nil)
	var safe *SafeHandoffError
	if !errors.As(err, &safe) || safe.Code != "invalid_handoff_url" {
		t.Fatalf("expected loopback handoff URL rejection, got %v", err)
	}
}

func TestHTTPVerifierRejectsTamperedVerificationPathWithoutLeakingIt(t *testing.T) {
	_, err := (HTTPVerifier{}).Verify(
		context.Background(), "https://preview.example.ts.net:8443",
		[]model.VerificationCheck{{Path: "/login?token=must-not-leak", MinCode: 200, MaxCode: 399}},
	)
	var safe *SafeHandoffError
	if !errors.As(err, &safe) || safe.Code != "invalid_verification_check" || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("expected sanitized invalid verification error, got %#v / %v", safe, err)
	}
}

func publicTLSTestOrigin(t *testing.T, server *httptest.Server, hostname string) (string, http.RoundTripper) {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.InsecureSkipVerify = true // #nosec G402 -- isolated TLS test server with a synthetic public hostname.
	return "https://" + net.JoinHostPort(hostname, port), transport
}

func serverURLWithoutCredentials(r *http.Request) string {
	return "https://" + r.Host
}

func TestErrorPayloadForPreservesWrappedSafeHandoffDetails(t *testing.T) {
	err := fmt.Errorf("outer operation: %w", &SafeHandoffError{
		Code: "loopback_redirect", Phase: "final_verification", Message: "unsafe redirect",
		RedirectOrigin: "http://localhost:3000", TargetPath: "/",
	})
	payload := ErrorPayloadFor(err)
	if payload.Code != "loopback_redirect" || payload.Phase != "final_verification" || payload.RedirectOrigin != "http://localhost:3000" {
		t.Fatalf("wrapped structured details were lost: %#v", payload)
	}
}

type fakeCaddy struct {
	applied       [][]model.Preview
	active        map[string]int
	failNextApply error
	onApply       func()
}

func (f *fakeCaddy) Apply(_ context.Context, previews []model.Preview) error {
	if f.failNextApply != nil {
		err := f.failNextApply
		f.failNextApply = nil
		return err
	}
	copy := clonePreviews(previews)
	f.applied = append(f.applied, copy)
	if f.onApply != nil {
		callback := f.onApply
		f.onApply = nil
		callback()
	}
	return nil
}
func (f *fakeCaddy) Stop(context.Context) error { return nil }
func (f *fakeCaddy) ActiveRequests(context.Context) (map[string]int, error) {
	return f.active, nil
}

type fakeTailscale struct {
	exposed     map[int]int
	busy        map[int]bool
	funnelPorts map[int]bool
	failExpose  error
}

func (f *fakeTailscale) Status(context.Context) (tailscale.Status, error) {
	var status tailscale.Status
	status.BackendState = "Running"
	status.Self.DNSName = "dev-mini.example.ts.net"
	status.Self.Online = true
	return status, nil
}
func (f *fakeTailscale) PortAvailable(_ context.Context, port int) (bool, error) {
	return !f.busy[port] && f.exposed[port] == 0, nil
}
func (f *fakeTailscale) FunnelEnabled(context.Context) (bool, error) { return false, nil }
func (f *fakeTailscale) FunnelEnabledOnPort(_ context.Context, port int) (bool, error) {
	return f.funnelPorts[port], nil
}
func (f *fakeTailscale) Expose(_ context.Context, external, gateway int) error {
	if f.failExpose != nil {
		err := f.failExpose
		f.failExpose = nil
		return err
	}
	f.exposed[external] = gateway
	return nil
}
func (f *fakeTailscale) Remove(_ context.Context, external int) error {
	delete(f.exposed, external)
	return nil
}

type fakeHealth struct{ err error }

func (f fakeHealth) Wait(context.Context, []model.HealthCheck) error      { return f.err }
func (f fakeHealth) CheckOnce(context.Context, []model.HealthCheck) error { return f.err }

type fakeVerifier struct {
	err    error
	report model.VerificationReport
}

func (f fakeVerifier) Verify(context.Context, string, []model.VerificationCheck) (model.VerificationReport, error) {
	return f.report, f.err
}

func newTestService(t *testing.T, now *time.Time) (*Service, *fakeCaddy, *fakeTailscale) {
	t.Helper()
	dir := t.TempDir()
	paths := appPaths.Paths{
		ConfigDir:  filepath.Join(dir, "config"),
		StateDir:   dir,
		RuntimeDir: filepath.Join(dir, "runtime"),
		LogsDir:    filepath.Join(dir, "logs"),
		Registry:   filepath.Join(dir, "registry.json"),
		Lock:       filepath.Join(dir, "registry.lock"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	fc := &fakeCaddy{active: map[string]int{}}
	ft := &fakeTailscale{exposed: map[int]int{}, busy: map[int]bool{}, funnelPorts: map[int]bool{}}
	service := &Service{
		Store:     state.Store{RegistryPath: paths.Registry, LockPath: paths.Lock},
		Paths:     paths,
		Caddy:     fc,
		Tailscale: ft,
		Health:    fakeHealth{},
		Verifier:  fakeVerifier{},
		Now:       func() time.Time { return *now },
		PortFree:  func(int) bool { return true },
	}
	return service, fc, ft
}

func testConfig(root string) config.Resolved {
	cfg := config.Defaults()
	cfg.ProjectRoot = root
	cfg.Routes = []model.Route{{Path: "/*", Upstream: "http://127.0.0.1:3000"}}
	return cfg
}

func TestUpAllocatesStablePortAndDownPreservesReservation(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	service, _, ts := newTestService(t, &now)
	root := filepath.Join(t.TempDir(), "OpnForm")
	first, err := service.Up(context.Background(), UpRequest{Config: testConfig(root), Name: "opnform-pr-1"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Preview.ExternalPort != 8443 || ts.exposed[8443] != 18080 {
		t.Fatalf("unexpected allocation: %#v / %#v", first.Preview, ts.exposed)
	}
	if first.Preview.HandoffURL != first.Preview.URL || len(first.Preview.Verify) != 1 {
		t.Fatalf("safe handoff fields missing: %#v", first.Preview)
	}
	if _, err := service.Down(context.Background(), first.Preview.Name); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	second, err := service.Up(context.Background(), UpRequest{Config: testConfig(root), Name: "opnform-pr-1"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Preview.ExternalPort != 8443 {
		t.Fatalf("expected stable port, got %d", second.Preview.ExternalPort)
	}
}

func TestCheckRevalidatesRegisteredPreviewAndPersistsTimestamp(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	service, _, _ := newTestService(t, &now)
	result, err := service.Up(context.Background(), UpRequest{Config: testConfig(t.TempDir()), Name: "check-me"})
	if err != nil {
		t.Fatal(err)
	}
	verifiedAt := now.Add(time.Minute)
	service.Verifier = fakeVerifier{report: model.VerificationReport{
		VerifiedAt: verifiedAt,
		Checks:     []model.VerificationResult{{Path: "/", StatusCode: 200, FinalOrigin: result.Preview.HandoffURL}},
	}}
	checked, err := service.Check(context.Background(), result.Preview.Name)
	if err != nil {
		t.Fatal(err)
	}
	if checked.Action != "check" || checked.Preview.LastVerifiedAt == nil || !checked.Preview.LastVerifiedAt.Equal(verifiedAt) {
		t.Fatalf("unexpected check result: %#v", checked)
	}
	previews, err := service.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(previews) != 1 || previews[0].LastVerifiedAt == nil || !previews[0].LastVerifiedAt.Equal(verifiedAt) {
		t.Fatalf("verification timestamp was not persisted: %#v", previews)
	}
}

func TestCheckDetectsMissingServeListener(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	service, _, ts := newTestService(t, &now)
	result, err := service.Up(context.Background(), UpRequest{Config: testConfig(t.TempDir()), Name: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	delete(ts.exposed, result.Preview.ExternalPort)
	_, err = service.Check(context.Background(), result.Preview.Name)
	var safe *SafeHandoffError
	if !errors.As(err, &safe) || safe.Code != "serve_listener_missing" {
		t.Fatalf("expected missing listener error, got %v", err)
	}
}

func TestCheckRejectsFunnelOnlyOnTheRegisteredPort(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	service, _, ts := newTestService(t, &now)
	result, err := service.Up(context.Background(), UpRequest{Config: testConfig(t.TempDir()), Name: "private-only"})
	if err != nil {
		t.Fatal(err)
	}
	ts.funnelPorts[result.Preview.ExternalPort+1] = true
	if _, err := service.Check(context.Background(), result.Preview.Name); err != nil {
		t.Fatalf("unrelated Funnel port must not fail this preview: %v", err)
	}
	ts.funnelPorts[result.Preview.ExternalPort] = true
	_, err = service.Check(context.Background(), result.Preview.Name)
	var safe *SafeHandoffError
	if !errors.As(err, &safe) || safe.Code != "public_endpoint_detected" {
		t.Fatalf("expected exact-port Funnel rejection, got %v", err)
	}
}

func TestUpExistingPreviewRefreshesLastUse(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	service, _, _ := newTestService(t, &now)
	root := filepath.Join(t.TempDir(), "worktree")
	first, err := service.Up(context.Background(), UpRequest{Config: testConfig(root), Name: "preview"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Hour)
	second, err := service.Up(context.Background(), UpRequest{Config: testConfig(root), Name: "preview"})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Preview.LastUsedAt.Equal(now) || second.Preview.CreatedAt != first.Preview.CreatedAt {
		t.Fatalf("unexpected timestamps after refresh: first=%#v second=%#v", first.Preview, second.Preview)
	}
}

func TestEleventhPreviewEvictsLeastRecentlyUsed(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	service, _, _ := newTestService(t, &now)
	base := t.TempDir()
	var oldest model.Preview
	for i := 0; i < 10; i++ {
		result, err := service.Up(context.Background(), UpRequest{Config: testConfig(filepath.Join(base, fmt.Sprintf("worktree-%02d", i))), Name: fmt.Sprintf("preview-%02d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			oldest = result.Preview
		}
		now = now.Add(time.Minute)
	}
	result, err := service.Up(context.Background(), UpRequest{Config: testConfig(filepath.Join(base, "worktree-10")), Name: "preview-10"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Evicted == nil || result.Evicted.ID != oldest.ID {
		t.Fatalf("expected oldest eviction, got %#v", result.Evicted)
	}
	if result.Preview.ExternalPort != oldest.ExternalPort {
		t.Fatalf("expected evicted port reuse, got %d", result.Preview.ExternalPort)
	}
}

func TestUpRollsBackCaddyWhenTailscaleFails(t *testing.T) {
	now := time.Now().UTC()
	service, caddy, ts := newTestService(t, &now)
	ts.failExpose = errors.New("serve failed")
	_, err := service.Up(context.Background(), UpRequest{Config: testConfig(t.TempDir()), Name: "broken"})
	if err == nil {
		t.Fatal("expected failure")
	}
	if len(caddy.applied) != 2 || len(caddy.applied[1]) != 0 {
		t.Fatalf("expected Caddy rollback, got %#v", caddy.applied)
	}
	previews, listErr := service.List()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(previews) != 0 {
		t.Fatalf("failed preview persisted: %#v", previews)
	}
}

func TestUpHealthFailureHasNoSideEffects(t *testing.T) {
	now := time.Now().UTC()
	service, caddy, ts := newTestService(t, &now)
	service.Health = fakeHealth{err: errors.New("backend unavailable")}
	_, err := service.Up(context.Background(), UpRequest{Config: testConfig(t.TempDir()), Name: "broken"})
	if err == nil {
		t.Fatal("expected health failure")
	}
	if len(caddy.applied) != 0 || len(ts.exposed) != 0 {
		t.Fatalf("health failure changed exposure state: caddy=%#v tailscale=%#v", caddy.applied, ts.exposed)
	}
}

func TestUpRollsBackAfterFinalURLVerificationFailure(t *testing.T) {
	now := time.Now().UTC()
	service, caddy, ts := newTestService(t, &now)
	service.Verifier = fakeVerifier{err: errors.New("tailnet URL unreachable")}
	_, err := service.Up(context.Background(), UpRequest{Config: testConfig(t.TempDir()), Name: "broken"})
	if err == nil {
		t.Fatal("expected verification failure")
	}
	if len(caddy.applied) != 2 || len(caddy.applied[1]) != 0 || len(ts.exposed) != 0 {
		t.Fatalf("verification rollback incomplete: caddy=%#v tailscale=%#v", caddy.applied, ts.exposed)
	}
}

func TestUpRestoresEvictedPreviewIfRegistrySaveFails(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	service, caddy, ts := newTestService(t, &now)
	base := t.TempDir()
	var oldest model.Preview
	for i := 0; i < 10; i++ {
		result, err := service.Up(context.Background(), UpRequest{Config: testConfig(filepath.Join(base, fmt.Sprintf("worktree-%02d", i))), Name: fmt.Sprintf("preview-%02d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			oldest = result.Preview
		}
		now = now.Add(time.Minute)
	}
	caddy.onApply = func() {
		blockStateDirectory(service.Paths.StateDir)
	}
	_, err := service.Up(context.Background(), UpRequest{Config: testConfig(filepath.Join(base, "worktree-10")), Name: "preview-10"})
	if err == nil {
		t.Fatal("expected registry save failure")
	}
	if ts.exposed[oldest.ExternalPort] != oldest.GatewayPort {
		t.Fatalf("evicted preview was not restored: %#v", ts.exposed)
	}
	if len(caddy.applied) < 2 || len(caddy.applied[len(caddy.applied)-1]) != 10 {
		t.Fatalf("Caddy state was not restored: %#v", caddy.applied)
	}
}

func TestEleventhPreviewFailsWhenEveryPreviewPinned(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	service, _, ts := newTestService(t, &now)
	base := t.TempDir()
	for i := 0; i < 10; i++ {
		result, err := service.Up(context.Background(), UpRequest{Config: testConfig(filepath.Join(base, fmt.Sprintf("worktree-%02d", i))), Name: fmt.Sprintf("preview-%02d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.SetPinned(result.Preview.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	before := len(ts.exposed)
	_, err := service.Up(context.Background(), UpRequest{Config: testConfig(filepath.Join(base, "worktree-10")), Name: "preview-10"})
	if err == nil || !strings.Contains(err.Error(), "pinned or active") {
		t.Fatalf("expected safe capacity failure, got %v", err)
	}
	if len(ts.exposed) != before {
		t.Fatal("capacity failure changed Tailscale state")
	}
}

func TestDownRemovesPreviewLogs(t *testing.T) {
	now := time.Now().UTC()
	service, _, _ := newTestService(t, &now)
	result, err := service.Up(context.Background(), UpRequest{Config: testConfig(t.TempDir()), Name: "logged"})
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(service.Paths.LogsDir, result.Preview.ID+".jsonl")
	rolledPath := logPath + ".2026-08-30"
	if err := os.WriteFile(logPath, []byte("request\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolledPath, []byte("request\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Down(context.Background(), result.Preview.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{logPath, rolledPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s removed, got %v", path, err)
		}
	}
}

func TestDownRestoresExposureIfRegistrySaveFails(t *testing.T) {
	now := time.Now().UTC()
	service, caddy, ts := newTestService(t, &now)
	result, err := service.Up(context.Background(), UpRequest{Config: testConfig(t.TempDir()), Name: "restore-me"})
	if err != nil {
		t.Fatal(err)
	}
	caddy.onApply = func() { blockStateDirectory(service.Paths.StateDir) }
	if _, err := service.Down(context.Background(), result.Preview.ID); err == nil {
		t.Fatal("expected registry save failure")
	}
	if ts.exposed[result.Preview.ExternalPort] != result.Preview.GatewayPort {
		t.Fatalf("Tailscale exposure was not restored: %#v", ts.exposed)
	}
	if len(caddy.applied[len(caddy.applied)-1]) != 1 {
		t.Fatalf("Caddy exposure was not restored: %#v", caddy.applied)
	}
}

func TestGCRestoresExposureIfRegistrySaveFails(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	service, caddy, ts := newTestService(t, &now)
	result, err := service.Up(context.Background(), UpRequest{Config: testConfig(t.TempDir()), Name: "restore-me"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(25 * time.Hour)
	caddy.onApply = func() { blockStateDirectory(service.Paths.StateDir) }
	if _, err := service.GC(context.Background()); err == nil {
		t.Fatal("expected registry save failure")
	}
	if ts.exposed[result.Preview.ExternalPort] != result.Preview.GatewayPort {
		t.Fatalf("Tailscale exposure was not restored: %#v", ts.exposed)
	}
	if len(caddy.applied[len(caddy.applied)-1]) != 1 {
		t.Fatalf("Caddy exposure was not restored: %#v", caddy.applied)
	}
}

func TestGCRespectsActiveRequestsThenExpires(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	service, caddy, _ := newTestService(t, &now)
	result, err := service.Up(context.Background(), UpRequest{Config: testConfig(t.TempDir()), Name: "active"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(25 * time.Hour)
	caddy.active["127.0.0.1:3000"] = 1
	gc, err := service.GC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gc.Removed) != 0 {
		t.Fatal("active request should prevent expiration")
	}
	caddy.active["127.0.0.1:3000"] = 0
	gc, err = service.GC(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gc.Removed) != 1 || gc.Removed[0].ID != result.Preview.ID {
		t.Fatalf("expected expiration, got %#v", gc.Removed)
	}
}

func blockStateDirectory(path string) {
	_ = os.RemoveAll(path)
	_ = os.WriteFile(path, []byte("block registry directory recreation"), 0o600)
}
