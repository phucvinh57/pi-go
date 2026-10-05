package tui

import "strings"

// history is the in-session list of submitted inputs, oldest first, with
// shell-style navigation filtered by the draft's prefix.
type history struct {
	entries []string
	pos     int    // index being shown; len(entries) when not navigating
	draft   string // editor text when navigation began; also the prefix
}

// add records a submitted input and ends any navigation. An input equal to the
// previous one is not recorded twice.
func (h *history) add(s string) {
	if n := len(h.entries); n == 0 || h.entries[n-1] != s {
		h.entries = append(h.entries, s)
	}
	h.reset()
}

func (h *history) navigating() bool { return h.pos < len(h.entries) }

// prev returns the next older entry that starts with the draft. current is the
// editor text; on the first call it becomes the draft.
func (h *history) prev(current string) (string, bool) {
	if !h.navigating() {
		h.draft = current
	}
	for i := h.pos - 1; i >= 0; i-- {
		if e := h.entries[i]; strings.HasPrefix(e, h.draft) && e != current {
			h.pos = i
			return e, true
		}
	}
	return "", false
}

// next returns the next newer entry that starts with the draft, or the draft
// itself once past the newest, which ends navigation.
func (h *history) next(current string) (string, bool) {
	if !h.navigating() {
		return "", false
	}
	for i := h.pos + 1; i < len(h.entries); i++ {
		if e := h.entries[i]; strings.HasPrefix(e, h.draft) && e != current {
			h.pos = i
			return e, true
		}
	}
	draft := h.draft
	h.reset()
	return draft, true
}

func (h *history) reset() {
	h.pos = len(h.entries)
	h.draft = ""
}
