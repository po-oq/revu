// Package diff computes line-based unified diffs for thread edit history.
// It is dependency-free and guarded so pathological inputs fall back to
// full-text display instead of burning CPU/memory.
package diff

import "strings"

type Op string

const (
	OpContext Op = "ctx"
	OpAdd     Op = "add"
	OpDel     Op = "del"
)

type Line struct {
	Op   Op     `json:"op"`
	Text string `json:"text"`
}

type Hunk struct {
	OldStart int    `json:"oldStart"`
	NewStart int    `json:"newStart"`
	Lines    []Line `json:"lines"`
}

type Result struct {
	TooLarge bool
	Hunks    []Hunk
}

const (
	// maxLines caps each side after common prefix/suffix trimming.
	maxLines = 20000
	// maxEditDistance caps the Myers search. The backtrack trace stores
	// O(D^2) ints, so 2000 keeps the worst case around 64MB transient.
	// Larger edits fall back to full-text display (TooLarge).
	maxEditDistance = 2000
	contextLines    = 3
)

// Lines computes a line diff between oldText and newText. CRLF/CR are
// normalized to LF and a trailing-newline difference is ignored.
func Lines(oldText, newText string) Result {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	prefix := commonPrefix(oldLines, newLines)
	oldRest := oldLines[prefix:]
	newRest := newLines[prefix:]
	suffix := commonSuffix(oldRest, newRest)
	oldMid := oldRest[:len(oldRest)-suffix]
	newMid := newRest[:len(newRest)-suffix]

	if len(oldMid) == 0 && len(newMid) == 0 {
		return Result{}
	}
	if len(oldMid) > maxLines || len(newMid) > maxLines {
		return Result{TooLarge: true}
	}
	ops, ok := myersOps(oldMid, newMid)
	if !ok {
		return Result{TooLarge: true}
	}
	full := make([]Line, 0, prefix+len(ops)+suffix)
	for _, text := range oldLines[:prefix] {
		full = append(full, Line{Op: OpContext, Text: text})
	}
	full = append(full, ops...)
	for _, text := range oldRest[len(oldRest)-suffix:] {
		full = append(full, Line{Op: OpContext, Text: text})
	}
	return Result{Hunks: buildHunks(full)}
}

func splitLines(text string) []string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")
	if normalized == "" {
		return nil
	}
	return strings.Split(normalized, "\n")
}

func commonPrefix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func commonSuffix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

// myersOps returns the edit script between a and b, or ok=false when the
// edit distance exceeds maxEditDistance.
func myersOps(a, b []string) ([]Line, bool) {
	n, m := len(a), len(b)
	limit := n + m
	if limit > maxEditDistance {
		limit = maxEditDistance
	}
	offset := limit
	v := make([]int, 2*limit+2)
	var trace [][]int
	found := -1
	for d := 0; d <= limit && found < 0; d++ {
		snapshot := make([]int, len(v))
		copy(snapshot, v)
		trace = append(trace, snapshot)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				found = d
				break
			}
		}
	}
	if found < 0 {
		return nil, false
	}
	var rev []Line
	x, y := n, m
	for d := found; d > 0; d-- {
		vPrev := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && vPrev[offset+k-1] < vPrev[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vPrev[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			rev = append(rev, Line{Op: OpContext, Text: a[x]})
		}
		if x == prevX {
			y--
			rev = append(rev, Line{Op: OpAdd, Text: b[y]})
		} else {
			x--
			rev = append(rev, Line{Op: OpDel, Text: a[x]})
		}
	}
	for x > 0 && y > 0 {
		x--
		y--
		rev = append(rev, Line{Op: OpContext, Text: a[x]})
	}
	out := make([]Line, len(rev))
	for i, line := range rev {
		out[len(rev)-1-i] = line
	}
	return out, true
}

// buildHunks groups changed lines into hunks with up to contextLines of
// surrounding context, merging hunks whose context overlaps.
func buildHunks(lines []Line) []Hunk {
	keep := make([]bool, len(lines))
	hasChange := false
	for i, line := range lines {
		if line.Op == OpContext {
			continue
		}
		hasChange = true
		from := i - contextLines
		if from < 0 {
			from = 0
		}
		to := i + contextLines
		if to > len(lines)-1 {
			to = len(lines) - 1
		}
		for j := from; j <= to; j++ {
			keep[j] = true
		}
	}
	if !hasChange {
		return nil
	}
	var hunks []Hunk
	oldNo, newNo := 1, 1
	i := 0
	for i < len(lines) {
		if !keep[i] {
			oldNo++
			newNo++
			i++
			continue
		}
		hunk := Hunk{OldStart: oldNo, NewStart: newNo}
		for i < len(lines) && keep[i] {
			hunk.Lines = append(hunk.Lines, lines[i])
			if lines[i].Op != OpAdd {
				oldNo++
			}
			if lines[i].Op != OpDel {
				newNo++
			}
			i++
		}
		hunks = append(hunks, hunk)
	}
	return hunks
}
