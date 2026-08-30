package doctor

import (
	"context"
	"fmt"
	"time"

	"github.com/jhumanj/tailpreview/internal/paths"
	"github.com/jhumanj/tailpreview/internal/process"
	"github.com/jhumanj/tailpreview/internal/tailscale"
)

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type Report struct {
	SchemaVersion int     `json:"schema_version"`
	Healthy       bool    `json:"healthy"`
	Checks        []Check `json:"checks"`
}

func Run(ctx context.Context, runner process.Runner, ts tailscale.Client, p paths.Paths, caddyBinary, tailscaleBinary string) Report {
	report := Report{SchemaVersion: 1, Healthy: true}
	add := func(name, status, message string) {
		report.Checks = append(report.Checks, Check{Name: name, Status: status, Message: message})
		if status == "error" {
			report.Healthy = false
		}
	}
	if path, err := runner.LookPath(caddyBinary); err != nil {
		add("caddy", "error", "Caddy is not installed or not on PATH")
	} else if result, err := runner.Run(ctx, path, "version"); err != nil {
		add("caddy", "error", err.Error())
	} else {
		add("caddy", "ok", result.Stdout)
	}
	if path, err := runner.LookPath(tailscaleBinary); err != nil {
		add("tailscale-cli", "error", "Tailscale is not installed or not on PATH")
	} else {
		add("tailscale-cli", "ok", path)
	}
	status, err := ts.Status(ctx)
	if err != nil {
		add("tailscale-status", "error", err.Error())
	} else {
		if status.BackendState != "Running" {
			add("tailscale-status", "error", "backend state is "+status.BackendState)
		} else {
			add("tailscale-status", "ok", status.Self.DNSName)
		}
		if status.CurrentTailnet.MagicDNSEnabled || status.Self.DNSName != "" {
			add("magicdns", "ok", status.CurrentTailnet.MagicDNSSuffix)
		} else {
			add("magicdns", "error", "MagicDNS or a tailnet DNS name is required")
		}
		if status.Self.KeyExpiry != "" {
			if expiry, parseErr := time.Parse(time.RFC3339, status.Self.KeyExpiry); parseErr == nil {
				remaining := time.Until(expiry)
				if remaining < 0 {
					add("key-expiry", "error", "Tailscale node key is expired")
				} else if remaining < 30*24*time.Hour {
					add("key-expiry", "warning", fmt.Sprintf("node key expires in %s", remaining.Round(time.Hour)))
				} else {
					add("key-expiry", "ok", expiry.Format(time.RFC3339))
				}
			}
		}
	}
	if enabled, funnelErr := ts.FunnelEnabled(ctx); funnelErr != nil {
		add("funnel", "error", funnelErr.Error())
	} else if enabled {
		add("funnel", "warning", "Funnel is configured on this device; Tailpreview will never use it")
	} else {
		add("funnel", "ok", "no public Funnel endpoint detected")
	}
	add("serve-consent", "warning", "one-time Tailscale Serve consent cannot be verified read-only; the first up command will report it safely if required")
	if err := p.Ensure(); err != nil {
		add("state-paths", "error", err.Error())
	} else {
		add("state-paths", "ok", p.StateDir)
	}
	return report
}
