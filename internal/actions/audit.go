package actions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AuditEntry is one line of the local audit trail that ARCHITECTURE.md
// §7 mandates for every action attempt. It is JSON so the log is both
// greppable and trivially parseable.
type AuditEntry struct {
	Time   string `json:"time"`   // RFC3339, UTC
	Action string `json:"action"` // e.g. "service.start"
	Target string `json:"target"` // e.g. "sshd.service"
	Result string `json:"result"` // "executed" or "failed"
	Detail string `json:"detail"` // error text; empty on success
}

// auditFile resolves the audit log location: $XDG_STATE_HOME/serverctl/
// audit.log, defaulting to ~/.local/state/serverctl/audit.log when
// XDG_STATE_HOME is unset or empty. Reading the environment on every
// call is also the injection point that keeps tests hermetic: they
// redirect the sink to t.TempDir() with t.Setenv instead of touching
// the real state directory. It fails when neither variable nor a home
// directory can be resolved — an unauditable path must not be guessed.
func auditFile() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("actions: resolve audit log directory: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "serverctl", "audit.log"), nil
}

// Record appends one entry to the local audit log, creating parent
// directories as needed. Screens call it for the attempt states the
// backend cannot see (denied by validation, confirmed by the modal);
// the unit lifecycle verbs call it internally for every executed or
// failed attempt. The path is resolved from the environment on each
// call — see auditFile for the default and the test injection point.
func Record(action, target, result, detail string) error {
	path, err := auditFile()
	if err != nil {
		return err
	}

	entry := AuditEntry{
		Time:   time.Now().UTC().Format(time.RFC3339),
		Action: action,
		Target: target,
		Result: result,
		Detail: detail,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("actions: encode audit entry: %w", err)
	}

	// 0o700/0o600: the audit trail records who changed what on the
	// machine; only the invoking user may read it.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("actions: create audit log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("actions: open audit log: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("actions: write audit log: %w", err)
	}
	return nil
}

// auditOutcome records one unit-lifecycle attempt and returns the error
// the caller should surface. Design choice (mandated by §7's "every
// attempt is logged"): an audit write failure never silently vanishes —
// it is folded into the returned error, and even an action that
// succeeded on the bus becomes an error when its audit line could not
// be written. serverctl treats an unauditable mutation as a failure;
// both wrapped errors stay inspectable via errors.Is/As.
func auditOutcome(action, target string, actionErr error) error {
	result, detail := "executed", ""
	if actionErr != nil {
		result, detail = "failed", actionErr.Error()
	}
	if err := Record(action, target, result, detail); err != nil {
		if actionErr != nil {
			return fmt.Errorf("%w (audit log write failed: %w)", actionErr, err)
		}
		return fmt.Errorf("actions: audit log write failed: %w", err)
	}
	return actionErr
}
