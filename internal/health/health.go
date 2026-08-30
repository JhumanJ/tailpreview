package health

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jhumanj/tailpreview/internal/model"
)

type Checker struct {
	Timeout time.Duration
}

func (c Checker) Wait(ctx context.Context, checks []model.HealthCheck) error {
	if len(checks) == 0 {
		return nil
	}
	deadline := time.Now().Add(c.Timeout)
	if c.Timeout <= 0 {
		deadline = time.Now().Add(30 * time.Second)
	}
	var last []string
	for {
		last = last[:0]
		allHealthy := true
		for _, check := range checks {
			if err := c.one(ctx, check); err != nil {
				last = append(last, err.Error())
				if check.Required {
					allHealthy = false
				}
			}
		}
		if allHealthy {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("health checks did not pass: %s", strings.Join(last, "; "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (c Checker) CheckOnce(ctx context.Context, checks []model.HealthCheck) error {
	var failures []string
	for _, check := range checks {
		if err := c.one(ctx, check); err != nil && check.Required {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("health checks failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (c Checker) one(ctx context.Context, check model.HealthCheck) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: check.InsecureSkipVerify} // #nosec G402 -- explicit per-project opt-in for loopback only.
	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, check.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Tailpreview-Health", "1")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", check.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < check.MinCode || resp.StatusCode > check.MaxCode {
		return fmt.Errorf("%s: status %d outside %d-%d", check.URL, resp.StatusCode, check.MinCode, check.MaxCode)
	}
	return nil
}
