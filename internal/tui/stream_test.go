package tui

import "testing"

func TestSummarizeArgs(t *testing.T) {
	tests := []struct{ args, want string }{
		{`{"command":"go  test\n./..."}`, "$ go test ./..."},
		{`{"path":"a/b.go","content":"x"}`, "a/b.go"},
		{`{}`, ""},
		{`{"q":1}`, `{"q":1}`},
		{`not json`, "not json"},
	}
	for _, tt := range tests {
		if got := summarizeArgs(tt.args); got != tt.want {
			t.Errorf("summarizeArgs(%s) = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestCleanStripsEscapesAndTabs(t *testing.T) {
	if got := clean("\x1b[31mred\x1b[0m\tx\r\n"); got != "red    x\n" {
		t.Errorf("clean = %q", got)
	}
}
