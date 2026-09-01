package model

import (
	"encoding/json"
	"fmt"
	"time"
)

const RegistryVersion = 1

type Duration time.Duration

func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

type Route struct {
	Path               string `json:"path" yaml:"path"`
	Upstream           string `json:"upstream" yaml:"upstream"`
	StripPrefix        bool   `json:"strip_prefix" yaml:"strip_prefix"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty" yaml:"insecure_skip_verify,omitempty"`
}

type HealthCheck struct {
	URL                string `json:"url" yaml:"url"`
	Required           bool   `json:"required" yaml:"required"`
	MinCode            int    `json:"min_code" yaml:"min_code"`
	MaxCode            int    `json:"max_code" yaml:"max_code"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty" yaml:"insecure_skip_verify,omitempty"`
}

type VerificationCheck struct {
	Path    string `json:"path" yaml:"path"`
	MinCode int    `json:"min_code" yaml:"min_code"`
	MaxCode int    `json:"max_code" yaml:"max_code"`
}

type VerificationResult struct {
	Path        string `json:"path"`
	StatusCode  int    `json:"status_code"`
	FinalOrigin string `json:"final_origin"`
}

type VerificationReport struct {
	VerifiedAt time.Time            `json:"verified_at"`
	Checks     []VerificationResult `json:"checks"`
}

type Preview struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	ProjectRoot     string              `json:"project_root"`
	ConfigPath      string              `json:"config_path,omitempty"`
	ExternalPort    int                 `json:"external_port"`
	GatewayPort     int                 `json:"gateway_port"`
	URL             string              `json:"url"`
	HandoffURL      string              `json:"handoff_url"`
	Routes          []Route             `json:"routes"`
	Health          []HealthCheck       `json:"health,omitempty"`
	Verify          []VerificationCheck `json:"verify,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
	LastUsedAt      time.Time           `json:"last_used_at"`
	IdleTTL         Duration            `json:"idle_ttl"`
	MaxAge          Duration            `json:"max_age"`
	Pinned          bool                `json:"pinned"`
	Status          string              `json:"status"`
	UnhealthySince  *time.Time          `json:"unhealthy_since,omitempty"`
	CookieWarning   bool                `json:"cookie_warning,omitempty"`
	LastHealthError string              `json:"last_health_error,omitempty"`
	LastVerifiedAt  *time.Time          `json:"last_verified_at,omitempty"`
}

type PortReservation struct {
	Port     int       `json:"port"`
	LastSeen time.Time `json:"last_seen"`
}

type Registry struct {
	Version      int                        `json:"version"`
	Previews     []Preview                  `json:"previews"`
	Reservations map[string]PortReservation `json:"reservations,omitempty"`
}

func NewRegistry() Registry {
	return Registry{Version: RegistryVersion, Previews: []Preview{}, Reservations: make(map[string]PortReservation)}
}
