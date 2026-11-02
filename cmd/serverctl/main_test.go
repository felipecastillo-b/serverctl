package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig writes body to a config.yaml under the test's temp directory
// and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestRunRejectsInvalidConfig covers every rejection path that returns
// before the TUI starts. A valid config is deliberately absent: run would
// launch Bubble Tea and block until the test harness kills it.
func TestRunRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown theme",
			body: "theme: nope\n",
			want: "theme must be",
		},
		{
			name: "refresh below one",
			body: "refresh_seconds: -1\n",
			want: "refresh_seconds must be at least 1",
		},
		{
			name: "interval below one",
			body: "intervals: {logs: 0}\n",
			want: "intervals.logs must be at least 1",
		},
		{
			name: "unknown global action",
			body: "keys: {nope: x}\n",
			want: "invalid keybinding override",
		},
		{
			name: "unknown screen",
			body: "screen_keys: {nope: {kill: x}}\n",
			want: "invalid screen keybinding override",
		},
		{
			name: "unknown screen action",
			body: "screen_keys: {processes: {nope: x}}\n",
			want: "invalid screen keybinding override",
		},
		{
			name: "empty screen key",
			body: "screen_keys: {processes: {kill: \"\"}}\n",
			want: "empty key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(writeConfig(t, tt.body))
			if err == nil {
				t.Fatalf("run with %s config returned nil error", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}
