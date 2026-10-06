// Package settings reads and writes <agent dir>/settings.toml: the choices a
// user makes from inside pi-go (today, the default model), as opposed to
// config.toml, which holds hand-written module configuration. It also reads the
// [auto] table, which tunes auto routing and is only ever written by hand.
//
// Writes patch the keys this package owns in the raw table and keep every other
// key, so a setting added by hand or by a newer version survives. Comments in
// the file are not kept.
package settings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	fileName        = "settings.toml"
	keyDefaultModel = "default_model"
	keyAuto         = "auto"
)

// Store is the settings.toml of one agent directory.
type Store struct {
	dir string
}

// NewStore returns the settings of the agent directory dir.
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Path returns the location of settings.toml.
func (s *Store) Path() string { return filepath.Join(s.dir, fileName) }

// DefaultModel returns the saved default model ("provider/id"), or "" if none
// is set. A missing file is not an error; a malformed one is.
func (s *Store) DefaultModel() (string, error) {
	table, err := s.read()
	if err != nil {
		return "", err
	}
	v, ok := table[keyDefaultModel]
	if !ok {
		return "", nil
	}
	ref, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: %s must be a string", s.Path(), keyDefaultModel)
	}
	return ref, nil
}

// SetDefaultModel saves ref as the default model, creating the file and the
// agent directory if needed.
func (s *Store) SetDefaultModel(ref string) error {
	if strings.TrimSpace(ref) == "" {
		return errors.New("default model is empty")
	}
	table, err := s.read()
	if err != nil {
		return err
	}
	if table == nil {
		table = map[string]any{}
	}
	table[keyDefaultModel] = ref
	return s.write(table)
}

// Auto is the [auto] table: how the model and effort are picked when they are
// "auto". A zero field is unset, and the router uses its default.
type Auto struct {
	// Classifier is the classifier model ("provider/id") that rates each
	// prompt; "none" turns it off, leaving only the local heuristics.
	Classifier string
	// Models, when set, are the only models auto may pick, weakest first.
	Models []string
	// MinPromptChars is the length under which a prompt keeps the model in use.
	MinPromptChars int
	// TimeoutMS bounds the classifier call, in milliseconds.
	TimeoutMS int
}

// Auto returns the [auto] table. A missing file or table is not an error; a
// value of the wrong type is.
func (s *Store) Auto() (Auto, error) {
	table, err := s.read()
	if err != nil {
		return Auto{}, err
	}
	raw, ok := table[keyAuto]
	if !ok {
		return Auto{}, nil
	}
	t, ok := raw.(map[string]any)
	if !ok {
		return Auto{}, fmt.Errorf("%s: [%s] must be a table", s.Path(), keyAuto)
	}
	var a Auto
	wrong := func(key, want string) error {
		return fmt.Errorf("%s: %s.%s must be %s", s.Path(), keyAuto, key, want)
	}
	if v, ok := t["classifier"]; ok {
		if a.Classifier, ok = v.(string); !ok {
			return Auto{}, wrong("classifier", "a string")
		}
	}
	if v, ok := t["models"]; ok {
		list, ok := v.([]any)
		if !ok {
			return Auto{}, wrong("models", "a list of strings")
		}
		for _, item := range list {
			ref, ok := item.(string)
			if !ok {
				return Auto{}, wrong("models", "a list of strings")
			}
			a.Models = append(a.Models, ref)
		}
	}
	for key, dst := range map[string]*int{"min_prompt_chars": &a.MinPromptChars, "timeout_ms": &a.TimeoutMS} {
		v, ok := t[key]
		if !ok {
			continue
		}
		n, ok := v.(int64)
		if !ok || n < 0 {
			return Auto{}, wrong(key, "a non-negative integer")
		}
		*dst = int(n)
	}
	return a, nil
}

func (s *Store) read() (map[string]any, error) {
	data, err := os.ReadFile(s.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.Path(), err)
	}
	var table map[string]any
	if err := toml.Unmarshal(data, &table); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.Path(), err)
	}
	return table, nil
}

// write replaces the file through a temporary file in the same directory, so a
// crash never leaves half a file behind.
func (s *Store) write(table map[string]any) error {
	data, err := toml.Marshal(table)
	if err != nil {
		return fmt.Errorf("encode %s: %w", s.Path(), err)
	}
	dir := filepath.Dir(s.Path())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, fileName+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", s.Path(), err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", s.Path(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", s.Path(), err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", s.Path(), err)
	}
	if err := os.Rename(tmp.Name(), s.Path()); err != nil {
		return fmt.Errorf("write %s: %w", s.Path(), err)
	}
	return nil
}
