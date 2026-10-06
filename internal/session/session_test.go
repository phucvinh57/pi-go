package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// clock is a fake clock that moves one second per call.
type clock struct{ t time.Time }

func newClock() *clock { return &clock{t: time.Date(2026, 10, 6, 9, 30, 0, 123_000_000, time.UTC)} }

func (c *clock) now() time.Time {
	t := c.t
	c.t = c.t.Add(time.Second)
	return t
}

func assistant(text string, in, out int, cost float64) ai.Message {
	return ai.Message{
		Role: ai.RoleAssistant, Provider: "ollama", Model: "qwen",
		Content:    []ai.Block{{Type: ai.BlockText, Text: text}},
		StopReason: ai.StopStop,
		Usage:      ai.Usage{Input: in, Output: out, TotalTokens: in + out, Cost: ai.Cost{Total: cost}},
	}
}

func TestDirEncodesCwd(t *testing.T) {
	for cwd, want := range map[string]string{
		"/home/me/proj":   "--home-me-proj--",
		"/":               "----",
		`C:\Users\me\app`: "--C--Users-me-app--",
	} {
		got := Dir("/agent", cwd)
		if want := filepath.Join("/agent", "sessions", want); got != want {
			t.Errorf("Dir(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func TestSessionIDIsUUIDv7(t *testing.T) {
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	now := time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
	a, b := newSessionID(now), newSessionID(now)
	if !uuid.MatchString(a) || !uuid.MatchString(b) {
		t.Fatalf("not UUIDv7 shaped: %q %q", a, b)
	}
	if a == b {
		t.Error("two IDs in the same millisecond must still differ")
	}
	// The leading 48 bits are the Unix milliseconds.
	if got, want := a[:8]+a[9:13], fmt.Sprintf("%012x", now.UnixMilli()); got != want {
		t.Errorf("prefix %q does not encode %d ms (%q)", got, now.UnixMilli(), want)
	}
	later := newSessionID(now.Add(time.Hour))
	if later <= a {
		t.Errorf("IDs must sort by time: %q then %q", a, later)
	}
}

func TestNothingWrittenBeforeTheConversation(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "/work/p", newClock().now)
	w.ModelChange("ollama", "qwen")
	w.Record(ai.ToolResult("c", "read", "stray", false)) // not user or assistant speech

	if _, err := os.Stat(filepath.Join(dir, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("starting a session must not create files: %v", err)
	}
	if paths, _ := List(dir); len(paths) != 0 {
		t.Fatalf("listed %v", paths)
	}

	w.Record(ai.UserText("hello"))
	s, err := Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	// The entries that were waiting come first, in order, then the message.
	var types []string
	for _, e := range s.Entries {
		types = append(types, e.Type)
	}
	if got := strings.Join(types, ","); got != "model_change,message,message" {
		t.Errorf("entries = %s", got)
	}
}

func TestWriteAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	clk := newClock()
	w := NewWriter(dir, "/work/p", clk.now)
	w.ModelChange("ollama", "qwen")
	w.Record(ai.UserText("what is 2+2?"))
	reply := assistant("4", 120, 3, 0.002)
	w.Record(reply)
	w.ModelChange("openai-codex", "gpt-5.5")
	if err := w.Err(); err != nil {
		t.Fatal(err)
	}

	s, err := Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if s.Skipped != 0 {
		t.Errorf("skipped %d lines", s.Skipped)
	}
	h := s.Header
	if h.Type != "session" || h.Version != Version || h.ID != w.ID() || h.CWD != "/work/p" || h.Timestamp != "2026-10-06T09:30:00.123Z" {
		t.Errorf("header = %+v", h)
	}
	if !strings.HasSuffix(w.Path(), "2026-10-06T09-30-00-123Z_"+w.ID()+".jsonl") || filepath.Dir(w.Path()) != Dir(dir, "/work/p") {
		t.Errorf("path = %s", w.Path())
	}
	if len(s.Entries) != 4 {
		t.Fatalf("%d entries: %+v", len(s.Entries), s.Entries)
	}

	// Each entry is a child of the one before it; the first has no parent.
	for i, e := range s.Entries {
		switch {
		case i == 0 && e.ParentID != nil:
			t.Errorf("first entry has parent %q", *e.ParentID)
		case i > 0 && (e.ParentID == nil || *e.ParentID != s.Entries[i-1].ID):
			t.Errorf("entry %d is not a child of entry %d", i, i-1)
		}
		if e.ID == "" || e.Time().IsZero() {
			t.Errorf("entry %d = %+v", i, e)
		}
		if i > 0 && !e.Time().After(s.Entries[i-1].Time()) {
			t.Errorf("timestamps must increase: %v then %v", s.Entries[i-1].Timestamp, e.Timestamp)
		}
	}

	if e := s.Entries[0]; e.Type != TypeModelChange || e.Provider != "ollama" || e.ModelID != "qwen" {
		t.Errorf("first entry = %+v", e)
	}
	if m := s.Entries[1].Message; m == nil || m.Role != ai.RoleUser || m.Text() != "what is 2+2?" {
		t.Errorf("user entry = %+v", s.Entries[1])
	}
	got := s.Entries[2].Message
	if got == nil || got.Text() != "4" || got.Usage != reply.Usage || got.Provider != "ollama" || got.StopReason != ai.StopStop {
		t.Errorf("assistant entry = %+v", got)
	}
	if e := s.Entries[3]; e.Type != TypeModelChange || e.ModelID != "gpt-5.5" {
		t.Errorf("last entry = %+v", e)
	}

	// One JSON object per line, header first: the format PI's files have.
	raw, _ := os.ReadFile(w.Path())
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 5 {
		t.Fatalf("file has %d lines, want header + 4 entries", len(lines))
	}
	for i, l := range lines {
		if !json.Valid([]byte(l)) {
			t.Errorf("line %d is not JSON: %q", i, l)
		}
	}
	info, _ := os.Stat(w.Path())
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v: conversations are private", info.Mode().Perm())
	}
}

func TestLoadSkipsDamagedLines(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "/p", newClock().now)
	w.Record(ai.UserText("one"))
	w.Record(assistant("two", 1, 1, 0))

	// A crash can leave half a line at the end; a bad edit, garbage in the middle.
	f, err := os.OpenFile(w.Path(), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("not json at all\n{\"id\":\"x\"}\n{\"type\":\"message\",\"mess")
	f.Close()

	s, err := Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Entries) != 2 || s.Skipped != 3 {
		t.Errorf("entries %d, skipped %d; want 2 and 3", len(s.Entries), s.Skipped)
	}
}

func TestLoadRejectsFilesWithoutHeader(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"empty.jsonl":  "",
		"entry.jsonl":  `{"type":"message","id":"a"}` + "\n",
		"random.jsonl": "hello\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := Load(filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("a missing file is an error for Load")
	}
}

func TestLoadHandlesVeryLongLines(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "/p", newClock().now)
	w.Record(ai.UserText("go"))
	w.Record(ai.ToolResult("c", "bash", strings.Repeat("x", 5<<20), false)) // 5 MB of output

	s, err := Load(w.Path())
	if err != nil || s.Skipped != 0 || len(s.Entries) != 2 {
		t.Fatalf("err %v, skipped %d, entries %d", err, s.Skipped, len(s.Entries))
	}
}

func TestListAndLoadAll(t *testing.T) {
	dir := t.TempDir()
	if paths, err := List(dir); err != nil || len(paths) != 0 {
		t.Fatalf("no sessions dir: %v, %v", paths, err)
	}

	clk := newClock()
	a := NewWriter(dir, "/proj/a", clk.now)
	a.Record(ai.UserText("first"))
	b := NewWriter(dir, "/proj/b", clk.now)
	b.Record(ai.UserText("second"))
	c := NewWriter(dir, "/proj/a", clk.now)
	c.Record(ai.UserText("third"))

	// A file that is not a session, and a stray non-jsonl file.
	bad := filepath.Join(Dir(dir, "/proj/a"), "broken.jsonl")
	os.WriteFile(bad, []byte("garbage\n"), 0o600)
	os.WriteFile(filepath.Join(Dir(dir, "/proj/a"), "notes.txt"), []byte("x"), 0o600)

	paths, err := List(dir)
	if err != nil || len(paths) != 4 {
		t.Fatalf("List = %v, %v", paths, err)
	}

	sessions, err := LoadAll(dir)
	if err == nil || !strings.Contains(err.Error(), "broken.jsonl") {
		t.Errorf("the unreadable file must be named in the error: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("loaded %d sessions, want the 3 good ones", len(sessions))
	}
	var order []string
	for _, s := range sessions {
		order = append(order, s.Entries[0].Message.Text())
	}
	if got := strings.Join(order, ","); got != "first,second,third" {
		t.Errorf("sessions are oldest first across projects: %s", got)
	}
}

func TestWriterReportsAnErrorOnceAndStops(t *testing.T) {
	dir := t.TempDir()
	// A file where the sessions directory should be makes MkdirAll fail.
	if err := os.WriteFile(filepath.Join(dir, "sessions"), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := NewWriter(dir, "/p", newClock().now)
	w.Record(ai.UserText("hello")) // must not panic or return anything
	w.Record(assistant("hi", 1, 1, 0))

	err := w.Err()
	if err == nil || !strings.Contains(err.Error(), "save session") {
		t.Fatalf("Err = %v", err)
	}
	if again := w.Err(); again != err {
		t.Errorf("the first error is kept, got %v then %v", err, again)
	}
}

func TestWriterIgnoresEntriesAfterClose(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "/p", newClock().now)
	w.Record(ai.UserText("one"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w.Record(ai.UserText("two"))
	s, err := Load(w.Path())
	if err != nil || len(s.Entries) != 1 {
		t.Fatalf("entries %v, err %v", s, err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("a second Close = %v", err)
	}
}

func TestWriterIsSafeForConcurrentUse(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, "/p", nil)
	w.Record(ai.UserText("start"))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				w.Record(assistant("x", 1, 1, 0))
				w.ModelChange("ollama", "qwen")
			}
		}()
	}
	wg.Wait()

	s, err := Load(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if s.Skipped != 0 || len(s.Entries) != 1+8*25*2 {
		t.Fatalf("skipped %d, entries %d: lines were interleaved or lost", s.Skipped, len(s.Entries))
	}
	// The parent chain is unbroken even under contention.
	for i := 1; i < len(s.Entries); i++ {
		if p := s.Entries[i].ParentID; p == nil || *p != s.Entries[i-1].ID {
			t.Fatalf("entry %d breaks the chain", i)
		}
	}
}
