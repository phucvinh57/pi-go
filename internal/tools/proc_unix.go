//go:build unix

package tools

import (
	"os"
	"os/exec"
	"syscall"
)

// killGroupOnCancel runs the command in its own process group and, when its
// context ends, kills the whole group. Killing only the shell would leave the
// commands it started (a dev server, a sleep) running.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// exitCode is the shell convention: 128+N for a process killed by signal N.
func exitCode(ps *os.ProcessState) int {
	if ps == nil {
		return 1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}
