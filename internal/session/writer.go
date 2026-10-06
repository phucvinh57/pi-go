package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// Writer saves one session as it happens. It satisfies agent.Recorder.
//
// Like PI's, it writes nothing until the conversation has a user or assistant
// message, so starting pi-go and quitting leaves no file behind. After that
// each entry is appended as its own line, so a crash loses at most the line
// being written.
//
// A Writer never fails the conversation: the first write error is kept (see
// Err) and later entries are dropped.
type Writer struct {
	mu  sync.Mutex
	dir string
	now func() time.Time

	header  Header
	pending []Entry // entries not yet on disk
	file    *os.File
	lastID  *string // the entry the next one is a child of
	err     error
	closed  bool
}

// NewWriter starts a session for cwd, to be saved under agentDir. now is the
// clock; nil means time.Now.
func NewWriter(agentDir, cwd string, now func() time.Time) *Writer {
	if now == nil {
		now = time.Now
	}
	t := now()
	return &Writer{
		dir: Dir(agentDir, cwd),
		now: now,
		header: Header{
			Type:      TypeSession,
			Version:   Version,
			ID:        newSessionID(t),
			Timestamp: formatTime(t),
			CWD:       cwd,
		},
	}
}

// ID is the session's ID.
func (w *Writer) ID() string { return w.header.ID }

// Path is where the session is, or will be, saved.
func (w *Writer) Path() string { return filepath.Join(w.dir, fileName(w.header)) }

// Record saves a message of the conversation.
func (w *Writer) Record(m ai.Message) {
	w.append(Entry{Type: TypeMessage, Message: &m})
}

// ModelChange notes the model the conversation now uses.
func (w *Writer) ModelChange(provider, id string) {
	w.append(Entry{Type: TypeModelChange, Provider: provider, ModelID: id})
}

// Err is the first error that stopped the session being saved, or nil.
func (w *Writer) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Close releases the file. Entries recorded afterwards are dropped.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *Writer) append(e Entry) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.err != nil {
		return
	}

	id := newEntryID()
	e.ID, e.ParentID, e.Timestamp = id, w.lastID, formatTime(w.now())
	w.lastID = &id
	w.pending = append(w.pending, e)

	if w.file == nil && !w.hasConversation() {
		return
	}
	if err := w.flush(); err != nil {
		w.err = fmt.Errorf("save session %s: %w", w.Path(), err)
		w.pending = nil
	}
}

// hasConversation reports whether something the user or the model said is
// waiting to be written.
func (w *Writer) hasConversation() bool {
	for _, e := range w.pending {
		if e.Type != TypeMessage || e.Message == nil {
			continue
		}
		if r := e.Message.Role; r == ai.RoleUser || r == ai.RoleAssistant {
			return true
		}
	}
	return false
}

// flush writes the header (the first time) and every pending entry.
func (w *Writer) flush() error {
	if w.file == nil {
		if err := os.MkdirAll(w.dir, 0o700); err != nil {
			return err
		}
		// O_EXCL: the name holds a fresh UUID, so a clash means something is
		// wrong, and silently appending to someone else's file would be worse.
		f, err := os.OpenFile(w.Path(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		w.file = f
		if err := writeLine(f, w.header); err != nil {
			return err
		}
	}
	for _, e := range w.pending {
		if err := writeLine(w.file, e); err != nil {
			return err
		}
	}
	w.pending = w.pending[:0]
	return nil
}

func writeLine(f *os.File, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}
