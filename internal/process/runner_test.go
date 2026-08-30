package process

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExecRunnerCancelsTheWholeProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := (ExecRunner{}).Run(ctx, "sh", "-c", "sleep 30")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("child process kept pipes open after cancellation for %s", elapsed)
	}
}
