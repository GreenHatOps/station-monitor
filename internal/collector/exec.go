package collector

import (
	"context"
	"os/exec"
	"time"
)

// CommandRunner abstracts command execution for testability.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// RealRunner executes commands on the host.
type RealRunner struct{}

func (r *RealRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// ExecWithTimeout runs a command with a timeout and returns stdout.
func ExecWithTimeout(runner CommandRunner, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runner.Run(ctx, name, args...)
}
