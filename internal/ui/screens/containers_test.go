package screens

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/felipecastillo-b/serverctl/internal/core"
	"github.com/felipecastillo-b/serverctl/internal/ui/theme"
)

// fakeContainerLister serves one static listing (or a per-call error)
// instead of reaching a Docker-compatible socket.
type fakeContainerLister struct {
	list  core.ContainerList
	err   error
	calls int
}

func (f *fakeContainerLister) Containers() (core.ContainerList, error) {
	f.calls++
	return f.list, f.err
}

// Compile-time proof that the fake satisfies the m10a port, exactly
// like collectors.System does.
var _ core.ContainerLister = (*fakeContainerLister)(nil)

// containerNow pins the screen clock so uptime cells are deterministic.
var containerNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// containerFixture mixes both running and exited containers, mixed-case
// names and distinct images so the sort keys and the filter have real
// rows to reorder and narrow.
var containerFixture = []core.Container{
	{ID: "id-web", Name: "web", Image: "nginx:1.27", State: "running", Created: containerNow.Add(-90 * time.Minute)},
	{ID: "id-db", Name: "db", Image: "postgres:16", State: "running", Created: containerNow.Add(-2 * time.Hour)},
	{ID: "id-cache", Name: "Cache", Image: "redis:7", State: "exited", Created: containerNow.Add(-48 * time.Hour)},
}

// containerRound is the fixture listing with its runtime provenance.
var containerRound = core.ContainerList{Runtime: "docker", Containers: containerFixture}

// driveContainers executes cmd and folds every message it produces into
// s, expanding tea.BatchMsg legs recursively. Callers hand it the
// collect leg only: executing a tea.Tick leg here would stall the test
// for a real cadence interval, so the timer stays runtime territory.
func driveContainers(t *testing.T, s *containers, cmd tea.Cmd) *containers {
	t.Helper()
	if cmd == nil {
		return s
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, leg := range batch {
			s = driveContainers(t, s, leg)
		}
		return s
	}
	updated, _ := s.Update(msg)
	out, ok := updated.(*containers)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	return out
}

// newContainersScreen builds the containers screen over the given fake,
// pins its clock and drives the first collection round synchronously.
// Init batches [collect, tick] and that batch is lazy — executing it
// only yields the legs — so the fixture runs the collect leg; the 10 s
// timer leg would stall the test.
func newContainersScreen(t *testing.T, lister *fakeContainerLister) *containers {
	t.Helper()
	s := newContainers(lister, theme.Dark(), defaultContainersInterval)
	s.now = func() time.Time { return containerNow }
	cmd := s.Init()
	if cmd == nil {
		t.Fatal("Init must collect and start the cadence")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("Init must batch collect and the cadence tick, got %T len %d", batch, len(batch))
	}
	s = driveContainers(t, s, batch[0])
	if !s.loaded || s.err != nil {
		t.Fatalf("fixture collection: loaded=%v err=%v", s.loaded, s.err)
	}
	return s
}

// containerNames lists the displayed rows in table order.
func containerNames(list []core.Container) []string {
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = c.Name
	}
	return out
}

// typeContainerQuery feeds every rune of text to the open filter input.
func typeContainerQuery(s *containers, text string) *containers {
	for _, r := range text {
		updated, _, _ := s.UpdateKey(keyMsg(string(r)))
		s = updated.(*containers)
	}
	return s
}

func TestContainersInitRendersListing(t *testing.T) {
	lister := &fakeContainerLister{list: containerRound}
	s := newContainersScreen(t, lister)
	if lister.calls != 1 {
		t.Fatalf("Init must collect exactly once, got %d", lister.calls)
	}

	view := s.View(100, 24)
	for _, want := range []string{
		"NAME", "STATE", "IMAGE", "UPTIME",
		"web", "nginx:1.27", "running",
		"db", "postgres:16",
		"Cache", "redis:7", "exited",
		"1h 30m",
		"3 containers · 2 running · docker · sort name ↑",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

// TestContainersCollectingState pins the pre-round footer: the screen
// is collecting, not empty and not failed.
func TestContainersCollectingState(t *testing.T) {
	s := newContainers(&fakeContainerLister{}, theme.Dark(), defaultContainersInterval)
	out := s.View(100, 24)
	if !strings.Contains(out, "collecting...") {
		t.Errorf("fresh screen must show collecting..., got:\n%s", out)
	}
	if strings.Contains(out, "0 containers") {
		t.Errorf("a screen before its first round must not claim a zero-count listing:\n%s", out)
	}
}

// TestContainersFirstRoundError pins the failed-first-round state: the
// error in the status line and no body rows, because there is no last
// good listing to keep.
func TestContainersFirstRoundError(t *testing.T) {
	s := newContainers(&fakeContainerLister{err: errors.New("no container socket")},
		theme.Dark(), defaultContainersInterval)
	updated, _ := s.Update(s.collect()())
	s = updated.(*containers)

	if s.loaded {
		t.Fatal("a failed first round must not mark the screen loaded")
	}
	if len(s.view) != 0 {
		t.Fatalf("a failed first round must not render rows: %v", containerNames(s.view))
	}
	out := s.View(100, 24)
	if !strings.Contains(out, "n/a (no container socket)") {
		t.Errorf("failed round must show its collection error:\n%s", out)
	}
	if strings.Contains(out, "web") {
		t.Errorf("failed first round must not render a listing:\n%s", out)
	}
}

// TestContainersStaleRoundKeepsLastGood pins §5's keeps-last-good
// state: a failed later round leaves the previous listing standing
// under the error plus the muted age of that listing, and the next good
// round clears both.
func TestContainersStaleRoundKeepsLastGood(t *testing.T) {
	s := newContainersScreen(t, &fakeContainerLister{list: containerRound})

	s.lister = &fakeContainerLister{err: errors.New("daemon gone")}
	updated, _ := s.Update(s.collect()())
	s = updated.(*containers)
	s.lastGood = time.Now().Add(-45 * time.Second)

	out := s.View(100, 24)
	for _, want := range []string{"n/a (daemon gone)", "stale 45s", "web", "Cache"} {
		if !strings.Contains(out, want) {
			t.Errorf("stale round must keep the listing under the error and age, missing %q:\n%s", want, out)
		}
	}
	if !s.loaded {
		t.Error("a failed later round must keep the screen loaded")
	}

	// Recovery: the good listing replaces the stale one and clears the
	// error and the age.
	s.lister = &fakeContainerLister{list: containerRound}
	updated, _ = s.Update(s.collect()())
	s = updated.(*containers)
	out = s.View(100, 24)
	if strings.Contains(out, "stale") || strings.Contains(out, "daemon gone") {
		t.Errorf("a recovered round must clear the error and the age:\n%s", out)
	}
}

// TestContainersEmptyListing pins that an empty successful round is a
// zero-row listing, never an error or a loading state.
func TestContainersEmptyListing(t *testing.T) {
	s := newContainersScreen(t, &fakeContainerLister{
		list: core.ContainerList{Runtime: "podman"},
	})
	out := s.View(100, 24)
	if !strings.Contains(out, "0 containers · 0 running · podman") {
		t.Errorf("empty round must show the zero-count summary:\n%s", out)
	}
	if strings.Contains(out, "collecting...") || strings.Contains(out, "n/a") {
		t.Errorf("empty round must render neither loading nor error:\n%s", out)
	}
}

func TestContainersSorting(t *testing.T) {
	s := newContainersScreen(t, &fakeContainerLister{list: containerRound})

	cases := []struct {
		name string
		key  string
		want []string
	}{
		{"name ascending is the default", "", []string{"Cache", "db", "web"}},
		{"n flips name descending", "n", []string{"web", "db", "Cache"}},
		{"n again returns name ascending", "n", []string{"Cache", "db", "web"}},
		{"s sorts by state ascending", "s", []string{"Cache", "web", "db"}},
		{"s again flips state descending", "s", []string{"web", "db", "Cache"}},
		{"i sorts by image ascending", "i", []string{"web", "db", "Cache"}},
		{"i again flips image descending", "i", []string{"Cache", "db", "web"}},
		{"u sorts by created ascending", "u", []string{"Cache", "db", "web"}},
		{"u again flips uptime descending", "u", []string{"web", "db", "Cache"}},
		{"n returns to name ascending", "n", []string{"Cache", "db", "web"}},
	}

	for idx, tc := range cases {
		if tc.key != "" {
			updated, _, handled := s.UpdateKey(keyMsg(tc.key))
			if !handled {
				t.Fatalf("sort key %q not handled", tc.key)
			}
			s = updated.(*containers)
		} else if idx > 0 {
			t.Fatal("table driven misuse: default case must be first")
		}
		if got := containerNames(s.view); !slices.Equal(got, tc.want) {
			t.Errorf("%s: order = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestContainersFilter(t *testing.T) {
	s := newContainersScreen(t, &fakeContainerLister{list: containerRound})

	if _, _, handled := s.UpdateKey(keyMsg("/")); !handled || !s.filtering {
		t.Fatal("filter mode did not open")
	}
	// "redis" matches an image only; the filter spans name and image.
	s = typeContainerQuery(s, "redis")
	if got := containerNames(s.view); !slices.Equal(got, []string{"Cache"}) {
		t.Fatalf("filter 'redis': %v", got)
	}
	view := s.View(100, 24)
	for _, want := range []string{`filter "redis"`, "1 containers · 0 running · docker"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}

	// esc closes the input but keeps the query applied.
	updated, _, handled := s.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc})
	s = updated.(*containers)
	if !handled || s.filtering {
		t.Fatal("esc must close filter mode")
	}
	if got := containerNames(s.view); !slices.Equal(got, []string{"Cache"}) {
		t.Fatalf("closed filter must keep rows applied: %v", got)
	}

	// A second esc clears the query, because there is one to clear.
	updated, _, handled = s.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc})
	s = updated.(*containers)
	if !handled {
		t.Fatal("esc must clear a non-empty filter")
	}
	if got := containerNames(s.view); !slices.Equal(got, []string{"Cache", "db", "web"}) {
		t.Fatalf("cleared filter must restore every row: %v", got)
	}
	if strings.Contains(s.View(100, 24), "filter ") {
		t.Error("status line must drop the cleared filter")
	}

	// The query is case-insensitive over the name too.
	s.UpdateKey(keyMsg("/"))
	s = typeContainerQuery(s, "CACHE")
	if got := containerNames(s.view); !slices.Equal(got, []string{"Cache"}) {
		t.Fatalf("filter 'CACHE': %v", got)
	}

	// A no-match query empties the view without an error.
	updated, _, _ = s.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc})
	s = updated.(*containers)
	updated, _, _ = s.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc})
	s = updated.(*containers)
	if _, _, handled := s.UpdateKey(keyMsg("/")); !handled {
		t.Fatal("filter mode did not reopen")
	}
	s = typeContainerQuery(s, "zzz")
	if len(s.view) != 0 {
		t.Fatalf("no-match filter must empty the view: %v", containerNames(s.view))
	}
	if out := s.View(100, 24); !strings.Contains(out, "0 containers") {
		t.Errorf("no-match filter must show a zero-count summary:\n%s", out)
	}
}

// TestContainersUptimeRendering pins the display-derived uptime cell:
// running rows show the age since Created, every other state (and a
// missing creation instant) shows an em dash.
func TestContainersUptimeRendering(t *testing.T) {
	lister := &fakeContainerLister{list: core.ContainerList{
		Runtime: "docker",
		Containers: []core.Container{
			{ID: "a", Name: "aged", Image: "img", State: "running", Created: containerNow.Add(-90 * time.Minute)},
			{ID: "b", Name: "dead", Image: "img", State: "exited", Created: containerNow.Add(-90 * time.Minute)},
			{ID: "c", Name: "unknown", Image: "img", State: "running"},
		},
	}}
	s := newContainersScreen(t, lister)

	got := map[string]string{}
	for _, row := range containerRowsFor(s.view, 100, containerNow) {
		got[row[0]] = row[3]
	}
	want := map[string]string{
		"aged":    "1h 30m",
		"dead":    "—",
		"unknown": "—",
	}
	for name, uptime := range want {
		if got[name] != uptime {
			t.Errorf("uptime for %q = %q, want %q", name, got[name], uptime)
		}
	}
	if out := s.View(100, 24); !strings.Contains(out, "1h 30m") {
		t.Errorf("view must render the running uptime:\n%s", out)
	}
}

// TestContainersTickRearmsOwnCadence pins the self-ticked chain: the
// shell's RefreshMsg is ignored, and the screen's own tick collects a
// fresh round and re-arms.
func TestContainersTickRearmsOwnCadence(t *testing.T) {
	lister := &fakeContainerLister{list: containerRound}
	s := newContainersScreen(t, lister)
	if lister.calls != 1 {
		t.Fatalf("fixture must have collected once, got %d", lister.calls)
	}

	updated, cmd := s.Update(RefreshMsg{})
	s = updated.(*containers)
	if cmd != nil {
		t.Error("RefreshMsg must not trigger a containers collection")
	}
	if lister.calls != 1 {
		t.Errorf("RefreshMsg must not re-collect: %d", lister.calls)
	}

	updated, cmd = s.Update(containersTickMsg{})
	s = updated.(*containers)
	if cmd == nil {
		t.Fatal("the tick must re-arm the cadence")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("the tick must re-arm collect and the next tick, got %T len %d", batch, len(batch))
	}
	// Only lister.calls matters here; the returned model is the same
	// pointer, so the leg can run without reassigning s.
	driveContainers(t, s, batch[0])
	if lister.calls != 2 {
		t.Errorf("the re-armed collect must fetch a fresh listing: %d", lister.calls)
	}
}

// TestContainersKeysFallThrough pins §6's per-key shadowing: sort and
// filter keys are claimed, everything else — and esc with no query to
// clear — passes to the shell.
func TestContainersKeysFallThrough(t *testing.T) {
	s := newContainersScreen(t, &fakeContainerLister{list: containerRound})

	if _, _, handled := s.UpdateKey(keyMsg("down")); !handled {
		t.Fatal("navigation keys must be claimed by the screen")
	}
	if s.table.Cursor() != 1 {
		t.Errorf("cursor = %d, want 1 after down", s.table.Cursor())
	}
	if _, _, handled := s.UpdateKey(keyMsg("up")); !handled {
		t.Error("up must be claimed by the screen")
	}
	if s.table.Cursor() != 0 {
		t.Errorf("cursor = %d, want 0 after up", s.table.Cursor())
	}

	for _, k := range []string{"q", "?", "tab", "x", "j", "k"} {
		if _, _, handled := s.UpdateKey(keyMsg(k)); handled {
			t.Errorf("key %q must fall through to the global keymap", k)
		}
	}
	if _, _, handled := s.UpdateKey(tea.KeyMsg{Type: tea.KeyEsc}); handled {
		t.Error("esc with no active filter must fall through to the shell")
	}
	if _, _, handled := s.UpdateMouse(tea.MouseMsg{}); handled {
		t.Error("mouse events must fall through to the shell")
	}
}

func TestContainersTitleAndHints(t *testing.T) {
	s := newContainers(&fakeContainerLister{}, theme.Dark(), defaultContainersInterval)
	if got := s.Title(); got != "Containers" {
		t.Errorf("Title() = %q, want Containers", got)
	}

	hints := s.Hints()
	got := make(map[string]string, len(hints))
	for _, b := range hints {
		help := b.Help()
		got[help.Key] = help.Desc
	}
	want := []string{"n", "s", "i", "u", "/", "esc"}
	if len(got) != len(want) {
		t.Fatalf("Hints() has %d entries, want %d: %v", len(got), len(want), got)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("Hints() missing key %q: %v", k, got)
		}
	}
}
