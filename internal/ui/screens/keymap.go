package screens

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
)

// screenKeySlots maps a config screen name to the factory of its
// configurable action slots. Add a screen here when it adopts overrides.
var screenKeySlots = map[string]func() map[string]*key.Binding{
	"processes": func() map[string]*key.Binding {
		k := defaultProcessKeys()
		return k.slots()
	},
	"services": func() map[string]*key.Binding {
		k := defaultServicesKeys()
		return k.slots()
	},
	"logs": func() map[string]*key.Binding {
		k := defaultLogsKeys()
		return k.slots()
	},
	"containers": func() map[string]*key.Binding {
		k := defaultContainersKeys()
		return k.slots()
	},
}

// ValidateScreenKeys checks the screen-local keybinding overrides before
// the TUI starts: every screen and action name must exist and every key
// must be non-blank. Navigation keys (table and viewport movement) remain
// built-in for now and are not configurable through the screen_keys
// section; only the actions registered in screenKeySlots are.
func ValidateScreenKeys(overrides map[string]map[string]string) error {
	screens := slices.Sorted(maps.Keys(screenKeySlots))
	for _, screen := range slices.Sorted(maps.Keys(overrides)) {
		factory, ok := screenKeySlots[screen]
		if !ok {
			return fmt.Errorf("unknown screen %q must be one of: %s",
				screen, strings.Join(screens, ", "))
		}
		slots := factory()
		actions := slices.Sorted(maps.Keys(slots))
		for _, action := range slices.Sorted(maps.Keys(overrides[screen])) {
			if _, ok := slots[action]; !ok {
				return fmt.Errorf("unknown action %q for screen %q must be one of: %s",
					action, screen, strings.Join(actions, ", "))
			}
			if strings.TrimSpace(overrides[screen][action]) == "" {
				return fmt.Errorf("empty key for screen %q action %q", screen, action)
			}
		}
	}
	return nil
}
