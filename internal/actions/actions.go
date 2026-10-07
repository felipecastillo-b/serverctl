// Package actions is the ONLY layer of serverctl allowed to mutate the
// system (ARCHITECTURE.md §4, §7). There is no generic command runner:
// every mutation is a whitelisted, validated operation with fixed
// semantics, delivered without a shell.
package actions

import (
	"fmt"
	"syscall"
)

// ProcessSignal is one of the signals serverctl may deliver to a process.
// The set is closed on purpose: anything outside the whitelist below can
// never be sent, no matter what the UI or a caller asks for.
type ProcessSignal int

const (
	// SignalTerm asks the process to exit gracefully (SIGTERM).
	SignalTerm ProcessSignal = iota + 1
	// SignalKill forces immediate termination (SIGKILL).
	SignalKill
	// SignalHup asks the process to hang up, typically reloading (SIGHUP).
	SignalHup
)

// allowedSignals is the complete whitelist of §7. Adding an entry here is
// a deliberate, reviewed security decision.
var allowedSignals = map[ProcessSignal]syscall.Signal{
	SignalTerm: syscall.SIGTERM,
	SignalKill: syscall.SIGKILL,
	SignalHup:  syscall.SIGHUP,
}

// String renders the signal for errors and confirmation prompts.
func (s ProcessSignal) String() string {
	switch s {
	case SignalTerm:
		return "TERM"
	case SignalKill:
		return "KILL"
	case SignalHup:
		return "HUP"
	default:
		return fmt.Sprintf("ProcessSignal(%d)", int(s))
	}
}

// Actor executes whitelisted mutations. The zero value is ready to use;
// Actor is stateless so the security boundary lives entirely in the
// whitelist and the validation below.
type Actor struct{}

// Signal delivers sig to pid after validating both. Validation rejects
// non-positive pids and any signal outside the whitelist before any
// syscall is attempted. Delivery is kill(2): never a shell, never exec.
func (Actor) Signal(pid int, sig ProcessSignal) error {
	if pid <= 0 {
		return fmt.Errorf("actions: invalid pid %d: must be positive", pid)
	}
	target, ok := allowedSignals[sig]
	if !ok {
		return fmt.Errorf("actions: signal %s rejected: not in whitelist (TERM, KILL, HUP)", sig)
	}
	if err := syscall.Kill(pid, target); err != nil {
		return fmt.Errorf("actions: send %s to pid %d: %w", sig, pid, err)
	}
	return nil
}
