package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

type rootCfg struct {
	Provider string   `mapstructure:"provider"`
	Model    string   `mapstructure:"model"`
	Mode     string   `mapstructure:"mode" validate:"oneof=text json"`
	Tools    []string `mapstructure:"tools"`
}

type authCfg struct {
	BaseURL string  `mapstructure:"base_url" validate:"omitempty,url"`
	Retries int     `mapstructure:"retries" validate:"min=0,max=5"`
	Probe   probe   `mapstructure:"probe"`
	Secret  string  `mapstructure:"-"`
	Extra   *string `mapstructure:"extra"`
}

type probe struct {
	TimeoutSeconds int `mapstructure:"timeout_seconds" validate:"gt=0"`
}

func writeConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(AgentDirEnv, dir)
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newFlags() *pflag.FlagSet {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("provider", "", "")
	fs.String("model", "", "")
	fs.String("mode", "text", "")
	fs.StringSlice("tools", nil, "")
	fs.Bool("print", false, "not a config key")
	return fs
}

func TestDefaultsWhenFileMissing(t *testing.T) {
	t.Setenv(AgentDirEnv, t.TempDir())

	cfg := rootCfg{Mode: "json"}
	if err := Load("", &cfg, WithFlags(newFlags())); err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "json" {
		t.Errorf("Mode = %q, want struct default to beat the flag default", cfg.Mode)
	}
}

func TestSectionsAreIndependent(t *testing.T) {
	writeConfig(t, `
provider = "openai"
tools = ["read", "bash"]

[auth]
base_url = "http://localhost:11434"
retries = 2

[auth.probe]
timeout_seconds = 7
`)

	root := rootCfg{Mode: "text"}
	if err := Load("", &root); err != nil {
		t.Fatal(err)
	}
	if root.Provider != "openai" || !reflect.DeepEqual(root.Tools, []string{"read", "bash"}) {
		t.Errorf("root = %+v", root)
	}

	auth := authCfg{Probe: probe{TimeoutSeconds: 2}}
	if err := Load("auth", &auth); err != nil {
		t.Fatal(err)
	}
	if auth.BaseURL != "http://localhost:11434" || auth.Retries != 2 || auth.Probe.TimeoutSeconds != 7 {
		t.Errorf("auth = %+v", auth)
	}
}

func TestPrecedence(t *testing.T) {
	writeConfig(t, `
provider = "from-file"
model = "from-file"
`)
	t.Setenv("PI_GO_PROVIDER", "from-env")
	t.Setenv("PI_GO_MODEL", "from-env")

	fs := newFlags()
	if err := fs.Parse([]string{"--model", "from-flag"}); err != nil {
		t.Fatal(err)
	}

	cfg := rootCfg{Mode: "text"}
	if err := Load("", &cfg, WithFlags(fs)); err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "from-flag" {
		t.Errorf("Model = %q, want flag to win", cfg.Model)
	}
	if cfg.Provider != "from-env" {
		t.Errorf("Provider = %q, want env to beat file", cfg.Provider)
	}
}

func TestEnvOverridesKeyAbsentFromFile(t *testing.T) {
	t.Setenv(AgentDirEnv, t.TempDir())
	t.Setenv("PI_GO_TOOLS", "read,write")
	t.Setenv("PI_GO_AUTH_BASE_URL", "http://example.com")
	t.Setenv("PI_GO_AUTH_PROBE_TIMEOUT_SECONDS", "9")

	root := rootCfg{Mode: "text"}
	if err := Load("", &root); err != nil {
		t.Fatal(err)
	}
	if want := []string{"read", "write"}; !reflect.DeepEqual(root.Tools, want) {
		t.Errorf("Tools = %v, want %v", root.Tools, want)
	}

	auth := authCfg{}
	if err := Load("auth", &auth); err != nil {
		t.Fatal(err)
	}
	if auth.BaseURL != "http://example.com" || auth.Probe.TimeoutSeconds != 9 {
		t.Errorf("auth = %+v", auth)
	}
}

func TestValidation(t *testing.T) {
	writeConfig(t, `
mode = "yaml"

[auth]
base_url = "not a url"
retries = 9
`)

	err := Load("", &rootCfg{Mode: "text"})
	if err == nil || !strings.Contains(err.Error(), `mode: failed "oneof=text json"`) {
		t.Errorf("root error = %v", err)
	}

	err = Load("auth", &authCfg{Probe: probe{TimeoutSeconds: 1}})
	if err == nil {
		t.Fatal("want validation error")
	}
	for _, want := range []string{"[auth]", `base_url: failed "url"`, `retries: failed "max=5"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "not a url") {
		t.Errorf("error leaks the value: %v", err)
	}
}

func TestMalformedFile(t *testing.T) {
	writeConfig(t, "provider = ")

	if err := Load("", &rootCfg{Mode: "text"}); err == nil {
		t.Fatal("want error for malformed TOML")
	}
}

func TestLoadRejectsNonStructPointer(t *testing.T) {
	if err := Load("", rootCfg{}); err == nil {
		t.Fatal("want error for non-pointer")
	}
}
