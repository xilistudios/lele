package tui

import "strings"

// mergeAdjacentSGR collapses the redundant SGR churn that glamour/chroma emit
// when rendering syntax-highlighted markdown.
//
// Observed pattern in rendered code lines (one triple per syntax token, often
// a single space):
//
//	OPEN(38;5;252) " " RESET  OPEN(38;5;252) " " RESET  OPEN(38;5;252) " " RESET …
//
// A 200x50 frame of a realistic conversation measured 131,504 bytes of which
// 92.2% were escape sequences (7,461 SGR / 2,487 resets for ~202 visible bytes
// per line). Applying this merge at cache-build time (per-message render cache
// in buildRenderedHistoryLines, streaming/thinking line caches in
// getRenderedStream/getRenderedThinking, and the viewport overlay) measured
// −82..88% frame bytes, −95% SGR sequences and 0 cell-level diffs on a
// terminal-emulator grid, which translates into a 1.9x–4.2x faster steady-state
// View() because every downstream stage (paintFrame, reapplyBackground,
// lipgloss.Place, AppContainer) is O(ANSI bytes).
//
// Three rewrites, all byte-level and attribute-preserving. SGR is cumulative
// in a terminal, so the pass tracks the set of OPENs accumulated since the
// last effective RESET ("open" below) and only drops a sequence when the
// resulting attribute state is provably identical:
//  1. RESET followed by the identical OPEN → drop both, but ONLY when that
//     OPEN is the single one in effect (an exact restore of the state the
//     RESET would have destroyed; with stacked opens such as BOLD+FG the
//     RESET is meaningful and is kept).
//  2. OPEN immediately followed by RESET with no text between → drop the pair
//     (the OPEN paints nothing and its RESET then has nothing left to clear).
//     When another OPEN is still in effect the RESET terminates that outer
//     run and is kept.
//  3. Consecutive identical OPENs with no text between → keep one.
//
// A RESET that is NOT part of one of these pairs is never dropped, even when
// no OPEN is in effect within the line. Two independent reasons:
//   - this pass runs per line, but inside a frame the lines are concatenated
//     and lipgloss/reapplyBackground leave SGR state (bg/fg) inherited across
//     line breaks, so the line does not start in a clean state: such a RESET
//     is observable — it wipes the inherited attributes, and keeping it is
//     what makes the merge cell-equivalent.
//   - reapplyBackground (style.go) is a stack machine that pops one run per
//     RESET, i.e. it relies on every RESET having a matching OPEN. Removing a
//     lone RESET is an under-pop that leaks the container's fg onto the
//     padding that follows; removing an OPEN while keeping its RESET is the
//     mirror over-pop that loses it. Rule 2 therefore always drops the pair.
//
// It ONLY rewrites CSI SGR sequences (ESC '[' + [0-9;]* + 'm'); any other
// sequence (cursor movement, erase-line, OSC hyperlinks, …) is treated as
// literal text and passes through untouched. Lines without escapes return
// immediately (near-zero cost).
func mergeAdjacentSGR(line string) string {
	if !strings.Contains(line, "\x1b[") {
		return line
	}
	const reset = "\x1b[0m"

	// Tokenize into (seq | text) segments first: the rewrites need lookahead
	// across segment boundaries, which a single streaming pass makes awkward.
	type seg struct {
		seq  string // non-empty for an SGR sequence
		text string // non-empty for literal bytes
	}
	var segs []seg
	i, n := 0, len(line)
	textStart := 0
	for i < n {
		if line[i] == 0x1b && i+1 < n && line[i+1] == '[' {
			k := i + 2
			for k < n && (line[k] == ';' || (line[k] >= '0' && line[k] <= '9')) {
				k++
			}
			if k < n && line[k] == 'm' {
				if i > textStart {
					segs = append(segs, seg{text: line[textStart:i]})
				}
				segs = append(segs, seg{seq: line[i : k+1]})
				i = k + 1
				textStart = i
				continue
			}
		}
		i++
	}
	if textStart < n {
		segs = append(segs, seg{text: line[textStart:]})
	}

	// Mark segments to drop. The pass tracks the OPENs accumulated since the
	// last effective RESET ("active" below) because SGR is cumulative in a
	// terminal: a sequence may only be removed when the attribute state seen
	// by every following character is provably unchanged.
	drop := make([]bool, len(segs))
	active := make([]string, 0, 4)
	for idx := 0; idx < len(segs); idx++ {
		sg := segs[idx]
		if sg.seq == "" {
			continue
		}
		if sg.seq == reset {
			// The OPEN immediately after this RESET, if any. Text in between
			// means the RESET already painted characters and cannot pair; a
			// non-SGR escape (cursor move, erase, OSC) is tokenized as text,
			// so it also blocks pairing when it sits BETWEEN the RESET and
			// the OPEN (conservative: the RESET survives). An escape BEFORE
			// the RESET does not block pairing — CSI erases/cursor moves do
			// not alter SGR state, so dropping the RESET+OPEN pair leaves
			// every following character with the same effective attributes.
			next := ""
			if idx+1 < len(segs) && segs[idx+1].seq != "" && segs[idx+1].seq != reset {
				next = segs[idx+1].seq
			}
			// Rule 1: RESET + the identical sole OPEN restores exactly the
			// state the RESET destroys → drop both, run continues.
			if next != "" && len(active) == 1 && active[0] == next {
				drop[idx] = true
				drop[idx+1] = true
				idx++
				continue
			}
			// Meaningful RESET (or one clearing state inherited from the
			// previous line): keep it and clear the effective state.
			active = active[:0]
			continue
		}
		// sg.seq is an OPEN.
		// Rule 2: OPEN immediately followed by RESET styles no character →
		// drop the OPEN. The RESET clears the WHOLE terminal state, so it is
		// kept unconditionally (it may be terminating an outer run or wiping
		// state inherited from the previous line); the effective state resets
		// to none (skip it: already decided here).
		if idx+1 < len(segs) && segs[idx+1].seq == reset {
			drop[idx] = true
			if len(active) == 0 {
				drop[idx+1] = true
			}
			active = active[:0]
			idx++
			continue
		}
		// Rule 3: OPEN immediately followed by an identical OPEN → keep one.
		if idx+1 < len(segs) && segs[idx+1].seq == sg.seq {
			drop[idx] = true
			continue
		}
		// OPEN kept: it joins the effective attribute state.
		if !containsSeq(active, sg.seq) {
			active = append(active, sg.seq)
		}
	}

	var sb strings.Builder
	sb.Grow(len(line))
	for idx, sg := range segs {
		if drop[idx] {
			continue
		}
		if sg.seq != "" {
			sb.WriteString(sg.seq)
		} else {
			sb.WriteString(sg.text)
		}
	}
	return sb.String()
}

// containsSeq reports whether seq is already in the effective-open set. The
// sets are tiny (a handful of stacked attributes per line), so a linear scan
// beats a map allocation.
func containsSeq(set []string, seq string) bool {
	for _, s := range set {
		if s == seq {
			return true
		}
	}
	return false
}

// mergeLines applies mergeAdjacentSGR to every line in place and returns the
// same slice. Used at cache-build sites so the stored lines are already
// collapsed and no per-frame cost is paid.
func mergeLines(lines []string) []string {
	for i, l := range lines {
		lines[i] = mergeAdjacentSGR(l)
	}
	return lines
}
