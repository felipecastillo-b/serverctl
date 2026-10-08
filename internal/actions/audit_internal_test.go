package actions

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAuditFile covers the §7 audit sink location: $XDG_STATE_HOME wins,
// an empty value falls back to ~/.local/state, and neither variable nor
// a home directory is an explicit error rather than a guessed path.
func TestAuditFile(t *testing.T) {
	t.Run("XDG_STATE_HOME wins when set", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "/custom/state")
		got, err := auditFile()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := filepath.Join("/custom/state", "serverctl", "audit.log"); got != want {
			t.Errorf("auditFile = %q, want %q", got, want)
		}
	})

	t.Run("empty XDG falls back to ~/.local/state", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "/home/tester")
		got, err := auditFile()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := filepath.Join("/home/tester", ".local", "state", "serverctl", "audit.log"); got != want {
			t.Errorf("auditFile = %q, want %q", got, want)
		}
	})

	t.Run("no state dir and no home is an error", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "")
		if _, err := auditFile(); err == nil {
			t.Fatal("expected error when neither XDG_STATE_HOME nor HOME resolves")
		}
	})
}

// TestAuditOutcomeFold pins the "audit failure never vanishes" policy of
// auditOutcome: the action error survives unwrapped-in-spirit (errors.Is
// keeps working) while an audit write failure is appended, and even a
// successful action fails when its audit line cannot be written.
func TestAuditOutcomeFold(t *testing.T) {
	t.Run("success records executed with empty detail", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", dir)

		if err := auditOutcome("service.start", "a.service", nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		entry := readLastAuditEntry(t, filepath.Join(dir, "serverctl", "audit.log"))
		if entry.Action != "service.start" || entry.Target != "a.service" {
			t.Errorf("entry = %s/%s, want service.start/a.service", entry.Action, entry.Target)
		}
		if entry.Result != "executed" || entry.Detail != "" {
			t.Errorf("entry = %s/%q, want executed with empty detail", entry.Result, entry.Detail)
		}
	})

	t.Run("action error is returned and recorded as failed", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", dir)
		actionErr := errors.New("boom: forbidden")

		got := auditOutcome("service.stop", "b.service", actionErr)
		if !errors.Is(got, actionErr) {
			t.Errorf("folded error %v must keep wrapping the action error", got)
		}
		if !strings.Contains(got.Error(), "boom: forbidden") {
			t.Errorf("folded error %q lost the action detail", got)
		}

		entry := readLastAuditEntry(t, filepath.Join(dir, "serverctl", "audit.log"))
		if entry.Result != "failed" || !strings.Contains(entry.Detail, "boom: forbidden") {
			t.Errorf("entry = %s/%q, want failed with the action error as detail", entry.Result, entry.Detail)
		}
	})

	t.Run("audit write failure is folded into the action error", func(t *testing.T) {
		dir := t.TempDir()
		breakAuditLog(t, dir)
		t.Setenv("XDG_STATE_HOME", dir)
		actionErr := errors.New("actions: stop unit \"c.service\": no such unit")

		got := auditOutcome("service.stop", "c.service", actionErr)
		if !errors.Is(got, actionErr) {
			t.Errorf("folded error %v must keep wrapping the action error", got)
		}
		if !strings.Contains(got.Error(), "audit log write failed") {
			t.Errorf("folded error %q must mention the audit write failure", got)
		}
	})

	t.Run("successful action still fails when the audit write fails", func(t *testing.T) {
		dir := t.TempDir()
		breakAuditLog(t, dir)
		t.Setenv("XDG_STATE_HOME", dir)

		got := auditOutcome("service.restart", "d.service", nil)
		if got == nil {
			t.Fatal("an unauditable mutation must surface as an error")
		}
		if !strings.Contains(got.Error(), "audit log write failed") {
			t.Errorf("error %q must mention the audit write failure", got)
		}
	})
}

// readLastAuditEntry returns the final JSON line of the audit log.
func readLastAuditEntry(t *testing.T, path string) AuditEntry {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 {
		t.Fatal("audit log is empty")
	}
	var entry AuditEntry
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatalf("decode last audit line %q: %v", lines[len(lines)-1], err)
	}
	return entry
}

// breakAuditLog makes the audit log un-writable by occupying the file
// path with a directory.
func breakAuditLog(t *testing.T, xdgDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(xdgDir, "serverctl", "audit.log"), 0o700); err != nil {
		t.Fatalf("break audit log: %v", err)
	}
}
