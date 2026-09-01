package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jhumanj/tailpreview/internal/app"
	"github.com/jhumanj/tailpreview/internal/buildinfo"
	"github.com/jhumanj/tailpreview/internal/caddy"
	"github.com/jhumanj/tailpreview/internal/config"
	"github.com/jhumanj/tailpreview/internal/doctor"
	"github.com/jhumanj/tailpreview/internal/health"
	"github.com/jhumanj/tailpreview/internal/model"
	appPaths "github.com/jhumanj/tailpreview/internal/paths"
	"github.com/jhumanj/tailpreview/internal/process"
	"github.com/jhumanj/tailpreview/internal/scheduler"
	"github.com/jhumanj/tailpreview/internal/state"
	"github.com/jhumanj/tailpreview/internal/tailscale"
	flag "github.com/spf13/pflag"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

type cli struct {
	out             io.Writer
	errOut          io.Writer
	paths           appPaths.Paths
	runner          process.Runner
	service         *app.Service
	tailscale       tailscale.Client
	caddyBinary     string
	tailscaleBinary string
}

func run(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		printHelp(out)
		return 2
	}
	p, err := appPaths.Resolve()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := p.Ensure(); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	runner := process.ExecRunner{}
	caddyBinary := envDefault("TAILPREVIEW_CADDY_BIN", "caddy")
	tailscaleBinary := envDefault("TAILPREVIEW_TAILSCALE_BIN", "tailscale")
	ts := tailscale.CLI{Binary: tailscaleBinary, Runner: runner}
	caddyManager := caddy.Manager{Binary: caddyBinary, Paths: p, Runner: runner}
	service := &app.Service{
		Store:     state.Store{RegistryPath: p.Registry, LockPath: p.Lock},
		Paths:     p,
		Caddy:     caddyManager,
		Tailscale: ts,
		Health:    health.Checker{Timeout: 30 * time.Second},
		Verifier:  app.HTTPVerifier{Timeout: 10 * time.Second},
	}
	command := &cli{out: out, errOut: errOut, paths: p, runner: runner, service: service, tailscale: ts, caddyBinary: caddyBinary, tailscaleBinary: tailscaleBinary}
	if err := command.execute(ctx, args); err != nil {
		jsonMode := containsArg(args, "--json")
		if jsonMode {
			_ = json.NewEncoder(errOut).Encode(app.ErrorPayloadFor(err))
		} else {
			fmt.Fprintln(errOut, "error:", err)
		}
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	return 0
}

func (c *cli) execute(ctx context.Context, args []string) error {
	switch args[0] {
	case "up":
		return c.up(ctx, args[1:])
	case "down":
		return c.down(ctx, args[1:])
	case "list", "ls":
		return c.list(args[1:])
	case "status":
		return c.status(args[1:])
	case "check":
		return c.check(ctx, args[1:])
	case "pin":
		return c.pin(args[1:], true)
	case "unpin":
		return c.pin(args[1:], false)
	case "gc":
		return c.gc(ctx, args[1:])
	case "doctor":
		return c.doctor(ctx, args[1:])
	case "paths":
		fs, jsonMode := basicFlagSet("paths", c.errOut)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return c.writeJSONOrHuman(c.paths, *jsonMode, func() { fmt.Fprintf(c.out, "config: %s\nstate: %s\n", c.paths.ConfigDir, c.paths.StateDir) })
	case "logs":
		return c.logs(args[1:])
	case "service":
		return c.scheduler(ctx, args[1:])
	case "version":
		fmt.Fprintf(c.out, "tailpreview %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return nil
	case "help", "--help", "-h":
		printHelp(c.out)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (c *cli) up(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(c.errOut)
	var routes, sets, healthChecks, optionalHealthChecks, healthRanges, verificationChecks stringList
	var stripPrefixes, insecureRoutes, insecureHealthChecks stringList
	name := fs.String("name", "", "preview name")
	configPath := fs.String("config", "", "explicit config file")
	projectRoot := fs.String("project-root", "", "project/worktree root")
	port := fs.Int("port", 0, "preferred HTTPS port")
	ttl := fs.String("ttl", "", "idle TTL")
	maxAge := fs.String("max-age", "", "absolute maximum age")
	skipHealth := fs.Bool("skip-health", false, "skip pre-exposure health checks")
	jsonMode := fs.Bool("json", false, "JSON output")
	_ = fs.Bool("non-interactive", false, "never prompt")
	fs.Var(&routes, "route", "route PATH=URL (repeatable)")
	fs.Var(&sets, "set", "config variable NAME=VALUE (repeatable)")
	fs.Var(&healthChecks, "health", "health-check URL (repeatable)")
	fs.Var(&optionalHealthChecks, "optional-health", "non-blocking health-check URL (repeatable)")
	fs.Var(&healthRanges, "health-range", "health status range URL=MIN-MAX (repeatable)")
	fs.Var(&stripPrefixes, "strip-prefix", "route path whose prefix should be stripped")
	fs.Var(&insecureRoutes, "insecure-upstream", "route path allowed to use an unverified loopback TLS certificate")
	fs.Var(&insecureHealthChecks, "insecure-health", "health URL allowed to use an unverified loopback TLS certificate")
	fs.Var(&verificationChecks, "verify", "public path or PATH=MIN-MAX to verify through the final HTTPS origin (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	values, err := config.ParseSet(sets)
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	discoveredPath, discoveredRoot, err := config.Discover(cwd)
	if err != nil {
		return err
	}
	if *configPath != "" {
		discoveredPath, err = filepath.Abs(*configPath)
		if err != nil {
			return err
		}
		discoveredRoot = filepath.Dir(discoveredPath)
	}
	if *projectRoot != "" {
		discoveredRoot, err = filepath.Abs(*projectRoot)
		if err != nil {
			return err
		}
	}
	resolved, err := config.Load(discoveredPath, discoveredRoot, values)
	if err != nil && !(discoveredPath == "" && strings.Contains(err.Error(), "at least one route")) {
		return err
	}
	if len(routes) > 0 {
		resolved.Routes = nil
		for _, raw := range routes {
			route, parseErr := config.ParseRoute(raw)
			if parseErr != nil {
				return parseErr
			}
			resolved.Routes = append(resolved.Routes, route)
		}
	}
	if fs.NArg() > 1 {
		return errors.New("up accepts at most one positional upstream URL")
	}
	if fs.NArg() == 1 {
		if len(routes) > 0 {
			return errors.New("positional upstream and --route cannot be combined")
		}
		resolved.Routes = []model.Route{{Path: "/*", Upstream: fs.Arg(0)}}
	}
	if err := applyRouteBooleans(resolved.Routes, stripPrefixes, func(route *model.Route) { route.StripPrefix = true }); err != nil {
		return err
	}
	if err := applyRouteBooleans(resolved.Routes, insecureRoutes, func(route *model.Route) { route.InsecureSkipVerify = true }); err != nil {
		return err
	}
	if len(healthChecks) > 0 || len(optionalHealthChecks) > 0 {
		resolved.Health = nil
		for _, raw := range healthChecks {
			resolved.Health = append(resolved.Health, model.HealthCheck{URL: raw, Required: true, MinCode: 200, MaxCode: 399})
		}
		for _, raw := range optionalHealthChecks {
			resolved.Health = append(resolved.Health, model.HealthCheck{URL: raw, Required: false, MinCode: 200, MaxCode: 399})
		}
	}
	for _, raw := range healthRanges {
		url, minCode, maxCode, parseErr := parseHealthRange(raw)
		if parseErr != nil {
			return parseErr
		}
		check := findHealth(resolved.Health, url)
		if check == nil {
			return fmt.Errorf("--health-range URL %q does not match a configured health check", url)
		}
		check.MinCode = minCode
		check.MaxCode = maxCode
	}
	for _, raw := range insecureHealthChecks {
		check := findHealth(resolved.Health, raw)
		if check == nil {
			return fmt.Errorf("--insecure-health URL %q does not match a configured health check", raw)
		}
		check.InsecureSkipVerify = true
	}
	if len(verificationChecks) > 0 {
		resolved.Verify = nil
		for _, raw := range verificationChecks {
			check, parseErr := parseVerificationCheck(raw)
			if parseErr != nil {
				return parseErr
			}
			resolved.Verify = append(resolved.Verify, check)
		}
	}
	if *ttl != "" {
		resolved.IdleTTL, err = config.ParseDuration(*ttl)
		if err != nil {
			return err
		}
	}
	if *maxAge != "" {
		resolved.MaxAge, err = config.ParseDuration(*maxAge)
		if err != nil {
			return err
		}
	}
	if *name == "" && resolved.Name == "" {
		*name = deriveProjectName(discoveredRoot)
	}
	result, err := c.service.Up(ctx, app.UpRequest{Config: resolved, Name: *name, Port: *port, SkipHealth: *skipHealth})
	if err != nil {
		return err
	}
	return c.writeJSONOrHuman(result, *jsonMode, func() {
		fmt.Fprintf(c.out, "Handoff URL: %s\n", result.Preview.HandoffURL)
		if result.Evicted != nil {
			fmt.Fprintf(c.out, "Evicted: %s\n", result.Evicted.Name)
		}
		for _, warning := range result.Warnings {
			fmt.Fprintf(c.out, "Warning: %s\n", warning)
		}
	})
}

func (c *cli) check(ctx context.Context, args []string) error {
	fs, jsonMode := basicFlagSet("check", c.errOut)
	_ = fs.Bool("non-interactive", false, "never prompt")
	if err := fs.Parse(args); err != nil {
		return err
	}
	selector, err := selectorFromArgs(fs.Args())
	if err != nil {
		return err
	}
	result, err := c.service.Check(ctx, selector)
	if err != nil {
		return err
	}
	return c.writeJSONOrHuman(result, *jsonMode, func() {
		fmt.Fprintf(c.out, "Verified handoff URL: %s\n", result.Preview.HandoffURL)
		for _, check := range result.Verification.Checks {
			fmt.Fprintf(c.out, "  %s -> HTTP %d\n", check.Path, check.StatusCode)
		}
	})
}

func (c *cli) down(ctx context.Context, args []string) error {
	fs, jsonMode := basicFlagSet("down", c.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	selector, err := selectorFromArgs(fs.Args())
	if err != nil {
		return err
	}
	result, err := c.service.Down(ctx, selector)
	if err != nil {
		return err
	}
	return c.writeJSONOrHuman(result, *jsonMode, func() { fmt.Fprintf(c.out, "Removed preview %s\n", result.Preview.Name) })
}

func (c *cli) list(args []string) error {
	fs, jsonMode := basicFlagSet("list", c.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	previews, err := c.service.List()
	if err != nil {
		return err
	}
	return c.writeJSONOrHuman(map[string]interface{}{"schema_version": 1, "previews": previews}, *jsonMode, func() {
		if len(previews) == 0 {
			fmt.Fprintln(c.out, "No active previews.")
			return
		}
		for _, preview := range previews {
			pin := ""
			if preview.Pinned {
				pin = " [pinned]"
			}
			fmt.Fprintf(c.out, "%-28s %s %s%s\n", preview.Name, preview.Status, preview.HandoffURL, pin)
		}
	})
}

func (c *cli) status(args []string) error {
	fs, jsonMode := basicFlagSet("status", c.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	selector, err := selectorFromArgs(fs.Args())
	if err != nil {
		return err
	}
	previews, err := c.service.List()
	if err != nil {
		return err
	}
	for _, preview := range previews {
		if preview.ID == selector || preview.Name == selector || preview.ProjectRoot == selector {
			return c.writeJSONOrHuman(preview, *jsonMode, func() { fmt.Fprintf(c.out, "%s\n%s\nstatus: %s\n", preview.Name, preview.HandoffURL, preview.Status) })
		}
	}
	return fmt.Errorf("preview %q not found", selector)
}

func (c *cli) logs(args []string) error {
	fs, jsonMode := basicFlagSet("logs", c.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	selector, err := selectorFromArgs(fs.Args())
	if err != nil {
		return err
	}
	previews, err := c.service.List()
	if err != nil {
		return err
	}
	for _, preview := range previews {
		if preview.ID == selector || preview.Name == selector || preview.ProjectRoot == selector {
			path := filepath.Join(c.paths.LogsDir, preview.ID+".jsonl")
			value := map[string]interface{}{"schema_version": 1, "preview": preview.Name, "path": path}
			return c.writeJSONOrHuman(value, *jsonMode, func() { fmt.Fprintln(c.out, path) })
		}
	}
	return fmt.Errorf("preview %q not found", selector)
}

func (c *cli) pin(args []string, pinned bool) error {
	name := "pin"
	if !pinned {
		name = "unpin"
	}
	fs, jsonMode := basicFlagSet(name, c.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	selector, err := selectorFromArgs(fs.Args())
	if err != nil {
		return err
	}
	result, err := c.service.SetPinned(selector, pinned)
	if err != nil {
		return err
	}
	return c.writeJSONOrHuman(result, *jsonMode, func() { fmt.Fprintf(c.out, "%s: %s\n", name, result.Preview.Name) })
}

func (c *cli) gc(ctx context.Context, args []string) error {
	fs, jsonMode := basicFlagSet("gc", c.errOut)
	_ = fs.Bool("non-interactive", false, "never prompt")
	if err := fs.Parse(args); err != nil {
		return err
	}
	result, err := c.service.GC(ctx)
	if err != nil {
		return err
	}
	return c.writeJSONOrHuman(result, *jsonMode, func() { fmt.Fprintf(c.out, "Removed %d expired previews.\n", len(result.Removed)) })
}

func (c *cli) doctor(ctx context.Context, args []string) error {
	fs, jsonMode := basicFlagSet("doctor", c.errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	report := doctor.Run(ctx, c.runner, c.tailscale, c.paths, c.caddyBinary, c.tailscaleBinary)
	if err := c.writeJSONOrHuman(report, *jsonMode, func() {
		for _, check := range report.Checks {
			fmt.Fprintf(c.out, "%-18s %-8s %s\n", check.Name, check.Status, check.Message)
		}
	}); err != nil {
		return err
	}
	if !report.Healthy {
		return errors.New("doctor found blocking issues")
	}
	return nil
}

func (c *cli) scheduler(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("service requires install or uninstall")
	}
	jsonMode := containsArg(args, "--json")
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	switch args[0] {
	case "install":
		result, installErr := scheduler.Install(ctx, c.runner, binary)
		if installErr != nil {
			return installErr
		}
		return c.writeJSONOrHuman(result, jsonMode, func() { fmt.Fprintf(c.out, "GC service installed: %s\n", result.Path) })
	case "uninstall":
		result, uninstallErr := scheduler.Uninstall(ctx, c.runner)
		if uninstallErr != nil {
			return uninstallErr
		}
		return c.writeJSONOrHuman(result, jsonMode, func() { fmt.Fprintf(c.out, "GC service uninstalled: %s\n", result.Path) })
	default:
		return fmt.Errorf("unknown service action %q", args[0])
	}
}

func (c *cli) writeJSONOrHuman(value interface{}, jsonMode bool, human func()) error {
	if jsonMode {
		encoder := json.NewEncoder(c.out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	human()
	return nil
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Type() string   { return "string" }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func basicFlagSet(name string, output io.Writer) (*flag.FlagSet, *bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(output)
	jsonMode := fs.Bool("json", false, "JSON output")
	return fs, jsonMode
}

func deriveProjectName(root string) string {
	repo := filepath.Base(root)
	branch := ""
	if result, err := (process.ExecRunner{}).Run(context.Background(), "git", "-C", root, "branch", "--show-current"); err == nil {
		branch = strings.TrimSpace(result.Stdout)
	}
	if branch == "" {
		if result, err := (process.ExecRunner{}).Run(context.Background(), "git", "-C", root, "rev-parse", "--short", "HEAD"); err == nil {
			branch = strings.TrimSpace(result.Stdout)
		}
	}
	if branch == "" {
		return repo
	}
	return repo + "-" + branch
}

func selectorFromArgs(args []string) (string, error) {
	if len(args) > 1 {
		return "", errors.New("expected zero or one preview selector")
	}
	if len(args) == 1 {
		return args[0], nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	_, root, err := config.Discover(cwd)
	return root, err
}

func containsArg(args []string, wanted string) bool {
	for _, arg := range args {
		if arg == wanted || strings.HasPrefix(arg, wanted+"=") {
			return true
		}
	}
	return false
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func applyRouteBooleans(routes []model.Route, paths []string, apply func(*model.Route)) error {
	for _, wanted := range paths {
		matched := false
		for i := range routes {
			if routes[i].Path == wanted {
				apply(&routes[i])
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("route path %q does not match a configured route", wanted)
		}
	}
	return nil
}

func parseHealthRange(value string) (string, int, int, error) {
	separator := strings.LastIndex(value, "=")
	if separator <= 0 || separator == len(value)-1 {
		return "", 0, 0, errors.New("--health-range must use URL=MIN-MAX")
	}
	rangeParts := strings.SplitN(value[separator+1:], "-", 2)
	if len(rangeParts) != 2 {
		return "", 0, 0, errors.New("--health-range must use URL=MIN-MAX")
	}
	minCode, minErr := strconv.Atoi(rangeParts[0])
	maxCode, maxErr := strconv.Atoi(rangeParts[1])
	if minErr != nil || maxErr != nil || minCode < 100 || maxCode > 599 || minCode > maxCode {
		return "", 0, 0, errors.New("--health-range status codes must satisfy 100 <= MIN <= MAX <= 599")
	}
	return value[:separator], minCode, maxCode, nil
}

func parseVerificationCheck(value string) (model.VerificationCheck, error) {
	check := model.VerificationCheck{Path: value, MinCode: 200, MaxCode: 399}
	if separator := strings.LastIndex(value, "="); separator > 0 && separator < len(value)-1 {
		rangeParts := strings.SplitN(value[separator+1:], "-", 2)
		if len(rangeParts) == 2 {
			minCode, minErr := strconv.Atoi(rangeParts[0])
			maxCode, maxErr := strconv.Atoi(rangeParts[1])
			if minErr == nil && maxErr == nil {
				check.Path = value[:separator]
				check.MinCode = minCode
				check.MaxCode = maxCode
			}
		}
	}
	if err := config.ValidateVerificationPath(check.Path); err != nil {
		return model.VerificationCheck{}, fmt.Errorf("--verify: %w", err)
	}
	if check.MinCode < 200 || check.MaxCode > 399 || check.MinCode > check.MaxCode {
		return model.VerificationCheck{}, errors.New("--verify status range must satisfy 200 <= MIN <= MAX <= 399")
	}
	return check, nil
}

func findHealth(checks []model.HealthCheck, url string) *model.HealthCheck {
	for i := range checks {
		if checks[i].URL == url {
			return &checks[i]
		}
	}
	return nil
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, `Tailpreview exposes already-running localhost services privately through Tailscale Serve.

Usage:
  tailpreview up [flags] [http://127.0.0.1:PORT]
  tailpreview down [name]
  tailpreview list
  tailpreview status [name]
  tailpreview check [name]
  tailpreview logs [name]
  tailpreview pin|unpin [name]
  tailpreview gc
  tailpreview doctor
  tailpreview paths
  tailpreview service install|uninstall
  tailpreview version

Run commands with --json for stable machine output. Tailpreview never uses Funnel.`)
}
