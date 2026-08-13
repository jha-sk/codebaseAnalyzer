// Package api serves the dashboard's HTTP surface. This file holds every
// derived number the UI shows - deliberately as pure functions over store
// rows, so they are unit-testable without a database and the SQL stays dumb.
package api

import (
	"sort"

	"codebase-analyser/internal/dashboard/store"
)

// severityWeight prices one finding of each severity against the 100-point
// health budget: a critical costs twelve times a low.
var severityWeight = map[string]int{"critical": 12, "high": 6, "medium": 2, "low": 1}

// categories is the spec's fixed set; CategoryCounts always returns all four
// so the UI can render a breakdown without nil checks.
var categories = [4]string{"correctness", "concurrency", "security", "operational"}

// trendAdjustment is what a run gains for improving on the previous one, or
// loses for regressing. Small on purpose: the score is dominated by what is
// wrong now, nudged by which way it is heading.
const trendAdjustment = 5

// HealthScore reduces a run to a single 0-100 signal: a severity-weighted
// penalty against a perfect 100, nudged by the direction of travel since the
// previous run. prev may be nil (first run on a branch), which scores as a
// flat trend rather than a penalty.
func HealthScore(cur, prev map[string]int) int {
	score := 100 - penalty(cur)
	if prev != nil {
		switch curPenalty, prevPenalty := penalty(cur), penalty(prev); {
		case curPenalty < prevPenalty:
			score += trendAdjustment
		case curPenalty > prevPenalty:
			score -= trendAdjustment
		}
	}
	return clamp(score, 0, 100)
}

// penalty prices a run's findings against the 100-point health budget. Both
// the score itself and the trend direction are computed from this, so
// "better than last time" can never disagree with "scores higher".
func penalty(counts map[string]int) int {
	total := 0
	for sev, weight := range severityWeight {
		total += counts[sev] * weight
	}
	return total
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Fingerprint identifies "the same problem" across runs. Line number is
// deliberately excluded: an unrelated edit higher up the file shifts every
// line below it, and reporting a whole file as newly broken because someone
// added an import would make the new/fixed counters useless.
// ponytail: two identical findings in one file collapse to one fingerprint,
// so a run that goes from three to one instance of the same rule reports no
// change. Add an occurrence index here if that granularity is ever wanted.
func Fingerprint(f store.Finding) string {
	return f.File + "\x00" + f.Tool + "\x00" + f.RuleID + "\x00" + f.Message
}

// Diff reports how many findings are new in cur and how many present in prev
// are gone. Either side may be nil.
func Diff(prev, cur []store.Finding) (added, fixed int) {
	prevSet := fingerprints(prev)
	curSet := fingerprints(cur)
	for fp := range curSet {
		if !prevSet[fp] {
			added++
		}
	}
	for fp := range prevSet {
		if !curSet[fp] {
			fixed++
		}
	}
	return added, fixed
}

func fingerprints(fs []store.Finding) map[string]bool {
	set := make(map[string]bool, len(fs))
	for _, f := range fs {
		set[Fingerprint(f)] = true
	}
	return set
}

// CategoryCounts answers "where does the risk concentrate", which the
// severity cards cannot show. Unknown categories are ignored rather than
// added as a fifth key, so the UI's four columns are guaranteed.
func CategoryCounts(fs []store.Finding) map[string]int {
	out := map[string]int{}
	for _, c := range categories {
		out[c] = 0
	}
	for _, f := range fs {
		if _, known := out[f.Category]; known {
			out[f.Category]++
		}
	}
	return out
}

type FileCount struct {
	File  string `json:"file"`
	Count int    `json:"count"`
}

// TopFiles ranks files by finding count, most first, ties broken
// alphabetically so the view does not reshuffle between identical renders.
func TopFiles(fs []store.Finding, n int) []FileCount {
	byFile := map[string]int{}
	for _, f := range fs {
		byFile[f.File]++
	}
	out := make([]FileCount, 0, len(byFile))
	for file, count := range byFile {
		out = append(out, FileCount{File: file, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].File < out[j].File
	})
	if n < 0 {
		n = 0
	}
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// PickDefaultBranch chooses which branch the page opens on. Nothing the CLI
// pushes tells us the repo's real git default branch, so this prefers the two
// names that almost always are one, and otherwise falls back to whichever
// branch pushed most recently (Branches is already ordered that way).
func PickDefaultBranch(branches []store.Branch) string {
	for _, preferred := range []string{"main", "master"} {
		for _, b := range branches {
			if b.Name == preferred {
				return preferred
			}
		}
	}
	if len(branches) > 0 {
		return branches[0].Name
	}
	return ""
}
