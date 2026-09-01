package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jhumanj/tailpreview/internal/config"
	"github.com/jhumanj/tailpreview/internal/model"
)

const maxVerificationRedirects = 5

type HTTPVerifier struct {
	Timeout   time.Duration
	Transport http.RoundTripper
}

func (v HTTPVerifier) Verify(ctx context.Context, rawURL string, checks []model.VerificationCheck) (model.VerificationReport, error) {
	base, err := url.Parse(rawURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || isLoopbackHostname(base.Hostname()) || base.User != nil || (base.Path != "" && base.Path != "/") || base.RawQuery != "" || base.Fragment != "" {
		return model.VerificationReport{}, &SafeHandoffError{
			Code: "invalid_handoff_url", Phase: "final_verification",
			Message: "final handoff URL must be an absolute non-loopback HTTPS origin",
		}
	}
	if len(checks) == 0 {
		checks = []model.VerificationCheck{{Path: "/", MinCode: 200, MaxCode: 399}}
	}
	for _, check := range checks {
		if config.ValidateVerificationPath(check.Path) != nil || check.MinCode < 200 || check.MaxCode > 399 || check.MinCode > check.MaxCode {
			return model.VerificationReport{}, &SafeHandoffError{
				Code: "invalid_verification_check", Phase: "final_verification",
				Message:     "stored final verification configuration is invalid",
				Hostname:    base.Hostname(),
				Remediation: "Run tailpreview up with valid query-free public verification paths and 2xx/3xx ranges.",
			}
		}
	}
	report := model.VerificationReport{Checks: make([]model.VerificationResult, 0, len(checks))}
	for _, check := range checks {
		result, verifyErr := v.verifyOne(ctx, base, check)
		if verifyErr != nil {
			return model.VerificationReport{}, verifyErr
		}
		report.Checks = append(report.Checks, result)
	}
	report.VerifiedAt = time.Now().UTC()
	return report, nil
}

func (v HTTPVerifier) verifyOne(ctx context.Context, base *url.URL, check model.VerificationCheck) (model.VerificationResult, error) {
	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	pathURL, _ := url.Parse(check.Path)
	target := base.ResolveReference(pathURL)
	var lastStatus int
	for {
		client := &http.Client{
			Timeout:   3 * time.Second,
			Transport: v.Transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= maxVerificationRedirects {
					return &SafeHandoffError{
						Code: "redirect_limit_exceeded", Phase: "final_verification",
						Message:  "final preview exceeded the same-origin redirect limit",
						Hostname: base.Hostname(), TargetPath: check.Path,
						Remediation: "Fix the application redirect loop, then run tailpreview check again.",
					}
				}
				if req.URL.User != nil {
					return &SafeHandoffError{
						Code: "credentialed_redirect", Phase: "final_verification",
						Message:  "final preview redirected to a URL containing credentials",
						Hostname: base.Hostname(), TargetPath: check.Path,
						RedirectOrigin: sanitizedOrigin(req.URL),
						Remediation:    "Remove credentials from the application redirect target, then run tailpreview check again.",
					}
				}
				if sameOrigin(base, req.URL) {
					return nil
				}
				if isLoopbackHostname(req.URL.Hostname()) {
					return &SafeHandoffError{
						Code: "loopback_redirect", Phase: "final_verification",
						Message:  "final preview redirected to a loopback origin",
						Hostname: base.Hostname(), TargetPath: check.Path,
						RedirectOrigin: sanitizedOrigin(req.URL),
						Remediation:    "Configure the application external origin, authentication, and WebAuthn settings to use the exact Tailpreview HTTPS origin. Permanent redirects may require a private browser window after correction.",
					}
				}
				if base.Scheme == "https" && req.URL.Scheme != "https" {
					return &SafeHandoffError{
						Code: "https_downgrade_redirect", Phase: "final_verification",
						Message:  "final preview redirected from HTTPS to a non-HTTPS origin",
						Hostname: base.Hostname(), TargetPath: check.Path,
						RedirectOrigin: sanitizedOrigin(req.URL),
						Remediation:    "Configure the application external origin to use the Tailpreview HTTPS URL.",
					}
				}
				if !sameOrigin(base, req.URL) {
					return &SafeHandoffError{
						Code: "cross_origin_redirect", Phase: "final_verification",
						Message:  "final preview redirected outside its Tailpreview origin",
						Hostname: base.Hostname(), TargetPath: check.Path,
						RedirectOrigin: sanitizedOrigin(req.URL),
						Remediation:    "Configure the application external origin, authentication, and WebAuthn settings to use the exact Tailpreview HTTPS origin. Permanent redirects may require a private browser window after correction.",
					}
				}
				return nil
			},
		}
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if requestErr != nil {
			return model.VerificationResult{}, requestErr
		}
		req.Header.Set("X-Tailpreview-Verify", "1")
		resp, requestErr := client.Do(req)
		if requestErr != nil {
			var safe *SafeHandoffError
			if errors.As(requestErr, &safe) {
				return model.VerificationResult{}, safe
			}
			if ctx.Err() != nil {
				return model.VerificationResult{}, ctx.Err()
			}
		} else {
			_ = resp.Body.Close()
			lastStatus = resp.StatusCode
			if resp.StatusCode >= check.MinCode && resp.StatusCode <= check.MaxCode && (resp.StatusCode < 300 || resp.StatusCode >= 400) {
				return model.VerificationResult{Path: check.Path, StatusCode: resp.StatusCode, FinalOrigin: sanitizedOrigin(resp.Request.URL)}, nil
			}
			if resp.StatusCode == http.StatusForbidden {
				return model.VerificationResult{}, &SafeHandoffError{
					Code: "external_forbidden", Phase: "final_verification",
					Message:    "final preview returned HTTP 403 through the Tailpreview origin",
					StatusCode: resp.StatusCode, Hostname: base.Hostname(), TargetPath: check.Path,
					Remediation: "Check the exact development-server hostname allowlist and the route or authentication authorization, restart the local service if its runtime configuration changed, then retry.",
				}
			}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return model.VerificationResult{}, unexpectedStatusError(base, check, resp.StatusCode)
			}
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				return model.VerificationResult{}, &SafeHandoffError{
					Code: "invalid_redirect", Phase: "final_verification",
					Message:    "final preview returned a redirect without a usable same-origin destination",
					StatusCode: resp.StatusCode, Hostname: base.Hostname(), TargetPath: check.Path,
					Remediation: "Fix the application redirect target, then run tailpreview check again.",
				}
			}
		}
		if time.Now().After(deadline) {
			if lastStatus != 0 {
				return model.VerificationResult{}, unexpectedStatusError(base, check, lastStatus)
			}
			return model.VerificationResult{}, &SafeHandoffError{
				Code: "external_unreachable", Phase: "final_verification",
				Message:  "final Tailpreview HTTPS endpoint was unreachable before the verification timeout",
				Hostname: base.Hostname(), TargetPath: check.Path,
				Remediation: "Check the local service, Caddy, Tailscale Serve, and tailnet connectivity, then retry.",
			}
		}
		select {
		case <-ctx.Done():
			return model.VerificationResult{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func unexpectedStatusError(base *url.URL, check model.VerificationCheck, status int) *SafeHandoffError {
	return &SafeHandoffError{
		Code: "unexpected_http_status", Phase: "final_verification",
		Message:    "final preview returned an HTTP status outside the configured range",
		StatusCode: status, Hostname: base.Hostname(), TargetPath: check.Path,
		Remediation: "Fix the public route or adjust its explicit 2xx/3xx verification range, then retry.",
	}
}

func sameOrigin(left, right *url.URL) bool {
	leftHost := strings.TrimSuffix(left.Hostname(), ".")
	rightHost := strings.TrimSuffix(right.Hostname(), ".")
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(leftHost, rightHost) && effectivePort(left) == effectivePort(right)
}

func effectivePort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if value.Scheme == "https" {
		return "443"
	}
	return "80"
}

func sanitizedOrigin(value *url.URL) string {
	if value == nil {
		return ""
	}
	host := value.Host
	if parsedHost, port, err := net.SplitHostPort(value.Host); err == nil {
		host = net.JoinHostPort(parsedHost, port)
	}
	return (&url.URL{Scheme: value.Scheme, Host: host}).String()
}

func isLoopbackHostname(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
