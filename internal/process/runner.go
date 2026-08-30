package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Result struct {
	Stdout string
	Stderr string
}

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
	LookPath(name string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (Result, error) {
	// #nosec G204 -- arguments are passed directly without a shell; binary overrides are an explicit local-operator feature.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := Result{Stdout: strings.TrimSpace(stdout.String()), Stderr: strings.TrimSpace(stderr.String())}
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return result, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, result.Stderr)
	}
	return result, nil
}

func (ExecRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}
