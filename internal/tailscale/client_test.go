package tailscale

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jhumanj/tailpreview/internal/process"
)

type recordedCall struct {
	name string
	args []string
}

type recordingRunner struct {
	calls   []recordedCall
	results []process.Result
	errors  []error
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) (process.Result, error) {
	r.calls = append(r.calls, recordedCall{name: name, args: append([]string(nil), args...)})
	index := len(r.calls) - 1
	var result process.Result
	var err error
	if index < len(r.results) {
		result = r.results[index]
	}
	if index < len(r.errors) {
		err = r.errors[index]
	}
	return result, err
}

func (r *recordingRunner) LookPath(name string) (string, error) { return name, nil }

func TestContainsPortIsConservative(t *testing.T) {
	payload := map[string]interface{}{
		"Web": map[string]interface{}{
			"https://machine.example.ts.net:8443": map[string]interface{}{"Handlers": map[string]interface{}{}},
		},
	}
	if !containsPort(payload, 8443) {
		t.Fatal("expected configured port to be found")
	}
	if containsPort(payload, 8444) {
		t.Fatal("unexpected different port match")
	}
}

func TestContainsEnabledFunnel(t *testing.T) {
	if !containsEnabledFunnel(map[string]interface{}{"AllowFunnel": map[string]interface{}{"example:443": true}}) {
		t.Fatal("expected enabled Funnel to be detected")
	}
	if containsEnabledFunnel(map[string]interface{}{"AllowFunnel": map[string]interface{}{}}) {
		t.Fatal("empty Funnel config should not be enabled")
	}
}

func TestContainsEnabledFunnelPortIsExact(t *testing.T) {
	payload := map[string]interface{}{
		"AllowFunnel": map[string]interface{}{
			"machine.example.ts.net:8443": true,
			"machine.example.ts.net:8444": false,
		},
	}
	if !containsEnabledFunnelPort(payload, 8443, false) {
		t.Fatal("expected Funnel on port 8443")
	}
	if containsEnabledFunnelPort(payload, 8444, false) || containsEnabledFunnelPort(payload, 8445, false) {
		t.Fatal("unrelated or disabled ports must not be reported as Funnel")
	}
}

func TestExposeUsesOnlyPrivateServeCommand(t *testing.T) {
	runner := &recordingRunner{results: []process.Result{{Stdout: "Available within your tailnet"}}}
	client := CLI{Binary: "/opt/tailscale", Runner: runner}
	if err := client.Expose(context.Background(), 8443, 18080); err != nil {
		t.Fatal(err)
	}
	want := recordedCall{name: "/opt/tailscale", args: []string{"serve", "--yes", "--bg", "--https=8443", "http://127.0.0.1:18080"}}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("unexpected command: %#v", runner.calls)
	}
	assertNoPublicOrResetCommand(t, runner.calls)
}

func TestRemoveDisablesOnlyTheOwnedHTTPSPort(t *testing.T) {
	runner := &recordingRunner{}
	client := CLI{Runner: runner}
	if err := client.Remove(context.Background(), 8449); err != nil {
		t.Fatal(err)
	}
	want := recordedCall{name: "tailscale", args: []string{"serve", "--yes", "--https=8449", "off"}}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("unexpected command: %#v", runner.calls)
	}
	assertNoPublicOrResetCommand(t, runner.calls)
}

func TestExposeRollsBackIfTailscaleReportsPublicEndpoint(t *testing.T) {
	runner := &recordingRunner{results: []process.Result{{Stdout: "Available on the internet"}, {}}}
	client := CLI{Runner: runner}
	if err := client.Expose(context.Background(), 8443, 18080); err == nil {
		t.Fatal("expected public endpoint safeguard to fail")
	}
	if len(runner.calls) != 2 || runner.calls[1].args[len(runner.calls[1].args)-1] != "off" {
		t.Fatalf("expected exact-port rollback, got %#v", runner.calls)
	}
	assertNoPublicOrResetCommand(t, runner.calls)
}

func TestExposeReturnsSanitizedConsentError(t *testing.T) {
	runner := &recordingRunner{
		results: []process.Result{{Stdout: "Serve is not enabled on your tailnet.\nTo enable, visit:\nhttps://login.tailscale.com/f/serve?node=secret"}},
		errors:  []error{context.DeadlineExceeded},
	}
	client := CLI{Runner: runner}
	err := client.Expose(context.Background(), 8443, 18080)
	if err == nil || !strings.Contains(err.Error(), "one-time tailnet consent") {
		t.Fatalf("expected consent guidance, got %v", err)
	}
	if strings.Contains(err.Error(), "login.tailscale.com") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("consent URL leaked into error: %v", err)
	}
}

func assertNoPublicOrResetCommand(t *testing.T, calls []recordedCall) {
	t.Helper()
	for _, call := range calls {
		joined := strings.ToLower(strings.Join(call.args, " "))
		if strings.Contains(joined, "funnel") || strings.Contains(joined, "serve reset") {
			t.Fatalf("unsafe Tailscale command emitted: %s %s", call.name, joined)
		}
	}
}
