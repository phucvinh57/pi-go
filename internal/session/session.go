// Package session saves conversations to disk and reads them back, so usage can
// be looked at after the fact.
//
// A session is one JSONL file, laid out like PI's: a header line, then one line
// per entry, each linked to the one before it by parentId:
//
//	<agent dir>/sessions/--<cwd>--/<timestamp>_<uuid>.jsonl
//
// The messages are pi-go's own ai.Message, so a file is meant to be read by
// pi-go, not by PI. Files are append-only and never rewritten.
package session

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// Version is the file format version written to the header.
const Version = 3

// Entry types.
const (
	TypeSession     = "session" // the header
	TypeMessage     = "message"
	TypeModelChange = "model_change"
	// TypeCharge is a billed call outside the conversation, such as the
	// classifier auto routing asks: Provider, ModelID and Usage.
	TypeCharge = "charge"
)

// timeLayout is how timestamps are written: ISO 8601 in UTC with milliseconds.
const timeLayout = "2006-01-02T15:04:05.000Z"

// Header is the first line of a session file.
type Header struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

// Entry is one line after the header. Which fields apply depends on Type.
type Entry struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"` // nil for the first entry
	Timestamp string  `json:"timestamp"`

	// TypeMessage.
	Message *ai.Message `json:"message,omitempty"`

	// TypeModelChange and TypeCharge.
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"modelId,omitempty"`

	// TypeCharge.
	Usage *ai.Usage `json:"usage,omitempty"`
}

// Time parses the entry's timestamp; the zero time if it is not valid.
func (e Entry) Time() time.Time { return parseTime(e.Timestamp) }

// Time parses the header's timestamp; the zero time if it is not valid.
func (h Header) Time() time.Time { return parseTime(h.Timestamp) }

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// Dir returns the directory that holds the sessions started in cwd: PI's
// layout, the working directory with its separators turned into dashes.
func Dir(agentDir, cwd string) string {
	safe := strings.TrimLeft(cwd, `/\`)
	safe = strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(safe)
	return filepath.Join(agentDir, "sessions", "--"+safe+"--")
}

// fileName is the name of a session's file: the timestamp, made safe for file
// systems, and the ID.
func fileName(h Header) string {
	ts := strings.NewReplacer(":", "-", ".", "-").Replace(h.Timestamp)
	return ts + "_" + h.ID + ".jsonl"
}
