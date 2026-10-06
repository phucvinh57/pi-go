package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Session is a session file read back.
type Session struct {
	Path    string
	Header  Header
	Entries []Entry
	// Skipped counts the lines that could not be read: a half-written last line
	// after a crash, or damage. They are left out, not fatal.
	Skipped int
}

// Load reads one session file. A line that is not valid is skipped and counted;
// a file whose first line is not a session header is an error.
func Load(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	s := &Session{Path: path}
	r := bufio.NewReader(f)
	first := true
	for {
		// ReadBytes, not a Scanner: a tool result can be far longer than a
		// Scanner's line limit.
		line, err := r.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			if first {
				if err := json.Unmarshal(line, &s.Header); err != nil || s.Header.Type != TypeSession {
					return nil, fmt.Errorf("%s: not a session file (no header)", path)
				}
				first = false
			} else {
				var e Entry
				if json.Unmarshal(line, &e) != nil || e.Type == "" {
					s.Skipped++
				} else {
					s.Entries = append(s.Entries, e)
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	if first {
		return nil, fmt.Errorf("%s: empty session file", path)
	}
	return s, nil
}

// List returns the session files under agentDir, in every project's directory.
// A missing sessions directory means there are none.
func List(agentDir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(agentDir, "sessions", "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(agentDir, "sessions")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

// LoadAll reads every session under agentDir, oldest first. A file that cannot
// be read does not stop the others: the sessions that did load are returned
// together with an error that names the ones that did not.
func LoadAll(agentDir string) ([]*Session, error) {
	paths, err := List(agentDir)
	if err != nil {
		return nil, err
	}
	var (
		sessions []*Session
		errs     []error
	)
	for _, p := range paths {
		s, err := Load(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		sessions = append(sessions, s)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].Header.Time().Before(sessions[j].Header.Time())
	})
	return sessions, errors.Join(errs...)
}
