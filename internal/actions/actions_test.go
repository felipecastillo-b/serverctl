package actions_test

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/felipecastillo-b/serverctl/internal/actions"
)

// TestActorSignalValidation covers only inputs that must be rejected
// BEFORE any syscall: these tests never signal a real process.
func TestActorSignalValidation(t *testing.T) {
	tests := []struct {
		name    string
		pid     int
		sig     actions.ProcessSignal
		wantErr string
	}{
		{"zero pid", 0, actions.SignalTerm, "invalid pid"},
		{"negative pid", -1, actions.SignalKill, "invalid pid"},
		{"large negative pid", -999, actions.SignalHup, "invalid pid"},
		{"zero signal", 1234, actions.ProcessSignal(0), "whitelist"},
		{"unknown signal", 1234, actions.ProcessSignal(99), "whitelist"},
		{"negative signal", 1234, actions.ProcessSignal(-1), "whitelist"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var actor actions.Actor
			err := actor.Signal(tt.pid, tt.sig)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q should mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestActorSignalReachesKernel proves every whitelisted signal passes
// validation and attempts delivery: pid 4194303 does not exist, so the
// kernel answers ESRCH (a validation failure would be a different error).
func TestActorSignalReachesKernel(t *testing.T) {
	signals := map[actions.ProcessSignal]string{
		actions.SignalTerm: "TERM",
		actions.SignalKill: "KILL",
		actions.SignalHup:  "HUP",
	}

	for sig, name := range signals {
		t.Run(name, func(t *testing.T) {
			var actor actions.Actor
			err := actor.Signal(4194303, sig)
			if err == nil {
				t.Fatalf("expected ESRCH for nonexistent pid, got nil")
			}
			if !errors.Is(err, syscall.ESRCH) {
				t.Errorf("error %v should wrap ESRCH (validation passed, kernel rejected)", err)
			}
		})
	}
}

// TestActorSignalTerminatesChild is the only test that signals a real
// process: a sleep child spawned by the test itself.
func TestActorSignalTerminatesChild(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: signals a real child process")
	}
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep binary not available")
	}

	cmd := exec.Command(sleepPath, "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	var actor actions.Actor
	if err := actor.Signal(cmd.Process.Pid, actions.SignalTerm); err != nil {
		t.Fatalf("signal child: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("child exited cleanly; TERM should have killed it")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit within 5s after TERM")
	}
}
