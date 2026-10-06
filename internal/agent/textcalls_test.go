package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/phucvinh57/pi-go/internal/ai"
	"github.com/phucvinh57/pi-go/internal/tools"
)

func TestRecoverToolCalls(t *testing.T) {
	known := map[string]bool{"bash": true, "read": true}

	for _, tc := range []struct {
		name      string
		text      string
		wantText  string
		wantCalls []string // "name args"
	}{
		{
			name:      "bare json from a real session",
			text:      `{"name": "bash", "arguments": {"command": "ls -l"}}`,
			wantCalls: []string{`bash {"command": "ls -l"}`},
		},
		{
			name:      "prose then call, parameters key",
			text:      "Let me look.\n\n{\"name\": \"bash\", \"parameters\": {\"command\":\"ls\"}}",
			wantText:  "Let me look.",
			wantCalls: []string{`bash {"command":"ls"}`},
		},
		{
			name:      "fenced",
			text:      "```json\n{\"name\":\"read\",\"arguments\":{\"path\":\"a.go\"}}\n```",
			wantCalls: []string{`read {"path":"a.go"}`},
		},
		{
			name:      "tags, two calls",
			text:      "<tool_call>\n{\"name\":\"bash\",\"arguments\":{\"command\":\"pwd\"}}\n</tool_call>\n<tool_call>\n{\"name\":\"read\",\"arguments\":{\"path\":\"x\"}}\n</tool_call>",
			wantCalls: []string{`bash {"command":"pwd"}`, `read {"path":"x"}`},
		},
		{
			name:      "arguments as a string",
			text:      `{"name":"bash","arguments":"{\"command\":\"ls\"}"}`,
			wantCalls: []string{`bash {"command":"ls"}`},
		},
		{name: "unknown tool", text: `{"name":"teleport","arguments":{}}`, wantText: `{"name":"teleport","arguments":{}}`},
		{name: "no arguments object", text: `{"name":"bash"}`, wantText: `{"name":"bash"}`},
		{name: "plain answer", text: "ls lists files.", wantText: "ls lists files."},
		{
			name:     "quoted in the middle of an explanation",
			text:     `Call it like {"name":"bash","arguments":{"command":"ls"}} and you get a listing.`,
			wantText: `Call it like {"name":"bash","arguments":{"command":"ls"}} and you get a listing.`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seq := 0
			msg := recoverToolCalls(say(tc.text), known, &seq)

			if got := msg.Text(); got != tc.wantText {
				t.Errorf("text = %q, want %q", got, tc.wantText)
			}
			var got []string
			ids := map[string]bool{}
			for _, c := range msg.ToolCalls() {
				got = append(got, c.Name+" "+string(c.Arguments))
				ids[c.ID] = true
			}
			if strings.Join(got, "|") != strings.Join(tc.wantCalls, "|") {
				t.Errorf("calls = %q, want %q", got, tc.wantCalls)
			}
			if len(ids) != len(tc.wantCalls) {
				t.Errorf("call IDs are not unique: %v", ids)
			}
			if wantStop := map[bool]ai.StopReason{true: ai.StopToolUse, false: ai.StopStop}[len(tc.wantCalls) > 0]; msg.StopReason != wantStop {
				t.Errorf("stop reason = %s, want %s", msg.StopReason, wantStop)
			}
		})
	}
}

func TestRecoverLeavesStructuredCallsAlone(t *testing.T) {
	seq := 0
	msg := callTool("c1", "bash", map[string]any{"command": "ls"})
	msg.Content = append(msg.Content, ai.Block{Type: ai.BlockText, Text: `{"name":"bash","arguments":{"command":"rm -rf /"}}`})
	if got := recoverToolCalls(msg, map[string]bool{"bash": true}, &seq); len(got.ToolCalls()) != 1 {
		t.Errorf("calls = %d, want the one structured call", len(got.ToolCalls()))
	}
}

// The session that motivated this: the model answers with a call written as
// text. The loop must run it and give the model the output.
func TestLoopRunsTextFormCall(t *testing.T) {
	dir := t.TempDir()
	p := &script{replies: []ai.Message{
		say(`{"name": "bash", "arguments": {"command": "echo from-text-call"}}`),
		say("done"),
	}}
	a := newAgent(p, dir, 0)

	reply, err := a.Prompt(context.Background(), "list this directory")
	if err != nil || reply.Text() != "done" {
		t.Fatalf("got %q, %v", reply.Text(), err)
	}
	msgs := p.requests[1].Messages
	if len(msgs) != 3 || msgs[1].ToolCalls()[0].Name != "bash" || msgs[2].Text() != "from-text-call\n" {
		t.Errorf("second request = %+v", msgs)
	}
}

func TestSystemPromptTellsModelToActOnItsOwn(t *testing.T) {
	got := SystemPrompt(tools.Core("/w"), "/w", testNow)
	for _, want := range []string{"instead of asking the user", "never write a tool call as text"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
