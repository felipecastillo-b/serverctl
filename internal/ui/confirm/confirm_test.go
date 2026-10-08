package confirm

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestUpdateKeyDecisions(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyMsg
		want Decision
	}{
		{"y confirms", key("y"), Confirmed},
		{"Y confirms", key("Y"), Confirmed},
		{"n denies", key("n"), Denied},
		{"N denies", key("N"), Denied},
		{"esc denies", tea.KeyMsg{Type: tea.KeyEsc}, Denied},
		{"other keys stay pending", key("x"), Pending},
		{"q stays pending: the modal swallows quit", key("q"), Pending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New("Send TERM to pid 1234?", theme.Dark())
			m.UpdateKey(tt.msg)
			if m.Decision() != tt.want {
				t.Errorf("Decision = %v, want %v", m.Decision(), tt.want)
			}
		})
	}
}

func TestResetRearms(t *testing.T) {
	m := New("first?", theme.Dark())
	m.UpdateKey(key("y"))
	if m.Decision() != Confirmed {
		t.Fatalf("expected confirmation before reset")
	}
	m.Reset("second?")
	if m.Decision() != Pending {
		t.Errorf("reset must clear the decision, got %v", m.Decision())
	}
	if !strings.Contains(m.View(60, 20), "second?") {
		t.Errorf("reset must render the new question")
	}
}

func TestMouseButtons(t *testing.T) {
	tests := []struct {
		name string
		want Decision
	}{
		{"yes button", Confirmed},
		{"no button", Denied},
	}

	// Both cases are driven through the same geometry View produces, so
	// the test verifies the render/hit-test contract instead of copying
	// layout math.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New("Confirm?", theme.Dark())
			m.View(60, 20)
			yes, no := m.buttonRects()
			target := yes
			if tt.want == Denied {
				target = no
			}
			x, y := (target.x0+target.x1)/2, target.y0
			m.UpdateMouse(tea.MouseMsg(tea.MouseEvent{
				Action: tea.MouseActionPress,
				Button: tea.MouseButtonLeft,
				X:      x, Y: y,
			}))
			if m.Decision() != tt.want {
				t.Errorf("click on %s center = %v, want %v", tt.name, m.Decision(), tt.want)
			}
		})
	}
}

func TestMouseOutsideButtonsStaysPending(t *testing.T) {
	m := New("Confirm?", theme.Dark())
	m.View(60, 20)
	m.UpdateMouse(tea.MouseMsg(tea.MouseEvent{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      1, Y: 1,
	}))
	if m.Decision() != Pending {
		t.Errorf("click outside buttons = %v, want Pending", m.Decision())
	}
}

func TestViewContainsQuestion(t *testing.T) {
	m := New("Send KILL to pid 42?", theme.Dark())
	if out := m.View(60, 20); !strings.Contains(out, "Send KILL to pid 42?") {
		t.Errorf("modal view lost the question:\n%s", out)
	}
}
