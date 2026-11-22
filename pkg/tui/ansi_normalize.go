package tui

import "strings"

func mergeAdjacentSGR(line string) string {
	if !strings.Contains(line, "\x1b[") {
		return line
	}
	const reset = "\x1b[0m"

	type seg struct {
		seq  string
		text string
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

	drop := make([]bool, len(segs))
	active := make([]string, 0, 4)
	for idx := 0; idx < len(segs); idx++ {
		sg := segs[idx]
		if sg.seq == "" {
			continue
		}
		if sg.seq == reset {
			next := ""
			if idx+1 < len(segs) && segs[idx+1].seq != "" && segs[idx+1].seq != reset {
				next = segs[idx+1].seq
			}
			if next != "" && len(active) == 1 && active[0] == next {
				drop[idx] = true
				drop[idx+1] = true
				idx++
				continue
			}
			active = active[:0]
			continue
		}
		if idx+1 < len(segs) && segs[idx+1].seq == reset {
			drop[idx] = true
			if len(active) == 0 {
				drop[idx+1] = true
			}
			active = active[:0]
			idx++
			continue
		}
		if idx+1 < len(segs) && segs[idx+1].seq == sg.seq {
			drop[idx] = true
			continue
		}
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

func containsSeq(set []string, seq string) bool {
	for _, s := range set {
		if s == seq {
			return true
		}
	}
	return false
}

func mergeLines(lines []string) []string {
	for i, l := range lines {
		lines[i] = mergeAdjacentSGR(l)
	}
	return lines
}
