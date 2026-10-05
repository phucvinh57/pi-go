package tui

import "testing"

func newHistory(entries ...string) *history {
	h := &history{}
	for _, e := range entries {
		h.add(e)
	}
	return h
}

func TestHistoryEmpty(t *testing.T) {
	h := newHistory()
	if _, ok := h.prev(""); ok {
		t.Error("prev on empty history should report false")
	}
	if _, ok := h.next(""); ok {
		t.Error("next while not navigating should report false")
	}
}

func TestHistoryWalkAndRestoreDraft(t *testing.T) {
	h := newHistory("one", "two")

	if got, _ := h.prev(""); got != "two" {
		t.Fatalf("first prev = %q", got)
	}
	if got, _ := h.prev("two"); got != "one" {
		t.Fatalf("second prev = %q", got)
	}
	if _, ok := h.prev("one"); ok {
		t.Error("prev past the oldest should report false")
	}
	if got, _ := h.next("one"); got != "two" {
		t.Errorf("next = %q, want two", got)
	}
	if got, ok := h.next("two"); !ok || got != "" {
		t.Errorf("next past newest = %q, %v; want the empty draft", got, ok)
	}
	if h.navigating() {
		t.Error("navigation should end after the draft is restored")
	}
}

func TestHistoryPrefixFilter(t *testing.T) {
	h := newHistory("foo a", "bar", "foo b")

	if got, _ := h.prev("foo"); got != "foo b" {
		t.Fatalf("prev = %q, want foo b", got)
	}
	if got, _ := h.prev("foo b"); got != "foo a" {
		t.Fatalf("prev = %q, want foo a (bar filtered out)", got)
	}
	if got, _ := h.next("foo a"); got != "foo b" {
		t.Errorf("next = %q, want foo b", got)
	}
	if got, _ := h.next("foo b"); got != "foo" {
		t.Errorf("next past newest = %q, want the draft foo", got)
	}
}

func TestHistorySkipsEntryEqualToCurrent(t *testing.T) {
	h := newHistory("ab", "a")
	// The editor already shows "a", so prev must go past it to "ab".
	if got, ok := h.prev("a"); !ok || got != "ab" {
		t.Errorf("prev = %q, %v; want ab", got, ok)
	}
}

func TestHistoryAddSkipsConsecutiveDuplicate(t *testing.T) {
	h := newHistory("a", "a", "b", "a")
	if got := len(h.entries); got != 3 {
		t.Errorf("entries = %v, want a b a", h.entries)
	}
}

func TestHistoryAddEndsNavigation(t *testing.T) {
	h := newHistory("a", "b")
	h.prev("")
	h.add("c")
	if h.navigating() {
		t.Error("add should end navigation")
	}
	if got, _ := h.prev(""); got != "c" {
		t.Errorf("prev after add = %q, want c", got)
	}
}
