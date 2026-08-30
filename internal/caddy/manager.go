package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jhumanj/tailpreview/internal/model"
	appPaths "github.com/jhumanj/tailpreview/internal/paths"
	"github.com/jhumanj/tailpreview/internal/process"
)

type Controller interface {
	Apply(ctx context.Context, previews []model.Preview) error
	Stop(ctx context.Context) error
	ActiveRequests(ctx context.Context) (map[string]int, error)
}

type Manager struct {
	Binary string
	Paths  appPaths.Paths
	Runner process.Runner
}

func (m Manager) Apply(ctx context.Context, previews []model.Preview) error {
	if len(previews) == 0 {
		return m.Stop(ctx)
	}
	candidate, err := Generate(m.Paths, previews)
	if err != nil {
		return err
	}
	candidatePath := m.Paths.CaddyJSON + ".candidate"
	if err := os.WriteFile(candidatePath, append(candidate, '\n'), 0o600); err != nil {
		return err
	}
	defer os.Remove(candidatePath)
	if _, err := m.Runner.Run(ctx, m.binary(), "validate", "--config", candidatePath); err != nil {
		return fmt.Errorf("validate generated Caddy config: %w", err)
	}
	if current, err := os.ReadFile(m.Paths.CaddyJSON); err == nil && bytes.Equal(bytes.TrimSpace(current), bytes.TrimSpace(candidate)) && m.running() {
		return nil
	}
	if m.running() {
		if err := m.load(ctx, candidate); err != nil {
			return err
		}
	} else if err := m.start(ctx, candidatePath); err != nil {
		return err
	}
	return atomicWrite(m.Paths.CaddyJSON, append(candidate, '\n'))
}

func (m Manager) Stop(ctx context.Context) error {
	if !m.running() {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/stop", nil)
	if err != nil {
		return err
	}
	resp, err := m.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("stop Caddy: status %d: %s", resp.StatusCode, body)
	}
	_ = os.Remove(m.Paths.CaddyPID)
	_ = os.Remove(m.Paths.CaddySock)
	return nil
}

func (m Manager) ActiveRequests(ctx context.Context) (map[string]int, error) {
	result := map[string]int{}
	if !m.running() {
		return result, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/reverse_proxy/upstreams", nil)
	resp, err := m.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Caddy upstream status returned %d", resp.StatusCode)
	}
	var payload []struct {
		Address     string `json:"address"`
		NumRequests int    `json:"num_requests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	for _, upstream := range payload {
		result[upstream.Address] += upstream.NumRequests
	}
	return result, nil
}

func (m Manager) load(ctx context.Context, config []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/load", bytes.NewReader(config))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client().Do(req)
	if err != nil {
		return fmt.Errorf("reload Caddy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("reload Caddy: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

func (m Manager) start(ctx context.Context, configPath string) error {
	_ = os.Remove(m.Paths.CaddySock)
	logPath := filepath.Join(m.Paths.LogsDir, "caddy-process.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- path is derived from Tailpreview's private state directory.
	if err != nil {
		return err
	}
	// #nosec G204 -- no shell is involved; the configurable Caddy binary is controlled by the local operator.
	cmd := exec.Command(m.binary(), "run", "--config", configPath, "--pidfile", m.Paths.CaddyPID)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start Caddy: %w", err)
	}
	_ = logFile.Close()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if m.running() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("Caddy did not create admin socket; see %s", logPath)
}

func (m Manager) running() bool {
	conn, err := net.DialTimeout("unix", m.Paths.CaddySock, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (m Manager) client() *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", m.Paths.CaddySock)
		},
	}
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func (m Manager) binary() string {
	if m.Binary != "" {
		return m.Binary
	}
	return "caddy"
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".caddy-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
