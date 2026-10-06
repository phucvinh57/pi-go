package cli

import (
	"os"

	"github.com/phucvinh57/pi-go/internal/auth"
	"github.com/phucvinh57/pi-go/internal/config"
	"github.com/phucvinh57/pi-go/internal/settings"
)

// environment is what a session reads from the machine: where its files live
// and where the agent works. cli reads it once, at startup, and hands it down,
// so nothing below looks at the working directory or the agent directory on
// its own and a test can build a session on a temporary directory.
type environment struct {
	agentDir string // auth.json, models.json, settings.toml and sessions/
	cwd      string // the project the agent works in
	home     string // for showing cwd as ~/...; "" when unknown
}

// hostEnvironment reads the environment of the running process. A working
// directory that cannot be read is an error only when an agent needs it, so
// `pi-go --help` and `auth` still work.
func hostEnvironment() environment {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	home, _ := os.UserHomeDir()
	return environment{agentDir: config.AgentDir(), cwd: cwd, home: home}
}

func (e environment) auth() *auth.Store         { return auth.NewStore(e.agentDir) }
func (e environment) settings() *settings.Store { return settings.NewStore(e.agentDir) }
