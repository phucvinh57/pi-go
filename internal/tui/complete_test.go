package tui

import (
	"reflect"
	"testing"
)

func names(ss []suggestion) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}

func TestSuggest(t *testing.T) {
	cmds := &fakeCommands{}

	tests := []struct {
		input string
		want  []string
	}{
		{"/", []string{"clear", "echo", "effort", "grp", "help", "model", "plan", "quit", "session"}}, // no hidden "exit"
		{"/e", []string{"echo", "effort"}},
		{"/qui", []string{"quit"}},
		{"/grp ", []string{"grp sub"}},
		{"/grp s", []string{"grp sub"}},
		{"/nope ", nil},
		{"/echo --j", nil},
		{"/quit ", nil},
		{"hello", nil},
		{"/a\nb", nil},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := names(suggest(cmds, tt.input)); !reflect.DeepEqual(got, tt.want) && (len(got) > 0 || len(tt.want) > 0) {
				t.Errorf("suggest(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
