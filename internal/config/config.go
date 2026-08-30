package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jhumanj/tailpreview/internal/model"
	"gopkg.in/yaml.v3"
)

const FileName = ".tailpreview.yml"

var variablePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type File struct {
	Version int           `yaml:"version"`
	Name    string        `yaml:"name,omitempty"`
	Routes  []model.Route `yaml:"routes"`
	Health  []HealthCheck `yaml:"health,omitempty"`
	TTL     TTL           `yaml:"ttl,omitempty"`
}

type TTL struct {
	Idle   Duration `yaml:"idle,omitempty"`
	MaxAge Duration `yaml:"max_age,omitempty"`
}

type Resolved struct {
	Path        string
	ProjectRoot string
	Name        string
	Routes      []model.Route
	Health      []model.HealthCheck
	IdleTTL     time.Duration
	MaxAge      time.Duration
}

type Values map[string]string

func Defaults() Resolved {
	return Resolved{IdleTTL: 24 * time.Hour, MaxAge: 7 * 24 * time.Hour}
}

func Discover(start string) (configPath, projectRoot string, err error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(abs)
	if err == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	cur := abs
	for {
		candidate := filepath.Join(cur, FileName)
		if stat, statErr := os.Stat(candidate); statErr == nil && !stat.IsDir() {
			return candidate, findGitRoot(cur), nil
		}
		if hasGitMarker(cur) {
			return "", cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", abs, nil
		}
		cur = parent
	}
}

func Load(path, projectRoot string, values Values) (Resolved, error) {
	resolved := Defaults()
	resolved.Path = path
	resolved.ProjectRoot = projectRoot
	if path == "" {
		return resolved, nil
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- reading the explicitly selected local project configuration is the intended behavior.
	if err != nil {
		return resolved, err
	}
	expanded, err := substitute(raw, values)
	if err != nil {
		return resolved, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(expanded))
	decoder.KnownFields(true)
	var cfg File
	if err := decoder.Decode(&cfg); err != nil {
		return resolved, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Version != 1 {
		return resolved, fmt.Errorf("unsupported config version %d; expected 1", cfg.Version)
	}
	resolved.Name = cfg.Name
	resolved.Routes = cfg.Routes
	resolved.Health = make([]model.HealthCheck, 0, len(cfg.Health))
	for _, check := range cfg.Health {
		resolved.Health = append(resolved.Health, check.HealthCheck)
	}
	if cfg.TTL.Idle.Duration > 0 {
		resolved.IdleTTL = cfg.TTL.Idle.Duration
	}
	if cfg.TTL.MaxAge.Duration > 0 {
		resolved.MaxAge = cfg.TTL.MaxAge.Duration
	}
	return resolved, Validate(resolved)
}

func Validate(cfg Resolved) error {
	if cfg.IdleTTL <= 0 || cfg.MaxAge <= 0 {
		return errors.New("idle TTL and max age must be positive")
	}
	if cfg.MaxAge < cfg.IdleTTL {
		return errors.New("max age must be greater than or equal to idle TTL")
	}
	if len(cfg.Routes) == 0 {
		return errors.New("at least one route is required")
	}
	catchAll := false
	for i, route := range cfg.Routes {
		if !strings.HasPrefix(route.Path, "/") {
			return fmt.Errorf("route %d path must start with /", i+1)
		}
		if route.Path == "/*" || route.Path == "/" {
			catchAll = true
			if i != len(cfg.Routes)-1 {
				return errors.New("catch-all route must be the final route")
			}
		}
		if err := ValidateUpstream(route.Upstream, route.InsecureSkipVerify); err != nil {
			return fmt.Errorf("route %d: %w", i+1, err)
		}
	}
	if !catchAll {
		return errors.New("final catch-all route /* is required")
	}
	for i, check := range normalizeHealth(cfg.Health) {
		if err := ValidateUpstream(check.URL, check.InsecureSkipVerify); err != nil {
			return fmt.Errorf("health check %d: %w", i+1, err)
		}
		if check.MinCode < 100 || check.MaxCode > 599 || check.MinCode > check.MaxCode {
			return fmt.Errorf("health check %d has invalid status range", i+1)
		}
	}
	return nil
}

func ValidateUpstream(raw string, insecure bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid upstream URL %q", raw)
	}
	if u.User != nil {
		return errors.New("credentials in upstream URLs are forbidden")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported upstream scheme %q", u.Scheme)
	}
	if insecure && u.Scheme != "https" {
		return errors.New("insecure_skip_verify only applies to HTTPS upstreams")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("upstream host %q is not loopback", host)
	}
	return nil
}

func ParseRoute(value string) (model.Route, error) {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return model.Route{}, fmt.Errorf("route must use PATH=URL")
	}
	route := model.Route{Path: parts[0], Upstream: parts[1]}
	return route, nil
}

func ParseSet(values []string) (Values, error) {
	result := Values{}
	for _, value := range values {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, fmt.Errorf("--set must use NAME=VALUE")
		}
		result[parts[0]] = parts[1]
	}
	return result, nil
}

func normalizeHealth(checks []model.HealthCheck) []model.HealthCheck {
	result := make([]model.HealthCheck, len(checks))
	copy(result, checks)
	for i := range result {
		if result[i].MinCode == 0 {
			result[i].MinCode = 200
		}
		if result[i].MaxCode == 0 {
			result[i].MaxCode = 399
		}
		if !result[i].Required {
			// YAML's zero value should remain convenient: checks are required unless
			// a future schema adds an explicit optional field representation.
			result[i].Required = true
		}
	}
	return result
}

func substitute(raw []byte, explicit Values) ([]byte, error) {
	missing := map[string]struct{}{}
	result := variablePattern.ReplaceAllStringFunc(string(raw), func(match string) string {
		name := variablePattern.FindStringSubmatch(match)[1]
		if value, ok := explicit[name]; ok {
			return value
		}
		if value, ok := os.LookupEnv(name); ok {
			return value
		}
		missing[name] = struct{}{}
		return match
	})
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for name := range missing {
			names = append(names, name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("missing variables: %s", strings.Join(names, ", "))
	}
	return []byte(result), nil
}

func hasGitMarker(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

func findGitRoot(start string) string {
	cur := start
	for {
		if hasGitMarker(cur) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return start
		}
		cur = parent
	}
}
