package palette

import (
	"sort"
	"strings"
)

// Score reports whether pattern occurs in text as a case-insensitive
// subsequence, and how well it matches: higher is better. It is pure —
// no palette state influences a score.
//
// The shape is deliberately simple (no stdlib or third-party fuzzy):
//   - every matched pattern rune scores 1;
//   - a rune that immediately continues the previous match scores 2
//     extra, so unbroken runs beat scattered ones;
//   - a first rune matched at the very start of text scores 4 extra,
//     so prefixes beat infixes.
//
// An empty pattern matches any text with score 0. The scan is greedy
// left-to-right: it takes the earliest match for every rune instead of
// searching for the optimal alignment, which is plenty to rank nine
// module titles.
func Score(pattern, text string) (int, bool) {
	pat := []rune(strings.ToLower(pattern))
	txt := []rune(strings.ToLower(text))
	if len(pat) == 0 {
		return 0, true
	}

	score := 0
	prev := -2 // index of the last matched rune; -2 = no match yet
	for _, r := range pat {
		start := prev + 1
		if start < 0 {
			start = 0
		}
		found := -1
		for j := start; j < len(txt); j++ {
			if txt[j] == r {
				found = j
				break
			}
		}
		if found < 0 {
			return 0, false
		}
		score++
		if found == prev+1 {
			score += 2
		}
		if prev == -2 && found == 0 {
			score += 4
		}
		prev = found
	}
	return score, true
}

// Search returns the indices of the titles matching pattern, best score
// first; ties keep catalog order (stable sort). An empty pattern
// matches every title.
func Search(pattern string, titles []string) []int {
	type candidate struct {
		index int
		score int
	}
	matched := make([]candidate, 0, len(titles))
	for i, title := range titles {
		if score, ok := Score(pattern, title); ok {
			matched = append(matched, candidate{index: i, score: score})
		}
	}
	sort.SliceStable(matched, func(a, b int) bool {
		return matched[a].score > matched[b].score
	})
	out := make([]int, len(matched))
	for i, c := range matched {
		out[i] = c.index
	}
	return out
}
