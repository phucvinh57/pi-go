package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Output limits shared by read and bash. Whichever is hit first wins.
const (
	MaxLines = 2000
	MaxBytes = 50 * 1024
)

// Truncation reports how a text was cut to fit the limits.
type Truncation struct {
	Content     string `json:"content"`
	Truncated   bool   `json:"truncated"`
	By          string `json:"truncatedBy,omitempty"` // "lines" or "bytes"
	TotalLines  int    `json:"totalLines"`
	TotalBytes  int    `json:"totalBytes"`
	OutputLines int    `json:"outputLines"`
	OutputBytes int    `json:"outputBytes"`
	// LastLinePartial: tail truncation kept only the end of the last line,
	// because that single line exceeded the byte limit.
	LastLinePartial bool `json:"lastLinePartial,omitempty"`
	// FirstLineExceedsLimit: head truncation kept nothing, because the first
	// line alone exceeded the byte limit.
	FirstLineExceedsLimit bool `json:"firstLineExceedsLimit,omitempty"`
}

// splitLines splits text into lines for counting: a trailing newline does not
// start another line, and empty text has none.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if strings.HasSuffix(s, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func formatSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}

// truncateHead keeps the start of s, whole lines only. It suits file reads,
// where the model continues from where the output stopped.
func truncateHead(s string, maxLines, maxBytes int) Truncation {
	lines := splitLines(s)
	t := Truncation{TotalLines: len(lines), TotalBytes: len(s)}
	if len(lines) <= maxLines && len(s) <= maxBytes {
		t.Content, t.OutputLines, t.OutputBytes = s, len(lines), len(s)
		return t
	}

	t.Truncated, t.By = true, "lines"
	if len(lines[0]) > maxBytes {
		t.By, t.FirstLineExceedsLimit = "bytes", true
		return t
	}

	var kept []string
	size := 0
	for i := 0; i < len(lines) && i < maxLines; i++ {
		n := len(lines[i])
		if i > 0 {
			n++ // the newline
		}
		if size+n > maxBytes {
			t.By = "bytes"
			break
		}
		kept = append(kept, lines[i])
		size += n
	}
	if len(kept) >= maxLines && size <= maxBytes {
		t.By = "lines"
	}
	t.Content = strings.Join(kept, "\n")
	t.OutputLines, t.OutputBytes = len(kept), len(t.Content)
	return t
}

// truncateTail keeps the end of s, whole lines unless a single last line is
// too long. It suits command output, where the end holds the error.
func truncateTail(s string, maxLines, maxBytes int) Truncation {
	lines := splitLines(s)
	t := Truncation{TotalLines: len(lines), TotalBytes: len(s)}
	if len(lines) <= maxLines && len(s) <= maxBytes {
		t.Content, t.OutputLines, t.OutputBytes = s, len(lines), len(s)
		return t
	}

	t.Truncated, t.By = true, "lines"
	var kept []string // reversed
	size := 0
	for i := len(lines) - 1; i >= 0 && len(kept) < maxLines; i-- {
		n := len(lines[i])
		if len(kept) > 0 {
			n++
		}
		if size+n > maxBytes {
			t.By = "bytes"
			if len(kept) == 0 {
				kept = append(kept, tailBytes(lines[i], maxBytes))
				size = len(kept[0])
				t.LastLinePartial = true
			}
			break
		}
		kept = append(kept, lines[i])
		size += n
	}
	if len(kept) >= maxLines && size <= maxBytes {
		t.By = "lines"
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	t.Content = strings.Join(kept, "\n")
	t.OutputLines, t.OutputBytes = len(kept), len(t.Content)
	return t
}

// tailBytes returns the last max bytes of s without splitting a UTF-8 rune.
func tailBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	start := len(s) - max
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
