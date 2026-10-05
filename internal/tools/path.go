package tools

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

// resolvePath turns a path from the model into an absolute one. It expands a
// leading "~", drops the "@" some models prefix to file references, replaces
// exotic Unicode spaces (they sneak in from pasted text) with plain ones, and
// resolves relative paths against cwd.
func resolvePath(p, cwd string) string {
	p = strings.Map(func(r rune) rune {
		if r != ' ' && unicode.Is(unicode.Zs, r) {
			return ' '
		}
		return r
	}, p)
	p = strings.TrimPrefix(p, "@")
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p)
}

// fileLocks serialises writers of the same file. A model may emit several tool
// calls in one turn, and two edits to one file must not interleave their
// read-modify-write cycles.
var fileLocks = struct {
	mu sync.Mutex
	m  map[string]*fileLock
}{m: map[string]*fileLock{}}

type fileLock struct {
	mu   sync.Mutex
	refs int
}

// withFileLock runs fn while holding the lock for path. The key follows
// symlinks, so two spellings of one file share a lock.
func withFileLock(path string, fn func() (Result, error)) (Result, error) {
	key := path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		key = real
	}

	fileLocks.mu.Lock()
	l := fileLocks.m[key]
	if l == nil {
		l = &fileLock{}
		fileLocks.m[key] = l
	}
	l.refs++
	fileLocks.mu.Unlock()

	l.mu.Lock()
	defer func() {
		l.mu.Unlock()
		fileLocks.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(fileLocks.m, key)
		}
		fileLocks.mu.Unlock()
	}()
	return fn()
}
