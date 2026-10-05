// Package config loads module settings from <agent dir>/config.toml.
//
// The package knows nothing about any module's settings. Each module declares
// its own struct, tagged with `mapstructure` (the TOML key) and `validate`
// (go-playground/validator rules), and loads it from its own table:
//
//	type Config struct {
//		BaseURL string `mapstructure:"base_url" validate:"omitempty,url"`
//	}
//
//	cfg := Config{BaseURL: "http://localhost:11434"} // defaults
//	err := config.Load("auth", &cfg)                 // reads [auth] in config.toml
//
// Precedence, highest first: command-line flag (see WithFlags), environment
// variable, config.toml, the defaults already in the struct. The environment
// variable for key base_url in section auth is PI_GO_AUTH_BASE_URL; the root
// section ("") uses the bare PI_GO_ prefix.
//
// Credentials do not belong here; they stay in auth.json.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// AgentDirEnv overrides the agent directory.
const AgentDirEnv = "PI_GO_CODING_AGENT_DIR"

const (
	fileName  = "config.toml"
	envPrefix = "PI_GO"
	tagName   = "mapstructure"
)

// AgentDir returns the directory holding config.toml, auth.json and models.json.
func AgentDir() string {
	if dir := os.Getenv(AgentDirEnv); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".pi-go", "agent")
	}
	return filepath.Join(home, ".pi-go", "agent")
}

// Option customizes Load.
type Option func(*loadOptions)

type loadOptions struct {
	flags *pflag.FlagSet
}

// WithFlags lets flags the user set on the command line override every other
// source. A flag binds to the struct field whose key equals the flag name with
// "-" replaced by "_" (--exclude-tools sets exclude_tools); flags with no
// matching field are ignored.
func WithFlags(flags *pflag.FlagSet) Option {
	return func(o *loadOptions) { o.flags = flags }
}

var validate = newValidator()

func newValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	// Report errors by TOML key, not Go field name.
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		return keyName(f)
	})
	return v
}

// Load fills dst, a pointer to a struct, from the given section of config.toml
// ("" is the top level) and validates it. Values already in dst are the
// defaults. A missing config.toml is not an error; a malformed file or an
// invalid value is.
func Load(section string, dst any, opts ...Option) error {
	var o loadOptions
	for _, opt := range opts {
		opt(&o)
	}

	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config: Load needs a non-nil struct pointer, got %T", dst)
	}

	path := filepath.Join(AgentDir(), fileName)
	table, err := readSection(path, section)
	if err != nil {
		return err
	}

	v := viper.New()
	if err := v.MergeConfigMap(table); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	prefix := envPrefix
	if section != "" {
		prefix += "_" + section
	}
	v.SetEnvPrefix(prefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))

	// Viper only reports keys it has been told about, so declare every key in
	// the schema: that is what makes env overrides work for keys absent from
	// the file, and keeps defaults ahead of a flag's own default.
	keys := map[string]bool{}
	if err := declareKeys(v, rv.Elem(), "", keys); err != nil {
		return err
	}

	if o.flags != nil {
		var bindErr error
		o.flags.VisitAll(func(f *pflag.Flag) {
			key := strings.ReplaceAll(f.Name, "-", "_")
			if bindErr == nil && keys[key] {
				bindErr = v.BindPFlag(key, f)
			}
		})
		if bindErr != nil {
			return fmt.Errorf("bind flags: %w", bindErr)
		}
	}

	if err := v.Unmarshal(dst); err != nil {
		return fmt.Errorf("parse %s: %w", describe(path, section), err)
	}
	if err := validate.Struct(dst); err != nil {
		return invalid(path, section, err)
	}
	return nil
}

// readSection returns one table of the config file, or the whole top level.
func readSection(path, section string) (map[string]any, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("toml")
	if err := v.ReadInConfig(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if section == "" {
		return v.AllSettings(), nil
	}
	return v.GetStringMap(section), nil
}

// declareKeys registers each leaf field of s with viper (env binding and, for
// non-zero fields, a default) and records its key.
func declareKeys(v *viper.Viper, s reflect.Value, parent string, keys map[string]bool) error {
	t := s.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := keyName(f)
		if name == "-" {
			continue
		}
		key := name
		if parent != "" {
			key = parent + "." + name
		}

		if f.Type.Kind() == reflect.Struct {
			if err := declareKeys(v, s.Field(i), key, keys); err != nil {
				return err
			}
			continue
		}

		keys[key] = true
		if err := v.BindEnv(key); err != nil {
			return fmt.Errorf("bind env for %s: %w", key, err)
		}
		if fv := s.Field(i); !fv.IsZero() {
			v.SetDefault(key, fv.Interface())
		}
	}
	return nil
}

// keyName is the TOML key of a struct field: its mapstructure tag, else the
// lower-cased field name.
func keyName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get(tagName), ",")
	if name == "" {
		return strings.ToLower(f.Name)
	}
	return name
}

func describe(path, section string) string {
	if section == "" {
		return path
	}
	return fmt.Sprintf("%s [%s]", path, section)
}

// invalid turns validator errors into one readable error. Values are left out
// on purpose: a setting that fails validation may be a secret.
func invalid(path, section string, err error) error {
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return fmt.Errorf("validate %s: %w", describe(path, section), err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "invalid config in %s:", describe(path, section))
	for _, fe := range verrs {
		// Namespace is "<StructType>.<key>[.<key>...]"; drop the type name.
		_, key, _ := strings.Cut(fe.Namespace(), ".")
		rule := fe.Tag()
		if fe.Param() != "" {
			rule += "=" + fe.Param()
		}
		fmt.Fprintf(&b, "\n  %s: failed %q", key, rule)
	}
	return errors.New(b.String())
}
