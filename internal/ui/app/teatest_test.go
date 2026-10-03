package app

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/felipecastillo-b/serverctl/internal/config"
)

// TestShellBootsAndQuits is the interactive smoke test: the full TUI boots
// in a virtual terminal, renders its header, and exits on the quit key.
// It runs headless on Linux and is part of the regular test suite.
func TestShellBootsAndQuits(t *testing.T) {
	tm := teatest.NewTestModel(t, New(config.Default()),
		teatest.WithInitialTermSize(100, 30))

	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		return bytes.Contains(bts, []byte("serverctl"))
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}
