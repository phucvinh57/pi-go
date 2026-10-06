package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"pi-go/internal/config"
)

// AgentDir returns the directory holding auth.json and models.json.
func AgentDir() string { return config.AgentDir() }

// storedCredential is one auth.json entry: an API key ("api_key") or an OAuth
// token set ("oauth", Expires in Unix milliseconds).
type storedCredential struct {
	Type      string `json:"type"`
	Key       string `json:"key,omitempty"`
	Access    string `json:"access,omitempty"`
	Refresh   string `json:"refresh,omitempty"`
	Expires   int64  `json:"expires,omitempty"`
	AccountID string `json:"accountId,omitempty"`
}

type modelsFile struct {
	Providers map[string]struct {
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
		Models  []struct {
			ID string `json:"id"`
		} `json:"models"`
	} `json:"providers"`
}

func readAuthFile(dir string) (map[string]storedCredential, error) {
	var creds map[string]storedCredential
	if err := readJSON(filepath.Join(dir, "auth.json"), &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// saveCredential sets one provider's entry in auth.json. Other entries are kept
// byte for byte, including ones this package does not understand. The file is
// replaced atomically and is readable by the owner only.
func saveCredential(dir, provider string, c storedCredential) error {
	entries := map[string]json.RawMessage{}
	if err := readJSON(filepath.Join(dir, "auth.json"), &entries); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	entries[provider] = raw
	return writeAuthFile(dir, entries)
}

// removeCredential deletes one provider's entry from auth.json, keeping the
// others as they are. It reports whether there was an entry to delete; when
// there was none, the file is not touched.
func removeCredential(dir, provider string) (bool, error) {
	entries := map[string]json.RawMessage{}
	if err := readJSON(filepath.Join(dir, "auth.json"), &entries); err != nil {
		return false, err
	}
	if _, ok := entries[provider]; !ok {
		return false, nil
	}
	delete(entries, provider)
	return true, writeAuthFile(dir, entries)
}

// writeAuthFile replaces auth.json with entries, atomically and readable by
// the owner only.
func writeAuthFile(dir string, entries map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "auth-*.json.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, "auth.json"))
}

func readModelsFile(dir string) (modelsFile, error) {
	var models modelsFile
	err := readJSON(filepath.Join(dir, "models.json"), &models)
	return models, err
}

// readJSON decodes path into v. A missing file is not an error.
func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
