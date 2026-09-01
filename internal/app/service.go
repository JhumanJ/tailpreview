package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jhumanj/tailpreview/internal/caddy"
	"github.com/jhumanj/tailpreview/internal/config"
	"github.com/jhumanj/tailpreview/internal/model"
	appPaths "github.com/jhumanj/tailpreview/internal/paths"
	"github.com/jhumanj/tailpreview/internal/state"
	"github.com/jhumanj/tailpreview/internal/tailscale"
)

const (
	DefaultPortStart    = 8443
	DefaultPortEnd      = 8452
	DefaultGatewayStart = 18080
	UnhealthyGrace      = 30 * time.Minute
	ReservationTTL      = 30 * 24 * time.Hour
)

type Health interface {
	Wait(ctx context.Context, checks []model.HealthCheck) error
	CheckOnce(ctx context.Context, checks []model.HealthCheck) error
}

type Verifier interface {
	Verify(ctx context.Context, rawURL string, checks []model.VerificationCheck) (model.VerificationReport, error)
}

type Service struct {
	Store        state.Store
	Paths        appPaths.Paths
	Caddy        caddy.Controller
	Tailscale    tailscale.Client
	Health       Health
	Verifier     Verifier
	Now          func() time.Time
	PortStart    int
	PortEnd      int
	GatewayStart int
	PortFree     func(int) bool
}

type UpRequest struct {
	Config     config.Resolved
	Name       string
	Port       int
	SkipHealth bool
}

type UpResult struct {
	SchemaVersion int            `json:"schema_version"`
	Action        string         `json:"action"`
	Preview       model.Preview  `json:"preview"`
	Evicted       *model.Preview `json:"evicted,omitempty"`
	Warnings      []string       `json:"warnings,omitempty"`
}

type ActionResult struct {
	SchemaVersion int             `json:"schema_version"`
	Action        string          `json:"action"`
	Preview       *model.Preview  `json:"preview,omitempty"`
	Removed       []model.Preview `json:"removed,omitempty"`
}

type CheckResult struct {
	SchemaVersion int                      `json:"schema_version"`
	Action        string                   `json:"action"`
	Preview       model.Preview            `json:"preview"`
	Verification  model.VerificationReport `json:"verification"`
}

func (s *Service) Up(ctx context.Context, request UpRequest) (UpResult, error) {
	if s.Verifier == nil {
		return UpResult{}, errors.New("final preview verifier is not configured")
	}
	if len(request.Config.Health) == 0 {
		request.Config.Health = defaultHealthChecks(request.Config.Routes)
	}
	if err := config.Validate(request.Config); err != nil {
		return UpResult{}, err
	}
	if !request.SkipHealth {
		if err := s.Health.Wait(ctx, request.Config.Health); err != nil {
			return UpResult{}, err
		}
	}
	locked, registry, err := s.lockRegistry()
	if err != nil {
		return UpResult{}, err
	}
	defer locked.Close()
	now := s.now()
	s.refreshUsage(&registry, now)
	s.pruneReservations(&registry, now)
	status, err := s.Tailscale.Status(ctx)
	if err != nil {
		return UpResult{}, err
	}
	if status.BackendState != "Running" || status.Self.DNSName == "" {
		return UpResult{}, errors.New("Tailscale is not running with a MagicDNS hostname")
	}
	name := request.Name
	if name == "" {
		name = request.Config.Name
	}
	if name == "" {
		name = deriveName(request.Config.ProjectRoot)
	}
	name = slug(name)
	if name == "" {
		return UpResult{}, errors.New("preview name is empty after normalization")
	}
	id := previewID(request.Config.ProjectRoot)
	for _, other := range registry.Previews {
		if other.ID != id && other.Name == name {
			name = name + "-" + id[:6]
			break
		}
	}
	existingIndex := indexByID(registry.Previews, id)
	var existing *model.Preview
	if existingIndex >= 0 {
		copy := registry.Previews[existingIndex]
		existing = &copy
	}
	port, gateway, evicted, err := s.choosePort(ctx, &registry, id, request.Port)
	if err != nil {
		return UpResult{}, err
	}
	created := now
	pinned := false
	if existing != nil {
		created = existing.CreatedAt
		pinned = existing.Pinned
		if now.Sub(existing.CreatedAt) >= request.Config.MaxAge || now.Sub(existing.LastUsedAt) >= request.Config.IdleTTL {
			created = now
		}
	}
	preview := model.Preview{
		ID:           id,
		Name:         name,
		ProjectRoot:  request.Config.ProjectRoot,
		ConfigPath:   request.Config.Path,
		ExternalPort: port,
		GatewayPort:  gateway,
		URL:          fmt.Sprintf("https://%s:%d", status.Self.DNSName, port),
		HandoffURL:   fmt.Sprintf("https://%s:%d", status.Self.DNSName, port),
		Routes:       request.Config.Routes,
		Health:       request.Config.Health,
		Verify:       request.Config.Verify,
		CreatedAt:    created,
		UpdatedAt:    now,
		LastUsedAt:   now,
		IdleTTL:      model.Duration(request.Config.IdleTTL),
		MaxAge:       model.Duration(request.Config.MaxAge),
		Pinned:       pinned,
		Status:       "active",
	}
	preview.CookieWarning = hasSameProject(registry.Previews, preview)
	oldPreviews := clonePreviews(registry.Previews)
	desired := withoutID(registry.Previews, id)
	if evicted != nil {
		desired = withoutID(desired, evicted.ID)
	}
	desired = append(desired, preview)
	sortPreviews(desired)
	if err := s.Caddy.Apply(ctx, desired); err != nil {
		return UpResult{}, err
	}
	if err := s.Tailscale.Expose(ctx, port, gateway); err != nil {
		_ = s.Caddy.Apply(ctx, oldPreviews)
		if evicted != nil && evicted.ExternalPort != port {
			_ = s.Tailscale.Expose(ctx, evicted.ExternalPort, evicted.GatewayPort)
		}
		return UpResult{}, err
	}
	report, verifyErr := s.Verifier.Verify(ctx, preview.URL, preview.Verify)
	if verifyErr != nil {
		_ = s.Tailscale.Remove(ctx, port)
		_ = s.Caddy.Apply(ctx, oldPreviews)
		if existing != nil {
			_ = s.Tailscale.Expose(ctx, existing.ExternalPort, existing.GatewayPort)
		} else if evicted != nil {
			_ = s.Tailscale.Expose(ctx, evicted.ExternalPort, evicted.GatewayPort)
		}
		return UpResult{}, fmt.Errorf("verify final preview URL: %w", verifyErr)
	}
	preview.LastVerifiedAt = &report.VerifiedAt
	desired = withoutID(desired, preview.ID)
	desired = append(desired, preview)
	sortPreviews(desired)
	if evicted != nil && evicted.ExternalPort != port {
		if err := s.Tailscale.Remove(ctx, evicted.ExternalPort); err != nil {
			_ = s.Tailscale.Remove(ctx, port)
			_ = s.Caddy.Apply(ctx, oldPreviews)
			_ = s.Tailscale.Expose(ctx, evicted.ExternalPort, evicted.GatewayPort)
			return UpResult{}, fmt.Errorf("remove evicted preview: %w", err)
		}
	}
	registry.Previews = desired
	registry.Reservations[id] = model.PortReservation{Port: port, LastSeen: now}
	if err := locked.Save(registry); err != nil {
		_ = s.Tailscale.Remove(ctx, port)
		_ = s.Caddy.Apply(ctx, oldPreviews)
		if existing != nil {
			_ = s.Tailscale.Expose(ctx, existing.ExternalPort, existing.GatewayPort)
		} else if evicted != nil {
			_ = s.Tailscale.Expose(ctx, evicted.ExternalPort, evicted.GatewayPort)
		}
		return UpResult{}, err
	}
	if evicted != nil {
		s.removePreviewLogs(evicted.ID)
	}
	warnings := []string{}
	if preview.CookieWarning {
		warnings = append(warnings, "multiple previews from this project share one hostname; if authentication sessions collide, configure unique cookie names per worktree")
	}
	return UpResult{SchemaVersion: 1, Action: "up", Preview: preview, Evicted: evicted, Warnings: warnings}, nil
}

func (s *Service) Check(ctx context.Context, selector string) (CheckResult, error) {
	locked, registry, err := s.lockRegistry()
	if err != nil {
		return CheckResult{}, err
	}
	defer locked.Close()
	index := findPreview(registry.Previews, selector)
	if index < 0 {
		return CheckResult{}, fmt.Errorf("preview %q not found", selector)
	}
	preview := registry.Previews[index]
	normalizePreview(&preview)
	if err := s.Health.CheckOnce(ctx, preview.Health); err != nil {
		return CheckResult{}, &SafeHandoffError{
			Code:        "local_health_failed",
			Phase:       "local_health",
			Message:     "local upstream health checks failed",
			Hostname:    hostnameOnly(preview.URL),
			Remediation: "Restore the already-running loopback service, then run tailpreview check again.",
		}
	}
	status, err := s.Tailscale.Status(ctx)
	if err != nil {
		return CheckResult{}, err
	}
	if status.BackendState != "Running" || status.Self.DNSName == "" {
		return CheckResult{}, &SafeHandoffError{
			Code:        "tailscale_unavailable",
			Phase:       "tailscale_status",
			Message:     "Tailscale is not running with a MagicDNS hostname",
			Hostname:    hostnameOnly(preview.HandoffURL),
			Remediation: "Restore Tailscale connectivity, then run tailpreview check again.",
		}
	}
	if !strings.EqualFold(strings.TrimSuffix(status.Self.DNSName, "."), hostnameOnly(preview.HandoffURL)) {
		return CheckResult{}, &SafeHandoffError{
			Code:        "tailnet_hostname_changed",
			Phase:       "tailscale_status",
			Message:     "the registered preview hostname no longer matches this Tailscale device",
			Hostname:    hostnameOnly(preview.HandoffURL),
			Remediation: "Run tailpreview down for the stale preview, then create it again with tailpreview up.",
		}
	}
	available, err := s.Tailscale.PortAvailable(ctx, preview.ExternalPort)
	if err != nil {
		return CheckResult{}, err
	}
	if available {
		return CheckResult{}, &SafeHandoffError{
			Code:        "serve_listener_missing",
			Phase:       "tailscale_status",
			Message:     "the registered Tailpreview HTTPS listener is missing from Tailscale Serve",
			Hostname:    hostnameOnly(preview.HandoffURL),
			Remediation: "Run tailpreview up again to restore only this preview port.",
		}
	}
	if enabled, funnelErr := s.Tailscale.FunnelEnabledOnPort(ctx, preview.ExternalPort); funnelErr != nil {
		return CheckResult{}, funnelErr
	} else if enabled {
		return CheckResult{}, &SafeHandoffError{
			Code:        "public_endpoint_detected",
			Phase:       "tailscale_status",
			Message:     "Tailscale Funnel is enabled on the registered Tailpreview port",
			Hostname:    hostnameOnly(preview.HandoffURL),
			Remediation: "Disable Funnel only on this port before restoring the preview with tailpreview up.",
		}
	}
	if s.Verifier == nil {
		return CheckResult{}, errors.New("final preview verifier is not configured")
	}
	report, err := s.Verifier.Verify(ctx, preview.HandoffURL, preview.Verify)
	if err != nil {
		return CheckResult{}, err
	}
	preview.LastVerifiedAt = &report.VerifiedAt
	preview.Status = "active"
	preview.LastHealthError = ""
	registry.Previews[index] = preview
	if err := locked.Save(registry); err != nil {
		return CheckResult{}, err
	}
	return CheckResult{SchemaVersion: 1, Action: "check", Preview: preview, Verification: report}, nil
}

func (s *Service) Down(ctx context.Context, selector string) (ActionResult, error) {
	locked, registry, err := s.lockRegistry()
	if err != nil {
		return ActionResult{}, err
	}
	defer locked.Close()
	index := findPreview(registry.Previews, selector)
	if index < 0 {
		return ActionResult{}, fmt.Errorf("preview %q not found", selector)
	}
	preview := registry.Previews[index]
	desired := append([]model.Preview{}, registry.Previews[:index]...)
	desired = append(desired, registry.Previews[index+1:]...)
	if err := s.Caddy.Apply(ctx, desired); err != nil {
		return ActionResult{}, err
	}
	if err := s.Tailscale.Remove(ctx, preview.ExternalPort); err != nil {
		_ = s.Caddy.Apply(ctx, registry.Previews)
		return ActionResult{}, err
	}
	registry.Previews = desired
	registry.Reservations[preview.ID] = model.PortReservation{Port: preview.ExternalPort, LastSeen: s.now()}
	if err := locked.Save(registry); err != nil {
		_ = s.Caddy.Apply(ctx, append(desired, preview))
		_ = s.Tailscale.Expose(ctx, preview.ExternalPort, preview.GatewayPort)
		return ActionResult{}, err
	}
	s.removePreviewLogs(preview.ID)
	return ActionResult{SchemaVersion: 1, Action: "down", Preview: &preview}, nil
}

func (s *Service) List() ([]model.Preview, error) {
	locked, registry, err := s.lockRegistry()
	if err != nil {
		return nil, err
	}
	defer locked.Close()
	s.refreshUsage(&registry, s.now())
	for i := range registry.Previews {
		normalizePreview(&registry.Previews[i])
	}
	if err := locked.Save(registry); err != nil {
		return nil, err
	}
	sortPreviews(registry.Previews)
	return registry.Previews, nil
}

func (s *Service) SetPinned(selector string, pinned bool) (ActionResult, error) {
	locked, registry, err := s.lockRegistry()
	if err != nil {
		return ActionResult{}, err
	}
	defer locked.Close()
	index := findPreview(registry.Previews, selector)
	if index < 0 {
		return ActionResult{}, fmt.Errorf("preview %q not found", selector)
	}
	registry.Previews[index].Pinned = pinned
	registry.Previews[index].UpdatedAt = s.now()
	if err := locked.Save(registry); err != nil {
		return ActionResult{}, err
	}
	action := "pin"
	if !pinned {
		action = "unpin"
	}
	return ActionResult{SchemaVersion: 1, Action: action, Preview: &registry.Previews[index]}, nil
}

func (s *Service) GC(ctx context.Context) (ActionResult, error) {
	locked, registry, err := s.lockRegistry()
	if err != nil {
		return ActionResult{}, err
	}
	defer locked.Close()
	now := s.now()
	s.refreshUsage(&registry, now)
	active, _ := s.Caddy.ActiveRequests(ctx)
	kept := make([]model.Preview, 0, len(registry.Previews))
	removed := []model.Preview{}
	for i := range registry.Previews {
		preview := registry.Previews[i]
		if err := s.Health.CheckOnce(ctx, preview.Health); err != nil {
			preview.Status = "unhealthy"
			preview.LastHealthError = err.Error()
			if preview.UnhealthySince == nil {
				t := now
				preview.UnhealthySince = &t
			}
		} else {
			preview.Status = "active"
			preview.UnhealthySince = nil
			preview.LastHealthError = ""
		}
		expired := !preview.Pinned && (now.Sub(preview.LastUsedAt) >= preview.IdleTTL.Duration() || now.Sub(preview.CreatedAt) >= preview.MaxAge.Duration())
		unhealthyExpired := !preview.Pinned && preview.UnhealthySince != nil && now.Sub(*preview.UnhealthySince) >= UnhealthyGrace
		if (expired || unhealthyExpired) && !previewHasActiveRequests(preview, active) {
			removed = append(removed, preview)
			registry.Reservations[preview.ID] = model.PortReservation{Port: preview.ExternalPort, LastSeen: now}
			continue
		}
		kept = append(kept, preview)
	}
	if len(removed) == 0 {
		registry.Previews = kept
		if err := s.Caddy.Apply(ctx, kept); err != nil {
			return ActionResult{}, err
		}
		if err := locked.Save(registry); err != nil {
			return ActionResult{}, err
		}
		return ActionResult{SchemaVersion: 1, Action: "gc"}, nil
	}
	old := registry.Previews
	if err := s.Caddy.Apply(ctx, kept); err != nil {
		return ActionResult{}, err
	}
	for _, preview := range removed {
		if err := s.Tailscale.Remove(ctx, preview.ExternalPort); err != nil {
			_ = s.Caddy.Apply(ctx, old)
			for _, restored := range removed {
				_ = s.Tailscale.Expose(ctx, restored.ExternalPort, restored.GatewayPort)
			}
			return ActionResult{}, err
		}
	}
	registry.Previews = kept
	s.pruneReservations(&registry, now)
	if err := locked.Save(registry); err != nil {
		_ = s.Caddy.Apply(ctx, old)
		for _, restored := range removed {
			_ = s.Tailscale.Expose(ctx, restored.ExternalPort, restored.GatewayPort)
		}
		return ActionResult{}, err
	}
	for _, preview := range removed {
		s.removePreviewLogs(preview.ID)
	}
	return ActionResult{SchemaVersion: 1, Action: "gc", Removed: removed}, nil
}

func (s *Service) choosePort(ctx context.Context, registry *model.Registry, id string, requested int) (int, int, *model.Preview, error) {
	if existing := indexByID(registry.Previews, id); existing >= 0 {
		preview := registry.Previews[existing]
		if requested != 0 && requested != preview.ExternalPort {
			return 0, 0, nil, fmt.Errorf("preview already owns port %d; run down before changing it", preview.ExternalPort)
		}
		return preview.ExternalPort, preview.GatewayPort, nil, nil
	}
	used := map[int]bool{}
	for _, preview := range registry.Previews {
		used[preview.ExternalPort] = true
	}
	candidates := []int{}
	if requested != 0 {
		if requested < s.portStart() || requested > s.portEnd() {
			return 0, 0, nil, fmt.Errorf("requested port %d is outside pool %d-%d", requested, s.portStart(), s.portEnd())
		}
		candidates = append(candidates, requested)
	} else if reservation, ok := registry.Reservations[id]; ok {
		candidates = append(candidates, reservation.Port)
	}
	for port := s.portStart(); port <= s.portEnd(); port++ {
		if !containsInt(candidates, port) {
			candidates = append(candidates, port)
		}
	}
	for _, port := range candidates {
		if used[port] {
			if requested != 0 {
				return 0, 0, nil, fmt.Errorf("requested port %d is already owned by another preview", port)
			}
			continue
		}
		gateway := s.gatewayFor(port)
		if !s.localPortFree(gateway) {
			if requested != 0 {
				return 0, 0, nil, fmt.Errorf("gateway port %d for requested port %d is already in use", gateway, port)
			}
			continue
		}
		available, err := s.Tailscale.PortAvailable(ctx, port)
		if err != nil {
			return 0, 0, nil, err
		}
		if available {
			return port, gateway, nil, nil
		}
		if requested != 0 {
			return 0, 0, nil, fmt.Errorf("requested Tailscale Serve port %d is already configured", port)
		}
	}
	active, _ := s.Caddy.ActiveRequests(ctx)
	candidatesForEviction := make([]model.Preview, 0, len(registry.Previews))
	for _, preview := range registry.Previews {
		if !preview.Pinned && !previewHasActiveRequests(preview, active) {
			candidatesForEviction = append(candidatesForEviction, preview)
		}
	}
	if len(candidatesForEviction) == 0 {
		return 0, 0, nil, errors.New("preview limit reached and every preview is pinned or active")
	}
	sort.Slice(candidatesForEviction, func(i, j int) bool {
		return candidatesForEviction[i].LastUsedAt.Before(candidatesForEviction[j].LastUsedAt)
	})
	victim := candidatesForEviction[0]
	return victim.ExternalPort, victim.GatewayPort, &victim, nil
}

func (s *Service) lockRegistry() (*state.Locked, model.Registry, error) {
	locked, err := s.Store.Lock()
	if err != nil {
		return nil, model.Registry{}, err
	}
	registry, err := locked.Load()
	if err != nil {
		_ = locked.Close()
		return nil, model.Registry{}, err
	}
	return locked, registry, nil
}

func (s *Service) refreshUsage(registry *model.Registry, now time.Time) {
	for i := range registry.Previews {
		logPath := filepath.Join(s.Paths.LogsDir, registry.Previews[i].ID+".jsonl")
		if info, err := os.Stat(logPath); err == nil && info.ModTime().After(registry.Previews[i].LastUsedAt) && !info.ModTime().After(now) {
			registry.Previews[i].LastUsedAt = info.ModTime()
		}
	}
}

func (s *Service) removePreviewLogs(id string) {
	matches, err := filepath.Glob(filepath.Join(s.Paths.LogsDir, id+".jsonl*"))
	if err != nil {
		return
	}
	for _, path := range matches {
		_ = os.Remove(path)
	}
}

func (s *Service) pruneReservations(registry *model.Registry, now time.Time) {
	for id, reservation := range registry.Reservations {
		if now.Sub(reservation.LastSeen) > ReservationTTL {
			delete(registry.Reservations, id)
		}
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) portStart() int {
	if s.PortStart != 0 {
		return s.PortStart
	}
	return DefaultPortStart
}

func (s *Service) portEnd() int {
	if s.PortEnd != 0 {
		return s.PortEnd
	}
	return DefaultPortEnd
}

func (s *Service) gatewayFor(port int) int {
	start := s.GatewayStart
	if start == 0 {
		start = DefaultGatewayStart
	}
	return start + port - s.portStart()
}

func (s *Service) localPortFree(port int) bool {
	if s.PortFree != nil {
		return s.PortFree(port)
	}
	return LocalPortFree(port)
}

func previewID(root string) string {
	abs, _ := filepath.Abs(root)
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:8])
}

func deriveName(root string) string {
	name := filepath.Base(root)
	parent := filepath.Base(filepath.Dir(root))
	if strings.EqualFold(name, parent) {
		return name
	}
	return name
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var result strings.Builder
	lastDash := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			result.WriteRune(r)
			lastDash = false
		} else if !lastDash && result.Len() > 0 {
			result.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(result.String(), "-")
}

func indexByID(previews []model.Preview, id string) int {
	for i := range previews {
		if previews[i].ID == id {
			return i
		}
	}
	return -1
}

func findPreview(previews []model.Preview, selector string) int {
	for i := range previews {
		if previews[i].ID == selector || previews[i].Name == selector || previews[i].ProjectRoot == selector {
			return i
		}
	}
	return -1
}

func withoutID(previews []model.Preview, id string) []model.Preview {
	result := make([]model.Preview, 0, len(previews))
	for _, preview := range previews {
		if preview.ID != id {
			result = append(result, preview)
		}
	}
	return result
}

func hasSameProject(previews []model.Preview, current model.Preview) bool {
	currentBase := filepath.Base(current.ProjectRoot)
	for _, preview := range previews {
		if preview.ID != current.ID && filepath.Base(preview.ProjectRoot) == currentBase {
			return true
		}
	}
	return false
}

func clonePreviews(previews []model.Preview) []model.Preview {
	result := make([]model.Preview, len(previews))
	copy(result, previews)
	return result
}

func sortPreviews(previews []model.Preview) {
	sort.Slice(previews, func(i, j int) bool { return previews[i].ExternalPort < previews[j].ExternalPort })
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func previewHasActiveRequests(preview model.Preview, active map[string]int) bool {
	for _, route := range preview.Routes {
		u, err := url.Parse(route.Upstream)
		if err == nil && active[u.Host] > 0 {
			return true
		}
	}
	return false
}

func defaultHealthChecks(routes []model.Route) []model.HealthCheck {
	seen := map[string]bool{}
	checks := make([]model.HealthCheck, 0, len(routes))
	for _, route := range routes {
		if seen[route.Upstream] {
			continue
		}
		seen[route.Upstream] = true
		checks = append(checks, model.HealthCheck{
			URL:                route.Upstream,
			Required:           true,
			MinCode:            100,
			MaxCode:            499,
			InsecureSkipVerify: route.InsecureSkipVerify,
		})
	}
	return checks
}

func normalizePreview(preview *model.Preview) {
	if preview.HandoffURL == "" {
		preview.HandoffURL = preview.URL
	}
	if len(preview.Verify) == 0 {
		preview.Verify = []model.VerificationCheck{{Path: "/", MinCode: 200, MaxCode: 399}}
	}
	if len(preview.Health) == 0 {
		preview.Health = defaultHealthChecks(preview.Routes)
	}
}

func LocalPortFree(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}
