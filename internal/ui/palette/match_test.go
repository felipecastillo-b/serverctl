package palette

import "testing"

// catalogTitles mirrors the module titles in screens catalog order; the
// matcher is pure and must not depend on the screens package.
var catalogTitles = []string{
	"Dashboard", "Processes", "Services", "Logs",
	"Storage", "Network", "Packages", "Users", "Containers",
}

func TestScore(t *testing.T) {
	tests := []struct {
		name      string
		pattern   string
		text      string
		wantScore int
		wantMatch bool
	}{
		{"empty pattern matches any text", "", "dashboard", 0, true},
		{"contiguous prefix", "da", "dashboard", 8, true},
		{"case-insensitive match", "PRO", "processes", 11, true},
		{"scattered subsequence", "ps", "processes", 6, true},
		{"contiguous run in the middle", "ss", "processes", 4, true},
		{"missing rune breaks the match", "prz", "processes", 0, false},
		{"pattern longer than text", "processesx", "logs", 0, false},
		{"no shared runes", "z", "dashboard", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, ok := Score(tt.pattern, tt.text)
			if ok != tt.wantMatch {
				t.Fatalf("Score(%q, %q) matched = %v, want %v",
					tt.pattern, tt.text, ok, tt.wantMatch)
			}
			if score != tt.wantScore {
				t.Errorf("Score(%q, %q) = %d, want %d",
					tt.pattern, tt.text, score, tt.wantScore)
			}
		})
	}
}

func TestScorePrefersPrefixAndContiguity(t *testing.T) {
	// "abc" prefixes "abcxyz": 1 per rune, +4 for the prefix, +2 per
	// contiguous continuation.
	prefix, ok := Score("abc", "abcxyz")
	if !ok {
		t.Fatal("prefix must match")
	}
	// The same runes scattered through "xaybzc" lose both bonuses.
	scattered, ok := Score("abc", "xaybzc")
	if !ok {
		t.Fatal("scattered subsequence must match")
	}
	if prefix <= scattered {
		t.Errorf("prefix match must outscore scattered: prefix=%d scattered=%d",
			prefix, scattered)
	}
}

func TestSearchRanking(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []int
	}{
		{"empty pattern keeps full catalog order", "", []int{0, 1, 2, 3, 4, 5, 6, 7, 8}},
		// "s" prefixes Services and Storage (score 5), matches six
		// more titles with score 1 in catalog order, and drops Network.
		{"single rune ranks prefixes first", "s", []int{2, 4, 0, 1, 3, 6, 7, 8}},
		{"unique fragment matches one module", "proc", []int{1}},
		{"unmatched pattern returns no results", "zzz", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Search(tt.pattern, catalogTitles)
			if len(got) != len(tt.want) {
				t.Fatalf("Search(%q) = %v, want %v", tt.pattern, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Search(%q)[%d] = %d, want %d (full: %v)",
						tt.pattern, i, got[i], tt.want[i], got)
				}
			}
		})
	}
}

func TestSearchRanksByScoreOverCatalogOrder(t *testing.T) {
	// "net" prefixes "network" (prefix + contiguity) while "xnet" only
	// contains it contiguously in the middle, so the result order
	// inverts the catalog order.
	got := Search("net", []string{"xnet", "network"})
	want := []int{1, 0}
	if len(got) != len(want) {
		t.Fatalf("Search(\"net\") = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Search(\"net\")[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}
