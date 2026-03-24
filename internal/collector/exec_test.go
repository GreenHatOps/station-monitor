package collector

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// MockRunner returns predefined outputs for command invocations.
type MockRunner struct {
	Responses map[string]MockResponse
}

type MockResponse struct {
	Output []byte
	Err    error
}

func (m *MockRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	key := name
	for _, a := range args {
		key += " " + a
	}
	if r, ok := m.Responses[key]; ok {
		// Respect context cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		return r.Output, r.Err
	}
	return nil, fmt.Errorf("mock: no response for %q", key)
}

func TestExecWithTimeout_Success(t *testing.T) {
	runner := &MockRunner{
		Responses: map[string]MockResponse{
			"echo hello": {Output: []byte("hello\n")},
		},
	}
	out, err := ExecWithTimeout(runner, 5*time.Second, "echo", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "hello\n" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestExecWithTimeout_CommandNotFound(t *testing.T) {
	runner := &MockRunner{
		Responses: map[string]MockResponse{
			"nosuchcmd": {Err: fmt.Errorf("exec: not found")},
		},
	}
	_, err := ExecWithTimeout(runner, 5*time.Second, "nosuchcmd")
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestExecWithTimeout_Timeout(t *testing.T) {
	// SlowRunner simulates a command that blocks until context cancels
	slow := &slowRunner{}
	_, err := ExecWithTimeout(slow, 50*time.Millisecond, "slow")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

type slowRunner struct{}

func (s *slowRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
