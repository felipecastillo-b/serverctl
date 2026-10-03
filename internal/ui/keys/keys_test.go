package keys_test

import (
	"testing"

	"github.com/felipecastillo-b/serverctl/internal/ui/keys"
)

func TestDefaultsHaveHelpForEveryBinding(t *testing.T) {
	g := keys.DefaultGlobal()

	for i, b := range g.Bindings() {
		if b.Help().Key == "" || b.Help().Desc == "" {
			t.Errorf("binding %d must have help key and description", i)
		}
	}
}

func TestApplyOverridesRebindsAndKeepsHelp(t *testing.T) {
	g := keys.DefaultGlobal()

	if err := g.ApplyOverrides(map[string]string{"quit": "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(g.Quit.Keys(), "x") {
		t.Errorf("quit keys = %v, want them to contain %q", g.Quit.Keys(), "x")
	}
	if contains(g.Quit.Keys(), "q") {
		t.Errorf("quit keys = %v, must no longer contain %q", g.Quit.Keys(), "q")
	}
	if g.Quit.Help().Desc != "quit" {
		t.Errorf("help description must survive the override, got %q", g.Quit.Help().Desc)
	}
}

func TestApplyOverridesErrors(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]string
	}{
		{"unknown action", map[string]string{"explode": "x"}},
		{"empty key", map[string]string{"quit": ""}},
		{"blank key", map[string]string{"quit": "   "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := keys.DefaultGlobal()
			if err := g.ApplyOverrides(tt.overrides); err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

// contains reports whether a key list holds the given key.
func contains(keyList []string, want string) bool {
	for _, k := range keyList {
		if k == want {
			return true
		}
	}
	return false
}
