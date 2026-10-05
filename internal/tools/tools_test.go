package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// call runs a tool with args given as a Go value.
func call(t *testing.T, tool Tool, args any) (Result, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Execute(context.Background(), raw, nil)
}

type m = map[string]any

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func lines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestCoreSpecs(t *testing.T) {
	ts := Core(t.TempDir())
	var names []string
	for _, tool := range ts {
		s := tool.Spec()
		names = append(names, s.Name)
		if s.Description == "" || s.Snippet == "" {
			t.Errorf("%s: missing description or snippet", s.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(s.Parameters, &schema); err != nil || schema["type"] != "object" {
			t.Errorf("%s: parameters is not a JSON Schema object: %v", s.Name, err)
		}
	}
	if got := strings.Join(names, ","); got != "read,bash,edit,write" {
		t.Errorf("names = %s", got)
	}
	if _, ok := Find(ts, "edit"); !ok {
		t.Error("Find(edit) failed")
	}
	if _, ok := Find(ts, "nope"); ok {
		t.Error("Find(nope) succeeded")
	}
}

func TestResolvePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	for _, tc := range []struct{ in, want string }{
		{"a/b.txt", "/work/a/b.txt"},
		{"/abs/x", "/abs/x"},
		{"@src/x.go", "/work/src/x.go"},
		{"~/notes", filepath.Join(home, "notes")},
		{"a\u202fb", "/work/a b"},
		{"../up", "/up"},
	} {
		if got := resolvePath(tc.in, "/work"); got != tc.want {
			t.Errorf("resolvePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	r := NewRead(dir)

	res, err := call(t, r, m{"path": "a.txt"})
	if err != nil || res.Text != "one\ntwo\nthree\n" {
		t.Fatalf("got %q, %v", res.Text, err)
	}

	res, err = call(t, r, m{"path": "a.txt", "offset": 2, "limit": 1})
	if err != nil || !strings.HasPrefix(res.Text, "two\n\n[2 more lines in file. Use offset=3 to continue.]") {
		t.Fatalf("got %q, %v", res.Text, err)
	}

	if _, err = call(t, r, m{"path": "a.txt", "offset": 99}); err == nil || !strings.Contains(err.Error(), "beyond end of file") {
		t.Errorf("offset past end: %v", err)
	}
	if _, err = call(t, r, m{"path": "missing.txt"}); err == nil {
		t.Error("missing file: want error")
	}
	if _, err = call(t, r, m{"path": "."}); err == nil {
		t.Error("directory: want error")
	}
	if _, err = call(t, r, m{}); err == nil {
		t.Error("no path: want error")
	}
	if _, err = call(t, r, m{"path": "a.txt", "limit": 0}); err == nil {
		t.Error("limit 0: want error")
	}
}

func TestReadTruncatesLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "big.txt", lines(5000))
	res, err := call(t, NewRead(dir), m{"path": "big.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "line 2000\n") || strings.Contains(res.Text, "line 2001\n") {
		t.Error("expected exactly the first 2000 lines")
	}
	if !strings.Contains(res.Text, "[Showing lines 1-2000 of 5001. Use offset=2001 to continue.]") {
		t.Errorf("missing continuation hint: %q", res.Text[len(res.Text)-120:])
	}
	if tr, ok := res.Details.(*Truncation); !ok || tr.By != "lines" {
		t.Errorf("details = %#v", res.Details)
	}

	// The hint's offset really continues where the output stopped.
	res, err = call(t, NewRead(dir), m{"path": "big.txt", "offset": 2001, "limit": 2})
	if err != nil || !strings.HasPrefix(res.Text, "line 2001\nline 2002\n") {
		t.Errorf("continue: %q, %v", res.Text, err)
	}
}

func TestReadTruncatesBytes(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 1000) + "\n"
	writeFile(t, dir, "wide.txt", strings.Repeat(long, 100))
	res, err := call(t, NewRead(dir), m{"path": "wide.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "50.0KB limit") {
		t.Errorf("want byte-limit hint: %q", res.Text[len(res.Text)-120:])
	}

	writeFile(t, dir, "oneline.txt", strings.Repeat("y", 2*MaxBytes))
	res, err = call(t, NewRead(dir), m{"path": "oneline.txt"})
	if err != nil || !strings.Contains(res.Text, "exceeds 50.0KB limit") {
		t.Errorf("giant first line: %q, %v", res.Text, err)
	}
}

func TestReadRejectsImage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "p.png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if _, err := call(t, NewRead(dir), m{"path": "p.png"}); err == nil || !strings.Contains(err.Error(), "image") {
		t.Errorf("err = %v", err)
	}
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	w := NewWrite(dir)

	res, err := call(t, w, m{"path": "deep/er/new.txt", "content": "hello"})
	if err != nil || !strings.Contains(res.Text, "5 bytes") {
		t.Fatalf("got %q, %v", res.Text, err)
	}
	if got := readFile(t, filepath.Join(dir, "deep/er/new.txt")); got != "hello" {
		t.Errorf("content = %q", got)
	}

	if _, err = call(t, w, m{"path": "deep/er/new.txt", "content": ""}); err != nil {
		t.Fatal(err) // empty content is valid: it truncates the file
	}
	if got := readFile(t, filepath.Join(dir, "deep/er/new.txt")); got != "" {
		t.Errorf("overwrite = %q", got)
	}

	if _, err = call(t, w, m{"path": "x"}); err == nil {
		t.Error("missing content: want error")
	}
	if _, err = call(t, w, m{"content": "x"}); err == nil {
		t.Error("missing path: want error")
	}
}

func TestEditSingle(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "f.go", "package a\n\nfunc One() {}\nfunc Two() {}\n")
	res, err := call(t, NewEdit(dir), m{"path": "f.go", "edits": []m{{"oldText": "One", "newText": "Uno"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "package a\n\nfunc Uno() {}\nfunc Two() {}\n" {
		t.Errorf("content = %q", got)
	}
	if !strings.Contains(res.Text, "Successfully replaced 1 block(s) in f.go") {
		t.Errorf("text = %q", res.Text)
	}
	d, ok := res.Details.(EditDetails)
	if !ok || d.FirstChangedLine != 3 {
		t.Fatalf("details = %#v", res.Details)
	}
	for _, want := range []string{"-3 func One() {}", "+3 func Uno() {}", " 4 func Two() {}"} {
		if !strings.Contains(d.Diff, want) {
			t.Errorf("diff missing %q:\n%s", want, d.Diff)
		}
	}
}

func TestEditMultipleMatchOriginal(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "f.txt", "a\nb\nc\n")
	// The second edit's oldText exists only in the first edit's newText; edits
	// are matched against the original, so it must be reported as not found.
	_, err := call(t, NewEdit(dir), m{"path": "f.txt", "edits": []m{
		{"oldText": "a", "newText": "zzz"},
		{"oldText": "zzz", "newText": "q"},
	}})
	if err == nil || !strings.Contains(err.Error(), "edits[1]") {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, p) != "a\nb\nc\n" {
		t.Error("a failed edit must leave the file untouched")
	}

	// Edits given out of order, and both applied.
	if _, err = call(t, NewEdit(dir), m{"path": "f.txt", "edits": []m{
		{"oldText": "c", "newText": "C"},
		{"oldText": "a", "newText": "A"},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "A\nb\nC\n" {
		t.Errorf("content = %q", got)
	}
}

func TestEditErrors(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "f.txt", "foo\nfoo\nbar baz\n")
	e := NewEdit(dir)

	for name, tc := range map[string]struct {
		args m
		want string
	}{
		"duplicate":   {m{"path": "f.txt", "edits": []m{{"oldText": "foo", "newText": "x"}}}, "Found 2 occurrences"},
		"not found":   {m{"path": "f.txt", "edits": []m{{"oldText": "nope", "newText": "x"}}}, "Could not find the exact text"},
		"empty old":   {m{"path": "f.txt", "edits": []m{{"oldText": "", "newText": "x"}}}, "must not be empty"},
		"no edits":    {m{"path": "f.txt", "edits": []m{}}, "at least one"},
		"identical":   {m{"path": "f.txt", "edits": []m{{"oldText": "bar", "newText": "bar"}}}, "No changes made"},
		"no file":     {m{"path": "gone.txt", "edits": []m{{"oldText": "a", "newText": "b"}}}, "Could not edit file"},
		"bad edits":   {m{"path": "f.txt", "edits": 7}, "invalid edits"},
		"overlapping": {m{"path": "f.txt", "edits": []m{{"oldText": "bar b", "newText": "x"}, {"oldText": "r baz", "newText": "y"}}}, "overlap"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := call(t, e, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
	if readFile(t, p) != "foo\nfoo\nbar baz\n" {
		t.Error("failed edits changed the file")
	}
}

func TestEditPreservesCRLFAndBOM(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "w.txt", "\ufeffone\r\ntwo\r\nthree\r\n")
	// The model sees LF in read output, so it sends LF in oldText.
	if _, err := call(t, NewEdit(dir), m{"path": "w.txt", "edits": []m{{"oldText": "one\ntwo", "newText": "1\n2\n2b"}}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "\ufeff1\r\n2\r\n2b\r\nthree\r\n" {
		t.Errorf("content = %q", got)
	}
}

func TestEditKeepsFileMode(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "run.sh", "echo a\n")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, NewEdit(dir), m{"path": "run.sh", "edits": []m{{"oldText": "a", "newText": "b"}}}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestEditFuzzy(t *testing.T) {
	dir := t.TempDir()
	// Curly quotes and a trailing space in the file; the model retypes plain ones.
	orig := "keep  \nsay \u201chi\u201d \nmid  \nend\n"
	p := writeFile(t, dir, "f.txt", orig)
	if _, err := call(t, NewEdit(dir), m{"path": "f.txt", "edits": []m{{"oldText": "say \"hi\"", "newText": "say \"bye\""}}}); err != nil {
		t.Fatal(err)
	}
	// Only the touched line is rewritten; "keep  " and "mid  " keep their trailing spaces.
	if got, want := readFile(t, p), "keep  \nsay \"bye\"\nmid  \nend\n"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}

	// Two fuzzy edits on separate lines, untouched line between them intact.
	p = writeFile(t, dir, "g.txt", "a\u2013b \nkeep  \nc\u00a0d\n")
	if _, err := call(t, NewEdit(dir), m{"path": "g.txt", "edits": []m{
		{"oldText": "a-b", "newText": "AB"},
		{"oldText": "c d", "newText": "CD"},
	}}); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, p), "AB\nkeep  \nCD\n"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestEditLenientArguments(t *testing.T) {
	dir := t.TempDir()
	for name, args := range map[string]string{
		"single object":  `{"path":"f.txt","edits":{"oldText":"a","newText":"b"}}`,
		"string array":   `{"path":"f.txt","edits":"[{\"oldText\":\"a\",\"newText\":\"b\"}]"}`,
		"string object":  `{"path":"f.txt","edits":"{\"oldText\":\"a\",\"newText\":\"b\"}"}`,
		"legacy top-lvl": `{"path":"f.txt","oldText":"a","newText":"b"}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := writeFile(t, dir, "f.txt", "a\n")
			if _, err := NewEdit(dir).Execute(context.Background(), json.RawMessage(args), nil); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, p); got != "b\n" {
				t.Errorf("content = %q", got)
			}
		})
	}
}

func TestEditConcurrentSameFile(t *testing.T) {
	dir := t.TempDir()
	const n = 20
	var content strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&content, "v%d=0\n", i)
	}
	p := writeFile(t, dir, "f.txt", content.String())

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := call(t, NewEdit(dir), m{"path": "f.txt", "edits": []m{{"oldText": fmt.Sprintf("v%d=0", i), "newText": fmt.Sprintf("v%d=1", i)}}})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := readFile(t, p); strings.Contains(got, "=0") {
		t.Errorf("lost updates:\n%s", got)
	}
	if len(fileLocks.m) != 0 {
		t.Errorf("file locks leaked: %d", len(fileLocks.m))
	}
}

func TestRenderDiff(t *testing.T) {
	old := lines(30)
	updated := strings.Replace(old, "line 15\n", "LINE 15\nextra\n", 1)
	diff, first := renderDiff(old, updated, 2)
	if first != 15 {
		t.Errorf("first = %d", first)
	}
	want := strings.Join([]string{
		"    ...",
		" 13 line 13",
		" 14 line 14",
		"-15 line 15",
		"+15 LINE 15",
		"+16 extra",
		" 16 line 16",
		" 17 line 17",
		"    ...",
	}, "\n")
	if diff != want {
		t.Errorf("diff:\n%s\nwant:\n%s", diff, want)
	}

	if d, f := renderDiff("same", "same", 2); f != 0 || strings.ContainsAny(d, "+-") {
		t.Errorf("no-change diff = %q, %d", d, f)
	}
}

func TestTruncateHeadTail(t *testing.T) {
	s := lines(10)
	h := truncateHead(s, 3, 1000)
	if h.Content != "line 1\nline 2\nline 3" || h.By != "lines" || h.TotalLines != 10 || h.OutputLines != 3 {
		t.Errorf("head = %+v", h)
	}
	tl := truncateTail(s, 3, 1000)
	if tl.Content != "line 8\nline 9\nline 10" || tl.By != "lines" {
		t.Errorf("tail = %+v", tl)
	}
	if tb := truncateTail(s, 100, 15); tb.By != "bytes" || tb.Content != "line 9\nline 10" {
		t.Errorf("tail by bytes = %+v", tb)
	}
	// One huge last line keeps its end, cut on a rune boundary.
	tp := truncateTail("é"+strings.Repeat("ab", 10), 10, 7)
	if !tp.LastLinePartial || len(tp.Content) > 7 || tp.Content != "bababab" {
		t.Errorf("partial = %+v", tp)
	}
	if tp := truncateTail(strings.Repeat("é", 10), 10, 7); tp.Content != "ééé" {
		t.Errorf("rune boundary: %q", tp.Content)
	}
	if n := truncateHead("", 3, 10); n.Truncated || n.TotalLines != 0 {
		t.Errorf("empty = %+v", n)
	}
}

func TestBash(t *testing.T) {
	dir := t.TempDir()
	b := NewBash(dir)

	res, err := call(t, b, m{"command": "echo hi; pwd"})
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if res.Text != "hi\n"+real+"\n" && res.Text != "hi\n"+dir+"\n" {
		t.Errorf("text = %q", res.Text)
	}

	// stdout and stderr arrive interleaved in one stream
	res, err = call(t, b, m{"command": "echo out; echo err >&2; echo out2"})
	if err != nil || res.Text != "out\nerr\nout2\n" {
		t.Errorf("combined = %q, %v", res.Text, err)
	}

	res, err = call(t, b, m{"command": "true"})
	if err != nil || res.Text != "(no output)" {
		t.Errorf("empty = %q, %v", res.Text, err)
	}
}

func TestBashFailure(t *testing.T) {
	b := NewBash(t.TempDir())
	_, err := call(t, b, m{"command": "echo partial; exit 3"})
	if err == nil || err.Error() != "partial\n\n\nCommand exited with code 3" {
		t.Errorf("err = %q", err)
	}
	_, err = call(t, b, m{"command": "kill -9 $$"})
	if err == nil || !strings.Contains(err.Error(), "code 137") {
		t.Errorf("signal exit: %v", err)
	}
	for _, bad := range []m{{}, {"command": "x", "timeout": 0}, {"command": "x", "timeout": -1}} {
		if _, err := call(t, b, bad); err == nil {
			t.Errorf("args %v: want error", bad)
		}
	}
	if _, err := call(t, NewBash(filepath.Join(t.TempDir(), "gone")), m{"command": "true"}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing cwd: %v", err)
	}
}

func TestBashTimeout(t *testing.T) {
	b := NewBash(t.TempDir())
	start := time.Now()
	_, err := call(t, b, m{"command": "echo started; sleep 30", "timeout": 0.3})
	if err == nil || !strings.Contains(err.Error(), "started") || !strings.Contains(err.Error(), "timed out after 0.3 seconds") {
		t.Errorf("err = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestBashAbortKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "survived")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	// The subshell is a grandchild of the shell; it must die with the group,
	// and the shell must not wait for it.
	cmd := fmt.Sprintf("(sleep 1.5; touch %s) & echo ready; wait", marker)
	raw, _ := json.Marshal(m{"command": cmd})
	_, err := NewBash(dir).Execute(ctx, raw, nil)
	if err == nil || !strings.Contains(err.Error(), "Command aborted") || !strings.Contains(err.Error(), "ready") {
		t.Fatalf("err = %v", err)
	}
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Error("grandchild survived the abort")
	}
}

func TestBashTruncatesAndKeepsFullOutput(t *testing.T) {
	b := NewBash(t.TempDir())
	res, err := call(t, b, m{"command": "seq 1 5000"})
	if err != nil {
		t.Fatal(err)
	}
	d, ok := res.Details.(BashDetails)
	if !ok || d.Truncation == nil || d.FullOutputPath == "" {
		t.Fatalf("details = %#v", res.Details)
	}
	defer os.Remove(d.FullOutputPath)

	if !strings.HasPrefix(res.Text, "3001\n") || !strings.Contains(res.Text, "\n5000\n") {
		t.Errorf("want the last 2000 lines, got %q...", res.Text[:20])
	}
	if !strings.Contains(res.Text, "[Showing lines 3001-5000 of 5000. Full output: "+d.FullOutputPath+"]") {
		t.Errorf("tail note: %q", res.Text[len(res.Text)-150:])
	}
	full := readFile(t, d.FullOutputPath)
	if !strings.HasPrefix(full, "1\n2\n") || !strings.HasSuffix(full, "4999\n5000\n") || strings.Count(full, "\n") != 5000 {
		t.Errorf("full output file is incomplete (%d bytes)", len(full))
	}
}

func TestBashHugeOutputBoundedMemory(t *testing.T) {
	b := NewBash(t.TempDir())
	// 20 MB in one go, no newlines in the middle of the first line.
	res, err := call(t, b, m{"command": "head -c 20000000 /dev/zero | tr '\\0' 'x'; echo; echo END"})
	if err != nil {
		t.Fatal(err)
	}
	d := res.Details.(BashDetails)
	defer os.Remove(d.FullOutputPath)
	if !strings.HasSuffix(strings.SplitN(res.Text, "\n\n[", 2)[0], "END") {
		t.Errorf("tail lost: %q", res.Text[:60])
	}
	if info, err := os.Stat(d.FullOutputPath); err != nil || info.Size() != 20_000_000+1+4 {
		t.Errorf("full output size = %v, %v", info, err)
	}
}

func TestBashStreamsUpdates(t *testing.T) {
	var mu sync.Mutex
	var updates []string
	raw, _ := json.Marshal(m{"command": "echo one; sleep 0.3; echo two"})
	res, err := NewBash(t.TempDir()).Execute(context.Background(), raw, func(r Result) {
		mu.Lock()
		updates = append(updates, r.Text)
		mu.Unlock()
	})
	if err != nil || res.Text != "one\ntwo\n" {
		t.Fatalf("got %q, %v", res.Text, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(updates) == 0 || updates[0] != "one\n" {
		t.Errorf("updates = %q", updates)
	}
}
