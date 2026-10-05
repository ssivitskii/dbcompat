package postgres

import (
	"context"
	"strings"
	"testing"
)

type captureRunner struct{ calls [][]string }

func (c *captureRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	c.calls = append(c.calls, append([]string{name}, args...))
	return "id", nil
}

func TestDockerRunArgumentsAreBounded(t *testing.T) {
	got := strings.Join(append([]string{"docker"}, dockerRunArgs("postgres:17.6", "password", "run-id")...), " ")
	for _, want := range []string{"docker run -d --rm", "-p 127.0.0.1::5432", "--memory 512m", "--cpus 1", "--pids-limit 256", label + "="} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "-v ") || strings.Contains(got, "/var/run/docker.sock") {
		t.Fatalf("unsafe mount in %s", got)
	}
}

func TestCommandOutputIgnoresSuccessfulStderr(t *testing.T) {
	got, err := commandOutput([]byte("abcdef123456\n"), []byte("pull warning\n"), nil)
	if err != nil || got != "abcdef123456" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestParseContainerID(t *testing.T) {
	if got, err := parseContainerID("abcdef123456\n"); err != nil || got != "abcdef123456" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	for _, value := range []string{"short", "abcdef123456\nwarning", "abcdef12345z", strings.Repeat("a", 65)} {
		if _, err := parseContainerID(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestParseLoopbackPort(t *testing.T) {
	if got, err := parseLoopbackPort("127.0.0.1:54321\n"); err != nil || got != 54321 {
		t.Fatalf("got=%d err=%v", got, err)
	}
	for _, value := range []string{"0.0.0.0:1234", "127.0.0.1:0", "127.0.0.1:70000", "127.0.0.1:1234\n127.0.0.1:1235"} {
		if _, err := parseLoopbackPort(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

type contextRunner struct{ hadDeadline, canceled bool }

func (r *contextRunner) Run(ctx context.Context, _ string, _ ...string) (string, error) {
	_, r.hadDeadline = ctx.Deadline()
	r.canceled = ctx.Err() != nil
	return "", nil
}
func TestCloseUsesIndependentBoundedContext(t *testing.T) {
	r := &contextRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	i := &Instance{ID: "owned-id", runner: r}
	if err := i.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !r.hadDeadline || r.canceled {
		t.Fatalf("deadline=%v canceled=%v", r.hadDeadline, r.canceled)
	}
}
