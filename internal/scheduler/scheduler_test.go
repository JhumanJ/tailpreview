package scheduler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderLaunchAgentEscapesBinaryAndRunsNonInteractively(t *testing.T) {
	content := renderLaunchAgent(`/Applications/Tail & Preview/tailpreview`)
	for _, wanted := range []string{
		`/Applications/Tail &amp; Preview/tailpreview`,
		`<string>gc</string>`,
		`<string>--json</string>`,
		`<string>--non-interactive</string>`,
		`<integer>300</integer>`,
	} {
		if !strings.Contains(content, wanted) {
			t.Fatalf("launchd unit missing %q", wanted)
		}
	}
}

func TestRenderSystemdEscapesSpacesAndUsesUserTimer(t *testing.T) {
	service, timer := renderSystemd(`/opt/Tail Preview/tailpreview`)
	if !strings.Contains(service, `ExecStart=/opt/Tail\x20Preview/tailpreview gc --json --non-interactive`) {
		t.Fatalf("unexpected systemd service:\n%s", service)
	}
	for _, wanted := range []string{"OnUnitActiveSec=5min", "Persistent=true", "WantedBy=timers.target"} {
		if !strings.Contains(timer, wanted) {
			t.Fatalf("systemd timer missing %q", wanted)
		}
	}
}

func TestAtomicWriteUsesRequestedPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unit")
	if err := atomicWrite(path, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}
