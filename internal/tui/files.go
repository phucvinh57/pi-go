package tui

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxFileMatches = 50      // most completions kept for the menu
	maxIndexFiles  = 20000   // stop indexing a huge tree here
	maxTagBytes    = 100_000 // a tagged file longer than this is cut
)

// skipDirs are never walked when the directory is not a git checkout.
var skipDirs = map[string]bool{"node_modules": true, "vendor": true}

// listFiles returns the files under root as slash-separated relative paths. In
// a git checkout it asks git, so ignored files stay out; otherwise it walks the
// tree, skipping hidden and dependency directories. Names with whitespace are
// left out, because a tag ends at the first space.
func listFiles(root string) []string {
	files, ok := gitFiles(root)
	if !ok {
		files = walkFiles(root)
	}
	out := files[:0]
	for _, f := range files {
		if !strings.ContainsAny(f, " \t\n") {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func gitFiles(root string) ([]string, bool) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	raw, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	var files []string
	for _, f := range bytes.Split(raw, []byte{0}) {
		if len(f) == 0 {
			continue
		}
		// A deleted file is still in the index.
		if _, err := os.Lstat(filepath.Join(root, string(f))); err != nil {
			continue
		}
		files = append(files, string(f))
		if len(files) >= maxIndexFiles {
			break
		}
	}
	return files, true
}

func walkFiles(root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable: skip it
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			files = append(files, filepath.ToSlash(rel))
		}
		if len(files) >= maxIndexFiles {
			return filepath.SkipAll
		}
		return nil
	})
	return files
}

// trailingTag returns the "@query" the user is typing at the end of input, and
// whether there is one. The "@" must start the input or follow whitespace, so
// an email address is not a tag.
func trailingTag(input string) (query string, ok bool) {
	start := strings.LastIndexAny(input, " \t\n") + 1
	word := input[start:]
	if !strings.HasPrefix(word, "@") {
		return "", false
	}
	return word[1:], true
}

// matchFiles returns the files that match query, best first: names starting
// with it, then paths starting with it, then names and paths containing it,
// then paths with its letters in order.
func matchFiles(files []string, query string) []string {
	q := strings.ToLower(query)
	type hit struct {
		path  string
		score int
	}
	var hits []hit
	for _, f := range files {
		lower := strings.ToLower(f)
		base := lower[strings.LastIndex(lower, "/")+1:]
		score := -1
		switch {
		case q == "":
			score = 0
		case strings.HasPrefix(base, q):
			score = 0
		case strings.HasPrefix(lower, q):
			score = 1
		case strings.Contains(base, q):
			score = 2
		case strings.Contains(lower, q):
			score = 3
		case isSubsequence(q, lower):
			score = 4
		}
		if score >= 0 {
			hits = append(hits, hit{f, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.score != b.score {
			return a.score < b.score
		}
		if len(a.path) != len(b.path) {
			return len(a.path) < len(b.path)
		}
		return a.path < b.path
	})
	out := make([]string, 0, min(len(hits), maxFileMatches))
	for _, h := range hits[:min(len(hits), maxFileMatches)] {
		out = append(out, h.path)
	}
	return out
}

func isSubsequence(needle, haystack string) bool {
	n := []rune(needle)
	i := 0
	for _, r := range haystack {
		if i < len(n) && n[i] == r {
			i++
		}
	}
	return i == len(n)
}

// expandTags attaches the files tagged with "@path" in text. It returns the
// text the model should see, which is text followed by one <file> block per
// distinct file, and the paths attached. A tag that is not an existing text
// file under root stays as plain text. Paths are relative to root.
func expandTags(root, text string) (string, []string) {
	var (
		paths  []string
		blocks []string
		seen   = map[string]bool{}
	)
	for _, word := range strings.Fields(text) {
		if !strings.HasPrefix(word, "@") {
			continue
		}
		path, content, ok := readTagged(root, word[1:])
		if !ok || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
		blocks = append(blocks, fmt.Sprintf("<file path=%q>\n%s\n</file>", path, content))
	}
	if len(blocks) == 0 {
		return text, nil
	}
	return text + "\n\n" + strings.Join(blocks, "\n\n"), paths
}

// readTagged reads the file a tag names. Trailing punctuation is dropped if the
// whole word is not a file, so "see @a.go, then" still finds a.go.
func readTagged(root, name string) (path, content string, ok bool) {
	for name != "" {
		full := name
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, name)
		}
		if info, err := os.Stat(full); err == nil && info.Mode().IsRegular() {
			data, err := os.ReadFile(full)
			if err != nil || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
				return "", "", false // unreadable or binary
			}
			if len(data) > maxTagBytes {
				data = append(data[:maxTagBytes:maxTagBytes], "\n[truncated]"...)
			}
			return name, strings.TrimSuffix(string(data), "\n"), true
		}
		if !strings.ContainsRune(",.;:!?)]}\"'", rune(name[len(name)-1])) {
			return "", "", false
		}
		name = name[:len(name)-1]
	}
	return "", "", false
}
