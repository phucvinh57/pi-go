package tools

import (
	"fmt"
	"strconv"
	"strings"
)

// maxLCSCells bounds the table of the line diff. A change bigger than this
// (say 2000 lines replaced by 2000 lines) is shown as a plain delete and add.
const maxLCSCells = 4_000_000

type diffOp struct {
	kind byte // ' ', '-' or '+'
	text string
}

// diffLines returns a line-level edit script from a to b.
func diffLines(a, b []string) []diffOp {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	am, bm := a[pre:len(a)-suf], b[pre:len(b)-suf]

	ops := make([]diffOp, 0, len(a)+len(b))
	for _, l := range a[:pre] {
		ops = append(ops, diffOp{' ', l})
	}
	ops = append(ops, diffMiddle(am, bm)...)
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

func diffMiddle(a, b []string) []diffOp {
	var ops []diffOp
	if len(a)*len(b) > maxLCSCells {
		for _, l := range a {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range b {
			ops = append(ops, diffOp{'+', l})
		}
		return ops
	}

	// lcs[i][j] is the longest common subsequence of a[i:] and b[j:].
	w := len(b) + 1
	lcs := make([]int32, (len(a)+1)*w)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else {
				lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case lcs[(i+1)*w+j] >= lcs[i*w+j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < len(b); j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// renderDiff formats the change from old to updated with line numbers, showing
// ctx unchanged lines around each change and "..." for the rest:
//
//	 12 unchanged
//	-13 removed
//	+13 added
//
// It also returns the line number, in the new text, of the first change (0 if
// there is none).
func renderDiff(old, updated string, ctx int) (string, int) {
	oldLines, newLines := strings.Split(old, "\n"), strings.Split(updated, "\n")
	ops := diffLines(oldLines, newLines)
	width := len(strconv.Itoa(max(len(oldLines), len(newLines))))

	// An unchanged line is shown when it is within ctx lines of a change.
	near := make([]bool, len(ops))
	last := -ctx - 1 // index of the most recent change
	for i, op := range ops {
		if op.kind != ' ' {
			last = i
			for k := max(0, i-ctx); k <= i; k++ {
				near[k] = true
			}
		} else if i-last <= ctx {
			near[i] = true
		}
	}

	var out []string
	oldNum, newNum, first := 1, 1, 0
	skipped := false
	for i, op := range ops {
		switch op.kind {
		case '+':
			if first == 0 {
				first = newNum
			}
			out = append(out, fmt.Sprintf("+%*d %s", width, newNum, op.text))
			newNum++
			skipped = false
		case '-':
			if first == 0 {
				first = newNum
			}
			out = append(out, fmt.Sprintf("-%*d %s", width, oldNum, op.text))
			oldNum++
			skipped = false
		default:
			if near[i] {
				out = append(out, fmt.Sprintf(" %*d %s", width, oldNum, op.text))
				skipped = false
			} else if !skipped {
				out = append(out, " "+strings.Repeat(" ", width)+" ...")
				skipped = true
			}
			oldNum++
			newNum++
		}
	}
	return strings.Join(out, "\n"), first
}
