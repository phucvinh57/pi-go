package cli

type mode int

const (
	modeInteractive mode = iota
	modePrint
)

// chooseMode picks how the root command runs. A session needs a terminal on
// both ends; otherwise (or with -p) pi-go answers once and exits.
func chooseMode(print, stdinTTY, stdoutTTY bool) mode {
	if print || !stdinTTY || !stdoutTTY {
		return modePrint
	}
	return modeInteractive
}
