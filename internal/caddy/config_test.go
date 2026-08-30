package caddy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jhumanj/tailpreview/internal/model"
	appPaths "github.com/jhumanj/tailpreview/internal/paths"
)

func TestGeneratePreservesRouteOrderAndPrivacyFilters(t *testing.T) {
	paths := appPaths.Paths{CaddySock: "/tmp/tailpreview.sock", LogsDir: "/tmp/logs"}
	preview := model.Preview{
		ID:          "abc-123",
		GatewayPort: 18080,
		Routes: []model.Route{
			{Path: "/api/*", Upstream: "http://127.0.0.1:3001", StripPrefix: true},
			{Path: "/*", Upstream: "http://127.0.0.1:3000"},
		},
		IdleTTL: model.Duration(24 * time.Hour),
	}
	raw, err := Generate(paths, []model.Preview{preview})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, expected := range []string{
		`"listen": "unix///tmp/tailpreview.sock"`,
		`"127.0.0.1:18080"`,
		`"strip_path_prefix": "/api"`,
		`"request\u003eheaders"`,
		`"filter": "delete"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("generated config missing %s:\n%s", expected, text)
		}
	}
	apiPos := strings.Index(text, `"/api"`)
	catchAllPos := strings.Index(text, `"/*"`)
	if apiPos < 0 || catchAllPos < 0 || apiPos > catchAllPos {
		t.Fatalf("route order not preserved:\n%s", text)
	}
}
