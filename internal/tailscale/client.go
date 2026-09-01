package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jhumanj/tailpreview/internal/process"
)

type Status struct {
	BackendState string `json:"BackendState"`
	Self         struct {
		DNSName   string `json:"DNSName"`
		Online    bool   `json:"Online"`
		KeyExpiry string `json:"KeyExpiry"`
	} `json:"Self"`
	CurrentTailnet struct {
		MagicDNSSuffix  string `json:"MagicDNSSuffix"`
		MagicDNSEnabled bool   `json:"MagicDNSEnabled"`
	} `json:"CurrentTailnet"`
}

type Client interface {
	Status(ctx context.Context) (Status, error)
	PortAvailable(ctx context.Context, port int) (bool, error)
	FunnelEnabled(ctx context.Context) (bool, error)
	FunnelEnabledOnPort(ctx context.Context, port int) (bool, error)
	Expose(ctx context.Context, externalPort, gatewayPort int) error
	Remove(ctx context.Context, externalPort int) error
}

type CLI struct {
	Binary string
	Runner process.Runner
}

func (c CLI) Status(ctx context.Context) (Status, error) {
	result, err := c.Runner.Run(ctx, c.binary(), "status", "--json")
	if err != nil {
		return Status{}, err
	}
	var status Status
	if err := json.Unmarshal([]byte(result.Stdout), &status); err != nil {
		return Status{}, fmt.Errorf("parse tailscale status: %w", err)
	}
	status.Self.DNSName = strings.TrimSuffix(status.Self.DNSName, ".")
	return status, nil
}

func (c CLI) PortAvailable(ctx context.Context, port int) (bool, error) {
	payload, err := c.serveStatus(ctx)
	if err != nil {
		return false, err
	}
	return !containsPort(payload, port), nil
}

func (c CLI) FunnelEnabled(ctx context.Context) (bool, error) {
	payload, err := c.serveStatus(ctx)
	if err != nil {
		return false, err
	}
	return containsEnabledFunnel(payload), nil
}

func (c CLI) FunnelEnabledOnPort(ctx context.Context, port int) (bool, error) {
	payload, err := c.serveStatus(ctx)
	if err != nil {
		return false, err
	}
	return containsEnabledFunnelPort(payload, port, false), nil
}

func (c CLI) Expose(ctx context.Context, externalPort, gatewayPort int) error {
	target := fmt.Sprintf("http://127.0.0.1:%d", gatewayPort)
	commandCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result, err := c.Runner.Run(commandCtx, c.binary(), "serve", "--yes", "--bg", fmt.Sprintf("--https=%d", externalPort), target)
	combined := result.Stdout + "\n" + result.Stderr
	lower := strings.ToLower(combined)
	if strings.Contains(lower, "serve is not enabled") {
		return fmt.Errorf("Tailscale Serve needs one-time tailnet consent; run `tailscale serve --bg --https=%d text:tailpreview-consent`, approve its private Serve page, then run `tailscale serve --https=%d off` and retry", externalPort, externalPort)
	}
	if strings.Contains(lower, "available on the internet") {
		_ = c.Remove(ctx, externalPort)
		return fmt.Errorf("tailscale reported a public endpoint; removed port %d", externalPort)
	}
	if err != nil {
		if commandCtx.Err() == context.DeadlineExceeded {
			return errors.New("Tailscale Serve did not finish within 15 seconds; it may be waiting for one-time tailnet consent")
		}
		return err
	}
	return nil
}

func (c CLI) Remove(ctx context.Context, externalPort int) error {
	commandCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := c.Runner.Run(commandCtx, c.binary(), "serve", "--yes", fmt.Sprintf("--https=%d", externalPort), "off")
	return err
}

func (c CLI) binary() string {
	if c.Binary != "" {
		return c.Binary
	}
	return "tailscale"
}

func (c CLI) serveStatus(ctx context.Context) (interface{}, error) {
	result, err := c.Runner.Run(ctx, c.binary(), "serve", "status", "--json")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return map[string]interface{}{}, nil
	}
	var payload interface{}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return nil, fmt.Errorf("parse tailscale serve status: %w", err)
	}
	return payload, nil
}

func containsPort(value interface{}, port int) bool {
	wanted := strconv.Itoa(port)
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if key == wanted || stringContainsPort(key, port) || containsPort(child, port) {
				return true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if containsPort(child, port) {
				return true
			}
		}
	case string:
		return typed == wanted || stringContainsPort(typed, port)
	case float64:
		return int(typed) == port
	}
	return false
}

func stringContainsPort(value string, port int) bool {
	if parsed, err := url.Parse(value); err == nil && parsed.Port() == strconv.Itoa(port) {
		return true
	}
	needle := ":" + strconv.Itoa(port)
	return strings.Contains(value, needle+"/") || strings.HasSuffix(value, needle) || strings.Contains(value, needle+" ")
}

func containsEnabledFunnel(value interface{}) bool {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if strings.Contains(strings.ToLower(key), "funnel") && truthy(child) {
				return true
			}
			if containsEnabledFunnel(child) {
				return true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if containsEnabledFunnel(child) {
				return true
			}
		}
	}
	return false
}

func containsEnabledFunnelPort(value interface{}, port int, insideFunnel bool) bool {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			nextInside := insideFunnel || strings.Contains(strings.ToLower(key), "funnel")
			if nextInside && (key == strconv.Itoa(port) || stringContainsPort(key, port)) && truthy(child) {
				return true
			}
			if containsEnabledFunnelPort(child, port, nextInside) {
				return true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if containsEnabledFunnelPort(child, port, insideFunnel) {
				return true
			}
		}
	case string:
		return insideFunnel && (typed == strconv.Itoa(port) || stringContainsPort(typed, port))
	case float64:
		return insideFunnel && int(typed) == port
	}
	return false
}

func truthy(value interface{}) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case map[string]interface{}:
		for _, child := range typed {
			if truthy(child) {
				return true
			}
		}
	case []interface{}:
		return len(typed) > 0
	case string:
		return typed != "" && typed != "false"
	case float64:
		return typed != 0
	}
	return false
}
