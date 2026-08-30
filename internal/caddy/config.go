package caddy

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/jhumanj/tailpreview/internal/model"
	appPaths "github.com/jhumanj/tailpreview/internal/paths"
)

func Generate(paths appPaths.Paths, previews []model.Preview) ([]byte, error) {
	servers := map[string]interface{}{}
	logs := map[string]interface{}{}
	accessNames := []string{}
	for _, preview := range previews {
		serverName := "preview_" + safeID(preview.ID)
		loggerName := serverName
		routes := make([]interface{}, 0, len(preview.Routes))
		for _, route := range preview.Routes {
			built, err := buildRoute(route)
			if err != nil {
				return nil, err
			}
			routes = append(routes, built)
		}
		servers[serverName] = map[string]interface{}{
			"listen":          []string{fmt.Sprintf("127.0.0.1:%d", preview.GatewayPort)},
			"routes":          routes,
			"automatic_https": map[string]interface{}{"disable": true},
			"metrics":         map[string]interface{}{},
			"logs":            map[string]interface{}{"default_logger_name": loggerName},
		}
		logs[loggerName] = accessLogger(filepath.Join(paths.LogsDir, preview.ID+".jsonl"), loggerName)
		accessNames = append(accessNames, "http.log.access."+loggerName)
	}
	logs["default"] = map[string]interface{}{"level": "ERROR", "exclude": accessNames}
	config := map[string]interface{}{
		"admin": map[string]interface{}{
			"listen": "unix//" + paths.CaddySock,
		},
		"logging": map[string]interface{}{"logs": logs},
		"apps": map[string]interface{}{
			"http": map[string]interface{}{
				"servers": servers,
			},
		},
	}
	return json.MarshalIndent(config, "", "  ")
}

func buildRoute(route model.Route) (map[string]interface{}, error) {
	u, err := url.Parse(route.Upstream)
	if err != nil {
		return nil, err
	}
	paths := []string{route.Path}
	if strings.HasSuffix(route.Path, "/*") {
		exact := strings.TrimSuffix(route.Path, "/*")
		if exact != "" {
			paths = append([]string{exact}, paths...)
		}
	}
	proxy := map[string]interface{}{
		"handler":            "reverse_proxy",
		"upstreams":          []interface{}{map[string]interface{}{"dial": u.Host}},
		"stream_close_delay": "5m",
		"stream_timeout":     "12h",
	}
	if u.Scheme == "https" {
		tlsConfig := map[string]interface{}{}
		if route.InsecureSkipVerify {
			tlsConfig["insecure_skip_verify"] = true
		}
		proxy["transport"] = map[string]interface{}{"protocol": "http", "tls": tlsConfig}
	}
	if route.StripPrefix {
		prefix := strings.TrimSuffix(route.Path, "/*")
		proxy["rewrite"] = map[string]interface{}{"strip_path_prefix": prefix}
	}
	return map[string]interface{}{
		"match":    []interface{}{map[string]interface{}{"path": paths}},
		"handle":   []interface{}{proxy},
		"terminal": true,
	}, nil
}

func accessLogger(filename, name string) map[string]interface{} {
	return map[string]interface{}{
		"include": []string{"http.log.access." + name},
		"writer": map[string]interface{}{
			"output":         "file",
			"filename":       filename,
			"mode":           "0600",
			"roll":           true,
			"roll_size_mb":   10,
			"roll_interval":  int64(24 * 60 * 60 * 1_000_000_000),
			"roll_keep":      3,
			"roll_keep_days": 1,
		},
		"encoder": map[string]interface{}{
			"format": "filter",
			"wrap":   map[string]interface{}{"format": "json"},
			"fields": map[string]interface{}{
				"request>headers":     map[string]interface{}{"filter": "delete"},
				"request>remote_ip":   map[string]interface{}{"filter": "delete"},
				"request>client_ip":   map[string]interface{}{"filter": "delete"},
				"request>remote_port": map[string]interface{}{"filter": "delete"},
				"resp_headers":        map[string]interface{}{"filter": "delete"},
				"request>uri": map[string]interface{}{
					"filter": "regexp",
					"regexp": `\?.*$`,
				},
			},
		},
	}
}

func safeID(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
