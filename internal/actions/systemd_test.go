package actions_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coreos/go-systemd/v22/dbus"

	"github.com/felipecastillo-b/serverctl/internal/actions"
)

// TestActorUnitNameValidation covers only names that must be rejected
// BEFORE any bus connection: these tests never reach systemd. The
// validation-specific error text proves no dial happened — a dial
// failure would read "connect to system bus" instead.
func TestActorUnitNameValidation(t *testing.T) {
	tests := []struct {
		name    string
		unit    string
		wantErr string
	}{
		{"empty name", "", "unit name is empty"},
		{"shell metacharacter", "sshd.service;reboot", "rejected"},
		{"command substitution", "evil$(id).service", "rejected"},
		{"backtick", "`reboot`.service", "rejected"},
		{"whitespace", "my service.service", "rejected"},
		{"path traversal", "../../etc/passwd", "rejected"},
		{"slash anywhere", "sshd/dev.service", "rejected"},
		{"leading dot", ".hidden.service", "rejected"},
		{"leading dash", "-evil.service", "rejected"},
		{"leading at", "@instance.service", "rejected"},
		{"leading separator colon", ":escaped.service", "rejected"},
		{"leading separator underscore", "_private.service", "rejected"},
		{"unicode", "serviça.service", "rejected"},
		{"newline injection", "sshd.service\nreboot", "rejected"},
		{"over 255 bytes", strings.Repeat("a", 256) + ".service", "over the 255-byte limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var actor actions.Actor
			err := actor.Start(tt.unit)
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q should mention %q", err, tt.wantErr)
			}
			// The same gate must hold for the other two verbs.
			if err := actor.Stop(tt.unit); !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Stop: error %q should mention %q", err, tt.wantErr)
			}
			if err := actor.Restart(tt.unit); !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Restart: error %q should mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestValidationDenialIsNotBackendAudited pins the audit boundary: a name
// rejected by validation never reaches the bus, so the backend writes no
// audit line. "Denied" attempts are logged by the screen that shows the
// refusal (ARCHITECTURE.md §7); the backend logs executed/failed only.
func TestValidationDenialIsNotBackendAudited(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	var actor actions.Actor
	if err := actor.Start("bogus..name; rm -rf /"); err == nil {
		t.Fatal("expected validation error")
	}

	if _, err := os.Stat(filepath.Join(dir, "serverctl", "audit.log")); !os.IsNotExist(err) {
		t.Errorf("validation denial must not create an audit log, stat err = %v", err)
	}
}

// TestActorLifecycleVerbsReachSystemBus is the live reachability probe:
// each verb is called on a well-shaped but nonexistent unit, so systemd
// must answer NoSuchUnit (or a PolicyKit denial) — either proves the
// call was validated, dialed and then wrapped with operation context.
// It also proves the audit wiring: every failed attempt lands in the log
// with the verb's own action name. Skipped cleanly when no system bus
// exists (CI's ubuntu-latest runners have one).
func TestActorLifecycleVerbsReachSystemBus(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: talks to the real system bus")
	}
	conn, err := dbus.NewSystemConnectionContext(context.Background())
	if err != nil {
		t.Skipf("no system bus available: %v", err)
	}
	conn.Close()

	verbs := []struct {
		name   string
		action string
		call   func(actions.Actor, string) error
	}{
		{"start", "service.start", func(a actions.Actor, unit string) error { return a.Start(unit) }},
		{"stop", "service.stop", func(a actions.Actor, unit string) error { return a.Stop(unit) }},
		{"restart", "service.restart", func(a actions.Actor, unit string) error { return a.Restart(unit) }},
	}

	for _, verb := range verbs {
		t.Run(verb.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_STATE_HOME", dir)

			unit := "serverctl-m4a-nonexistent-probe.service"
			var actor actions.Actor
			err := verb.call(actor, unit)
			if err == nil {
				t.Fatalf("%s on a nonexistent unit must fail, got nil", verb.name)
			}
			if !strings.Contains(err.Error(), "actions: "+verb.name+" unit") {
				t.Errorf("error %q should carry %q operation context", err, verb.name)
			}
			if strings.Contains(err.Error(), "audit log write failed") {
				t.Errorf("audit log is writable in this test: %v", err)
			}

			raw, err := os.ReadFile(filepath.Join(dir, "serverctl", "audit.log"))
			if err != nil {
				t.Fatalf("every executed/failed attempt must be audited: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) != 1 {
				t.Fatalf("audit log has %d lines, want exactly 1", len(lines))
			}
			var entry actions.AuditEntry
			if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
				t.Fatalf("decode audit line: %v", err)
			}
			if entry.Action != verb.action {
				t.Errorf("audit action = %q, want %q", entry.Action, verb.action)
			}
			if entry.Target != unit {
				t.Errorf("audit target = %q, want %q", entry.Target, unit)
			}
			if entry.Result != "failed" || entry.Detail == "" {
				t.Errorf("audit result/detail = %q/%q, want failed with the error text", entry.Result, entry.Detail)
			}
		})
	}
}

// TestRecordAppendsJSONLines round-trips the exported audit API: two
// Record calls produce two JSON lines whose fields survive parsing.
func TestRecordAppendsJSONLines(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	if err := actions.Record("service.start", "nginx.service", "executed", ""); err != nil {
		t.Fatalf("record success: %v", err)
	}
	if err := actions.Record("service.stop", "nginx.service", "failed", "unit not loaded"); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "serverctl", "audit.log"))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("audit log has %d lines, want 2", len(lines))
	}

	want := []actions.AuditEntry{
		{Action: "service.start", Target: "nginx.service", Result: "executed", Detail: ""},
		{Action: "service.stop", Target: "nginx.service", Result: "failed", Detail: "unit not loaded"},
	}
	for i, line := range lines {
		var entry actions.AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode line %d: %v", i, err)
		}
		if entry.Time == "" {
			t.Errorf("line %d: time must not be empty", i)
		}
		if entry.Action != want[i].Action || entry.Target != want[i].Target ||
			entry.Result != want[i].Result || entry.Detail != want[i].Detail {
			t.Errorf("line %d = %+v, want %+v", i, entry, want[i])
		}
	}
}

// TestRecordDefaultPath checks the XDG fallback: with XDG_STATE_HOME
// unset and HOME pointed at a temp dir, entries land in
// ~/.local/state/serverctl/audit.log.
func TestRecordDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)

	if err := actions.Record("service.restart", "a.service", "executed", ""); err != nil {
		t.Fatalf("record: %v", err)
	}

	want := filepath.Join(home, ".local", "state", "serverctl", "audit.log")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("default audit path %s missing: %v", want, err)
	}
}

// TestRecordErrorPropagation proves a broken sink surfaces as an error:
// a state directory that is a regular file makes the parent-dir
// creation fail.
func TestRecordErrorPropagation(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("occupied"), 0o600); err != nil {
		t.Fatalf("create blocker file: %v", err)
	}
	t.Setenv("XDG_STATE_HOME", blocker)

	err := actions.Record("service.start", "a.service", "executed", "")
	if err == nil {
		t.Fatal("expected an audit write error")
	}
	if !strings.Contains(err.Error(), "audit") {
		t.Errorf("error %q should mention the audit log", err)
	}
}
