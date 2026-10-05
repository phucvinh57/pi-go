package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type write struct{ cwd string }

// NewWrite returns the write tool.
func NewWrite(cwd string) Tool { return write{cwd} }

func (write) Spec() Spec {
	return Spec{
		Name: "write",
		Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. " +
			"Automatically creates parent directories.",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the file to write (relative or absolute)"},
    "content": {"type": "string", "description": "Content to write to the file"}
  },
  "required": ["path", "content"]
}`),
		Snippet:    "Create or overwrite files",
		Guidelines: []string{"Use write only for new files or complete rewrites."},
	}
}

func (w write) Execute(ctx context.Context, raw json.RawMessage, _ func(Result)) (Result, error) {
	var a struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return Result{}, err
	}
	if a.Path == "" {
		return Result{}, errors.New("path is required")
	}
	if a.Content == nil {
		return Result{}, errors.New("content is required")
	}

	path := resolvePath(a.Path, w.cwd)
	return withFileLock(path, func() (Result, error) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(path, []byte(*a.Content), 0o644); err != nil {
			return Result{}, err
		}
		return Result{Text: fmt.Sprintf("Successfully wrote %d bytes to %s", len(*a.Content), a.Path)}, nil
	})
}
