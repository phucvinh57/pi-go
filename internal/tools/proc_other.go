//go:build !unix

package tools

import (
	"os"
	"os/exec"
)

// killGroupOnCancel is a no-op here: exec.CommandContext already kills the
// shell itself. Children it started may outlive it.
func killGroupOnCancel(*exec.Cmd) {}

func exitCode(ps *os.ProcessState) int {
	if ps == nil {
		return 1
	}
	return ps.ExitCode()
}
