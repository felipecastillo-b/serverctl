// Package keys defines the configurable keybindings of serverctl.
// Every screen-specific map lives with its screen; this package owns the
// global map and the user override mechanism.
package keys

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
)

// Global holds the bindings available on every screen.
type Global struct {
	Quit       key.Binding
	Help       key.Binding
	NextScreen key.Binding
	PrevScreen key.Binding
	Up         key.Binding
	Down       key.Binding
	Select     key.Binding
	Back       key.Binding
}

// DefaultGlobal returns the built-in global bindings.
func DefaultGlobal() Global {
	return Global{
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		NextScreen: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next module")),
		PrevScreen: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev module")),
		Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Select:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select")),
		Back:       key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// Bindings returns the global bindings in a stable order, ready for the help
// overlay to render.
func (g Global) Bindings() []key.Binding {
	return []key.Binding{
		g.Quit, g.Help, g.NextScreen, g.PrevScreen, g.Up, g.Down, g.Select, g.Back,
	}
}

// slots maps a config action name to the binding it overrides. These are the
// exact names accepted in the config file's `keys` section.
var slots = map[string]func(*Global) *key.Binding{
	"quit":        func(g *Global) *key.Binding { return &g.Quit },
	"help":        func(g *Global) *key.Binding { return &g.Help },
	"next_screen": func(g *Global) *key.Binding { return &g.NextScreen },
	"prev_screen": func(g *Global) *key.Binding { return &g.PrevScreen },
	"up":          func(g *Global) *key.Binding { return &g.Up },
	"down":        func(g *Global) *key.Binding { return &g.Down },
	"select":      func(g *Global) *key.Binding { return &g.Select },
	"back":        func(g *Global) *key.Binding { return &g.Back },
}

// ApplyOverrides replaces binding keys with the user's configured strings.
// Each map entry is an action name (see slots) mapped to a single key string
// such as "x", "tab" or "ctrl+c". The binding's help description is kept.
func (g *Global) ApplyOverrides(overrides map[string]string) error {
	for action, keyStr := range overrides {
		slot, ok := slots[action]
		if !ok {
			return fmt.Errorf("unknown key action %q", action)
		}
		keyStr = strings.TrimSpace(keyStr)
		if keyStr == "" {
			return fmt.Errorf("empty key for action %q", action)
		}
		binding := slot(g)
		*binding = key.NewBinding(
			key.WithKeys(keyStr),
			key.WithHelp(keyStr, binding.Help().Desc),
		)
	}
	return nil
}
