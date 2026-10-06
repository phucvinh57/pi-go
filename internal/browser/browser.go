// Package browser opens a URL or a file in the user's browser.
package browser

import (
	"os/exec"
	"runtime"
)

// Open is a variable so tests do not launch a browser. It starts the system's
// opener without waiting for it and reports only whether that could be started:
// whether a browser then shows the page is beyond what can be known, so callers
// also tell the user where the page is.
var Open = open

func open(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap it
	return nil
}
