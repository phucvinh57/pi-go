package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

const utf8BOM = "\ufeff"

type edit struct{ cwd string }

// NewEdit returns the edit tool.
func NewEdit(cwd string) Tool { return edit{cwd} }

// EditDetails is Result.Details of a successful edit.
type EditDetails struct {
	Diff             string `json:"diff"`
	FirstChangedLine int    `json:"firstChangedLine"`
}

type replacement struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

func (edit) Spec() Spec {
	return Spec{
		Name: "edit",
		Description: "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, " +
			"non-overlapping region of the original file. If two changes affect the same block or nearby lines, " +
			"merge them into one edit instead of emitting overlapping edits. Do not include large unchanged " +
			"regions just to connect distant changes.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the file to edit (relative or absolute)"},
    "edits": {
      "type": "array",
      "description": "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead.",
      "items": {
        "type": "object",
        "properties": {
          "oldText": {"type": "string", "description": "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."},
          "newText": {"type": "string", "description": "Replacement text for this targeted edit."}
        },
        "required": ["oldText", "newText"]
      }
    }
  },
  "required": ["path", "edits"]
}`),
		Snippet: "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
		Guidelines: []string{
			"Use edit for precise changes (edits[].oldText must match exactly)",
			"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
			"Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
			"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
		},
	}
}

func (e edit) Execute(ctx context.Context, raw json.RawMessage, _ func(Result)) (Result, error) {
	var a struct {
		Path    string          `json:"path"`
		Edits   json.RawMessage `json:"edits"`
		OldText *string         `json:"oldText"` // legacy single-edit form
		NewText *string         `json:"newText"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return Result{}, err
	}
	if a.Path == "" {
		return Result{}, errors.New("path is required")
	}
	edits, err := parseEdits(a.Edits)
	if err != nil {
		return Result{}, err
	}
	if a.OldText != nil && a.NewText != nil {
		edits = append(edits, replacement{*a.OldText, *a.NewText})
	}
	if len(edits) == 0 {
		return Result{}, errors.New("edits must contain at least one replacement")
	}

	path := resolvePath(a.Path, e.cwd)
	return withFileLock(path, func() (Result, error) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return Result{}, fmt.Errorf("Could not edit file: %s. %w", a.Path, err)
		}

		// Match against LF-normalised text without the BOM, then put both back
		// so an edit never changes a file's line endings.
		text := string(data)
		bom := ""
		if strings.HasPrefix(text, utf8BOM) {
			bom, text = utf8BOM, text[len(utf8BOM):]
		}
		ending := lineEnding(text)
		base := normalizeLF(text)

		updated, err := applyEdits(base, edits, a.Path)
		if err != nil {
			return Result{}, err
		}
		out := updated
		if ending == "\r\n" {
			out = strings.ReplaceAll(out, "\n", "\r\n")
		}
		info, err := os.Stat(path)
		if err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(path, []byte(bom+out), info.Mode().Perm()); err != nil {
			return Result{}, err
		}

		diff, first := renderDiff(base, updated, 4)
		return Result{
			Text:    fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), a.Path),
			Details: EditDetails{Diff: diff, FirstChangedLine: first},
		}, nil
	})
}

// parseEdits reads the edits argument. Besides the documented array it accepts
// what weaker models actually emit: a single object, or either of those
// encoded as a JSON string.
func parseEdits(raw json.RawMessage) ([]replacement, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("invalid edits: %w", err)
		}
		raw = bytes.TrimSpace([]byte(s))
		if len(raw) == 0 {
			return nil, nil
		}
	}
	var edits []replacement
	switch raw[0] {
	case '[':
		if err := json.Unmarshal(raw, &edits); err != nil {
			return nil, fmt.Errorf("invalid edits: %w", err)
		}
	case '{':
		var one replacement
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, fmt.Errorf("invalid edits: %w", err)
		}
		edits = []replacement{one}
	default:
		return nil, errors.New("invalid edits: expected an array of {oldText, newText}")
	}
	return edits, nil
}

func lineEnding(s string) string {
	crlf := strings.Index(s, "\r\n")
	lf := strings.Index(s, "\n")
	if crlf != -1 && crlf < lf {
		return "\r\n"
	}
	return "\n"
}

func normalizeLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// normalizeFuzzy flattens the differences models commonly get wrong when they
// retype text: trailing whitespace, curly quotes, Unicode dashes and spaces.
// It never adds or removes a line, so line numbers stay valid in both forms.
func normalizeFuzzy(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRightFunc(l, unicode.IsSpace)
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '‘' || r == '’' || r == '‚' || r == '‛':
			return '\''
		case r == '“' || r == '”' || r == '„' || r == '‟':
			return '"'
		case r >= '‐' && r <= '―' || r == '−':
			return '-'
		case r == ' ' || r >= ' ' && r <= ' ' || r == ' ' || r == ' ' || r == '　':
			return ' '
		}
		return r
	}, strings.Join(lines, "\n"))
}

type match struct {
	edit   int // index into the caller's edits, for error messages
	index  int
	length int
	text   string // the replacement
}

// applyEdits applies every edit to base (LF text). Each oldText is located in
// the original, not in the result of earlier edits, and must be unique and
// disjoint from the others. When some oldText only matches after fuzzy
// normalisation, all edits match in normalised space and the lines they touch
// are rewritten from it, while untouched lines keep their original bytes.
func applyEdits(base string, edits []replacement, path string) (string, error) {
	olds := make([]string, len(edits))
	news := make([]string, len(edits))
	fuzzy := false
	for i, e := range edits {
		olds[i], news[i] = normalizeLF(e.OldText), normalizeLF(e.NewText)
		if olds[i] == "" {
			return "", fmt.Errorf("%soldText must not be empty in %s.", editLabel(i, len(edits)), path)
		}
		if !strings.Contains(base, olds[i]) {
			fuzzy = true
		}
	}

	hay := base
	if fuzzy {
		hay = normalizeFuzzy(base)
	}
	matches := make([]match, len(edits))
	for i := range edits {
		old := olds[i]
		if fuzzy {
			old = normalizeFuzzy(old)
		}
		switch n := strings.Count(hay, old); {
		case n == 0 && len(edits) == 1:
			return "", fmt.Errorf("Could not find the exact text in %s. The old text must match exactly including all whitespace and newlines.", path)
		case n == 0:
			return "", fmt.Errorf("Could not find edits[%d] in %s. The oldText must match exactly including all whitespace and newlines.", i, path)
		case n > 1 && len(edits) == 1:
			return "", fmt.Errorf("Found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique.", n, path)
		case n > 1:
			return "", fmt.Errorf("Found %d occurrences of edits[%d] in %s. Each oldText must be unique. Please provide more context to make it unique.", n, i, path)
		}
		matches[i] = match{edit: i, index: strings.Index(hay, old), length: len(old), text: news[i]}
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].index < matches[j].index })
	for i := 1; i < len(matches); i++ {
		if p, c := matches[i-1], matches[i]; p.index+p.length > c.index {
			return "", fmt.Errorf("edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.", p.edit, c.edit, path)
		}
	}

	var out string
	if fuzzy {
		out = replacePreservingLines(base, hay, matches)
	} else {
		out = replaceAll(base, matches, 0)
	}
	if out == base {
		if len(edits) == 1 {
			return "", fmt.Errorf("No changes made to %s. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.", path)
		}
		return "", fmt.Errorf("No changes made to %s. The replacements produced identical content.", path)
	}
	return out, nil
}

func editLabel(i, n int) string {
	if n == 1 {
		return ""
	}
	return fmt.Sprintf("edits[%d].", i)
}

// replaceAll applies matches (sorted, disjoint, indexes relative to s plus
// offset) to s.
func replaceAll(s string, ms []match, offset int) string {
	var b strings.Builder
	pos := 0
	for _, m := range ms {
		at := m.index - offset
		b.WriteString(s[pos:at])
		b.WriteString(m.text)
		pos = at + m.length
	}
	b.WriteString(s[pos:])
	return b.String()
}

type span struct{ start, end int }

func lineSpans(s string) []span {
	var spans []span
	pos := 0
	for pos < len(s) {
		end := len(s)
		if i := strings.IndexByte(s[pos:], '\n'); i >= 0 {
			end = pos + i + 1
		}
		spans = append(spans, span{pos, end})
		pos = end
	}
	return spans
}

// replacePreservingLines applies matches found in norm, the normalised form of
// orig, to orig. Every line a match touches is rewritten from norm; all other
// lines are copied from orig unchanged.
func replacePreservingLines(orig, norm string, ms []match) string {
	spans := lineSpans(norm) // same line structure as orig

	type group struct {
		first, last int // line range, last exclusive
		ms          []match
	}
	var groups []group
	for _, m := range ms {
		first := sort.Search(len(spans), func(i int) bool { return spans[i].end > m.index })
		last := first
		for last < len(spans) && spans[last].end < m.index+m.length {
			last++
		}
		last++
		if n := len(groups); n > 0 && first < groups[n-1].last {
			groups[n-1].last = max(groups[n-1].last, last)
			groups[n-1].ms = append(groups[n-1].ms, m)
			continue
		}
		groups = append(groups, group{first, last, []match{m}})
	}

	var b strings.Builder
	pos := 0 // offset in orig; orig and norm share line starts only by line index
	origSpans := lineSpans(orig)
	for _, g := range groups {
		b.WriteString(orig[pos:origSpans[g.first].start])
		from, to := spans[g.first].start, spans[g.last-1].end
		b.WriteString(replaceAll(norm[from:to], g.ms, from))
		pos = origSpans[g.last-1].end
	}
	b.WriteString(orig[pos:])
	return b.String()
}
