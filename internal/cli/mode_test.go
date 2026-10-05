package cli

import "testing"

func TestChooseMode(t *testing.T) {
	tests := []struct {
		name                    string
		print, stdinTTY, outTTY bool
		want                    mode
	}{
		{"terminal both ends", false, true, true, modeInteractive},
		{"-p forces print", true, true, true, modePrint},
		{"piped stdin", false, false, true, modePrint},
		{"piped stdout", false, true, false, modePrint},
		{"nothing is a terminal", false, false, false, modePrint},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chooseMode(tt.print, tt.stdinTTY, tt.outTTY); got != tt.want {
				t.Errorf("chooseMode = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJoinPrompt(t *testing.T) {
	tests := []struct{ piped, arg, want string }{
		{"", "hi", "hi"},
		{"data\n", "", "data"},
		{"data\n", "summarize", "data\n\nsummarize"},
	}
	for _, tt := range tests {
		if got := joinPrompt(tt.piped, tt.arg); got != tt.want {
			t.Errorf("joinPrompt(%q, %q) = %q, want %q", tt.piped, tt.arg, got, tt.want)
		}
	}
}
