package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseServer replies to every request with the given SSE data lines and records
// the request.
type recorded struct {
	path    string
	headers http.Header
	body    map[string]any
}

func sseServer(t *testing.T, status int, lines ...string) (*httptest.Server, *recorded) {
	t.Helper()
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.path = r.URL.Path
		rec.headers = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &rec.body)
		if status != 200 {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":"nope"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			fmt.Fprintf(w, "data: %s\n\n", l)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func collect(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var evs []Event
	for ev := range ch {
		evs = append(evs, ev)
	}
	if len(evs) == 0 || !evs[len(evs)-1].Terminal() {
		t.Fatalf("stream did not end with a terminal event: %+v", evs)
	}
	return evs
}

func types(evs []Event) string {
	var s []string
	for _, e := range evs {
		s = append(s, string(e.Type))
	}
	return strings.Join(s, " ")
}

func TestCompletionsText(t *testing.T) {
	srv, rec := sseServer(t, 200,
		`{"choices":[{"delta":{"role":"assistant","content":""}}]}`,
		`{"choices":[{"delta":{"content":"Hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":2}}}`,
		`[DONE]`,
	)
	m, _ := NewModel("ollama", "qwen", srv.URL+"/v1")
	temp := 0.2
	evs := collect(t, wireProvider{completions{}}.Stream(context.Background(), m,
		Context{SystemPrompt: "be brief", Messages: []Message{UserText("hi")}},
		Options{APIKey: "k", MaxTokens: 50, Temperature: &temp}))

	if got, want := types(evs), "start text_start text_delta text_delta text_end done"; got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
	final := evs[len(evs)-1].Message
	if final.Text() != "Hello" || final.StopReason != StopStop {
		t.Fatalf("final = %+v", final)
	}
	if final.Usage != (Usage{Input: 10, Output: 3, CacheRead: 2, TotalTokens: 15}) {
		t.Fatalf("usage = %+v", final.Usage)
	}
	if final.Provider != "ollama" || final.Model != "qwen" {
		t.Fatalf("identity = %s/%s", final.Provider, final.Model)
	}

	if rec.path != "/v1/chat/completions" {
		t.Errorf("path = %s", rec.path)
	}
	if rec.headers.Get("Authorization") != "Bearer k" {
		t.Errorf("auth header = %q", rec.headers.Get("Authorization"))
	}
	msgs := rec.body["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[1].(map[string]any)["content"] != "hi" {
		t.Errorf("messages = %v", msgs)
	}
	if rec.body["stream"] != true || rec.body["max_tokens"] != float64(50) || rec.body["temperature"] != 0.2 {
		t.Errorf("body = %v", rec.body)
	}
}

func TestCompletionsToolCallFragments(t *testing.T) {
	srv, _ := sseServer(t, 200,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"read","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.go\"}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"bash","arguments":"{\"cmd\":\"ls\"}"}}]}}]}`,
		// Some servers say "stop" even when the turn ends in tool calls.
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)
	m, _ := NewModel("ollama", "qwen", srv.URL)
	evs := collect(t, wireProvider{completions{}}.Stream(context.Background(), m, Context{Messages: []Message{UserText("go")}}, Options{}))

	final := evs[len(evs)-1].Message
	calls := final.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].ID != "call_a" || calls[0].Name != "read" || string(calls[0].Arguments) != `{"path":"a.go"}` {
		t.Errorf("call 0 = %+v", calls[0])
	}
	if calls[1].ID != "call_b" || string(calls[1].Arguments) != `{"cmd":"ls"}` {
		t.Errorf("call 1 = %+v", calls[1])
	}
	if final.StopReason != StopToolUse {
		t.Errorf("stop = %s, want toolUse", final.StopReason)
	}
	var ends int
	for _, e := range evs {
		if e.Type == EventToolCallEnd {
			ends++
		}
	}
	if ends != 2 {
		t.Errorf("toolcall_end events = %d", ends)
	}
}

func TestCompletionsToolCallWithoutArgsOrID(t *testing.T) {
	srv, _ := sseServer(t, 200,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"now"}}]},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	)
	m, _ := NewModel("ollama", "qwen", srv.URL)
	evs := collect(t, wireProvider{completions{}}.Stream(context.Background(), m, Context{}, Options{}))
	call := evs[len(evs)-1].Message.ToolCalls()[0]
	if call.ID == "" || string(call.Arguments) != "{}" {
		t.Errorf("call = %+v", call)
	}
}

func TestCompletionsThinkingThenText(t *testing.T) {
	srv, _ := sseServer(t, 200,
		`{"choices":[{"delta":{"reasoning":"hm"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"m."}}]}`,
		`{"choices":[{"delta":{"content":"42"},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)
	m, _ := NewModel("ollama", "gpt-oss", srv.URL)
	evs := collect(t, wireProvider{completions{}}.Stream(context.Background(), m, Context{}, Options{}))
	if got, want := types(evs), "start thinking_start thinking_delta thinking_delta thinking_end text_start text_delta text_end done"; got != want {
		t.Fatalf("events = %q", got)
	}
	c := evs[len(evs)-1].Message.Content
	if c[0].Type != BlockThinking || c[0].Text != "hmm." || c[1].Text != "42" {
		t.Errorf("content = %+v", c)
	}
}

func TestCompletionsReplaysConversation(t *testing.T) {
	srv, rec := sseServer(t, 200, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, `[DONE]`)
	m, _ := NewModel("ollama", "qwen", srv.URL)

	assistant := Message{Role: RoleAssistant, StopReason: StopToolUse, Content: []Block{
		{Type: BlockThinking, Text: "secret thoughts"},
		{Type: BlockText, Text: "reading"},
		{Type: BlockToolCall, ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"x"}`)},
	}}
	failed := Message{Role: RoleAssistant, StopReason: StopError, Content: []Block{{Type: BlockText, Text: "partial"}}}
	tools := []Tool{{Name: "read", Description: "read a file", Parameters: json.RawMessage(`{"type":"object"}`)}}

	collect(t, wireProvider{completions{}}.Stream(context.Background(), m, Context{
		Messages: []Message{UserText("q"), failed, assistant, ToolResult("c1", "read", "contents", false)},
		Tools:    tools,
	}, Options{}))

	msgs := rec.body["messages"].([]any)
	if len(msgs) != 3 { // user, assistant, tool: the failed turn is dropped
		t.Fatalf("messages = %v", msgs)
	}
	a := msgs[1].(map[string]any)
	if a["content"] != "reading" {
		t.Errorf("assistant content = %v (thinking must not be replayed)", a["content"])
	}
	tc := a["tool_calls"].([]any)[0].(map[string]any)
	fn := tc["function"].(map[string]any)
	if tc["id"] != "c1" || tc["type"] != "function" || fn["name"] != "read" || fn["arguments"] != `{"path":"x"}` {
		t.Errorf("tool_calls = %v", tc)
	}
	tr := msgs[2].(map[string]any)
	if tr["role"] != "tool" || tr["tool_call_id"] != "c1" || tr["content"] != "contents" {
		t.Errorf("tool result = %v", tr)
	}
	decl := rec.body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if decl["name"] != "read" || decl["description"] != "read a file" {
		t.Errorf("tools = %v", rec.body["tools"])
	}
}

func TestHTTPErrorBecomesErrorEvent(t *testing.T) {
	srv, _ := sseServer(t, 401)
	m, _ := NewModel("ollama", "qwen", srv.URL)
	_, err := Complete(context.Background(), wireProvider{completions{}}, m, Context{}, Options{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnreachableServerBecomesErrorEvent(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	m, _ := NewModel("ollama", "qwen", url)
	msg, err := Complete(context.Background(), wireProvider{completions{}}, m, Context{}, Options{})
	if err == nil || msg.StopReason != StopError {
		t.Fatalf("msg=%+v err=%v", msg, err)
	}
}

func TestTruncatedStreamIsAnError(t *testing.T) {
	srv, _ := sseServer(t, 200, `{"choices":[{"delta":{"content":"par"}}]}`)
	m, _ := NewModel("ollama", "qwen", srv.URL)
	msg, err := Complete(context.Background(), wireProvider{completions{}}, m, Context{}, Options{})
	if err == nil || msg.StopReason != StopError {
		t.Fatalf("msg=%+v err=%v", msg, err)
	}
	if msg.Text() != "par" {
		t.Errorf("partial text lost: %q", msg.Text())
	}
}

func TestAbort(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"one\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	m, _ := NewModel("ollama", "qwen", srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	ch := wireProvider{completions{}}.Stream(ctx, m, Context{}, Options{})

	done := make(chan Message, 1)
	go func() {
		for ev := range ch {
			if ev.Type == EventTextDelta {
				cancel()
			}
			if ev.Terminal() {
				done <- *ev.Message
			}
		}
	}()
	select {
	case msg := <-done:
		if msg.StopReason != StopAborted || msg.Text() != "one" {
			t.Fatalf("msg = %+v", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("abort did not end the stream")
	}
}

// fakeJWT builds an unsigned token carrying a ChatGPT account ID.
func fakeJWT(account string) string {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return enc(`{"alg":"none"}`) + "." + enc(fmt.Sprintf(`{%q:{"chatgpt_account_id":%q}}`, codexJWTClaim, account)) + "."
}

func TestCodexStream(t *testing.T) {
	srv, rec := sseServer(t, 200,
		`{"type":"response.created","response":{"id":"r1"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning"}}`,
		`{"type":"response.reasoning_summary_text.delta","delta":"thinking"}`,
		`{"type":"response.output_item.done","item":{"type":"reasoning"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"message"}}`,
		`{"type":"response.output_text.delta","delta":"Sure, "}`,
		`{"type":"response.output_text.delta","delta":"reading."}`,
		`{"type":"response.output_item.done","item":{"type":"message"}}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","call_id":"call_1","name":"read","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","delta":"{\"path\":"}`,
		`{"type":"response.function_call_arguments.delta","delta":"\"a\"}"}`,
		`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"a\"}"}}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25,"input_tokens_details":{"cached_tokens":4}}}}`,
	)
	m, _ := NewModel("openai-codex", "gpt-5", srv.URL)
	tools := []Tool{{Name: "read", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}}
	history := []Message{
		UserText("hello"),
		{Role: RoleAssistant, StopReason: StopToolUse, Content: []Block{
			{Type: BlockText, Text: "ok"},
			{Type: BlockToolCall, ID: "c0", Name: "read", Arguments: json.RawMessage(`{}`)},
		}},
		ToolResult("c0", "read", "data", false),
		UserText("next"),
	}
	evs := collect(t, wireProvider{codex{}}.Stream(context.Background(), m,
		Context{SystemPrompt: "sys", Messages: history, Tools: tools},
		Options{APIKey: fakeJWT("acct-1"), SessionID: "s1", Reasoning: "low"}))

	if got, want := types(evs), "start thinking_start thinking_delta thinking_end text_start text_delta text_delta text_end toolcall_start toolcall_delta toolcall_delta toolcall_end done"; got != want {
		t.Fatalf("events = %q\nwant     %q", got, want)
	}
	final := evs[len(evs)-1].Message
	if final.Text() != "Sure, reading." || final.StopReason != StopToolUse {
		t.Fatalf("final = %+v", final)
	}
	call := final.ToolCalls()[0]
	if call.ID != "call_1" || string(call.Arguments) != `{"path":"a"}` {
		t.Errorf("call = %+v", call)
	}
	if final.Usage != (Usage{Input: 16, Output: 5, CacheRead: 4, TotalTokens: 25}) {
		t.Errorf("usage = %+v", final.Usage)
	}

	if rec.path != "/codex/responses" {
		t.Errorf("path = %s", rec.path)
	}
	if rec.headers.Get("chatgpt-account-id") != "acct-1" || !strings.HasPrefix(rec.headers.Get("Authorization"), "Bearer ") ||
		rec.headers.Get("session-id") != "s1" || rec.headers.Get("OpenAI-Beta") != "responses=experimental" {
		t.Errorf("headers = %v", rec.headers)
	}
	if rec.body["store"] != false || rec.body["stream"] != true || rec.body["instructions"] != "sys" ||
		rec.body["reasoning"].(map[string]any)["effort"] != "low" {
		t.Errorf("body = %v", rec.body)
	}
	input := rec.body["input"].([]any)
	var kinds []string
	for _, it := range input {
		m := it.(map[string]any)
		k, _ := m["type"].(string)
		if k == "" {
			k, _ = m["role"].(string)
		}
		kinds = append(kinds, k)
	}
	if got, want := strings.Join(kinds, ","), "user,message,function_call,function_call_output,user"; got != want {
		t.Errorf("input items = %s, want %s", got, want)
	}
	out := input[3].(map[string]any)
	if out["call_id"] != "c0" || out["output"] != "data" {
		t.Errorf("function_call_output = %v", out)
	}
}

func TestCodexArgumentsFromDoneItem(t *testing.T) {
	srv, _ := sseServer(t, 200,
		`{"type":"response.output_item.added","item":{"type":"function_call","call_id":"c","name":"x"}}`,
		`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"c","name":"x","arguments":"{\"a\":1}"}}`,
		`{"type":"response.completed","response":{"status":"completed"}}`,
	)
	m, _ := NewModel("openai-codex", "gpt-5", srv.URL)
	msg, err := Complete(context.Background(), wireProvider{codex{}}, m, Context{}, Options{APIKey: fakeJWT("a")})
	if err != nil || string(msg.ToolCalls()[0].Arguments) != `{"a":1}` {
		t.Fatalf("msg=%+v err=%v", msg, err)
	}
}

func TestCodexFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		line, want string
	}{
		"failed":     {`{"type":"response.failed","response":{"error":{"code":"rate_limit","message":"slow down"}}}`, "slow down"},
		"error":      {`{"type":"error","code":"bad","message":"boom"}`, "boom"},
		"incomplete": {`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"content_filter"}}}`, "content_filter"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := sseServer(t, 200, tc.line)
			m, _ := NewModel("openai-codex", "gpt-5", srv.URL)
			_, err := Complete(context.Background(), wireProvider{codex{}}, m, Context{}, Options{APIKey: fakeJWT("a")})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestCodexLengthStop(t *testing.T) {
	srv, _ := sseServer(t, 200,
		`{"type":"response.output_text.delta","delta":"cut"}`,
		`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`,
	)
	m, _ := NewModel("openai-codex", "gpt-5", srv.URL)
	msg, err := Complete(context.Background(), wireProvider{codex{}}, m, Context{}, Options{APIKey: fakeJWT("a")})
	if err != nil || msg.StopReason != StopLength {
		t.Fatalf("msg=%+v err=%v", msg, err)
	}
}

func TestCodexNeedsLogin(t *testing.T) {
	m, _ := NewModel("openai-codex", "gpt-5", "http://127.0.0.1:1")
	_, err := Complete(context.Background(), wireProvider{codex{}}, m, Context{}, Options{})
	if err == nil || !strings.Contains(err.Error(), "auth login") {
		t.Fatalf("err = %v", err)
	}
	_, err = Complete(context.Background(), wireProvider{codex{}}, m, Context{}, Options{APIKey: "not-a-jwt"})
	if err == nil || !strings.Contains(err.Error(), "JWT") {
		t.Fatalf("err = %v", err)
	}
}

func TestCodexURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                                 "https://chatgpt.com/backend-api/codex/responses",
		"https://x.test/backend-api/":      "https://x.test/backend-api/codex/responses",
		"https://x.test/backend-api/codex": "https://x.test/backend-api/codex/responses",
		"https://x.test/backend-api/codex/responses": "https://x.test/backend-api/codex/responses",
	} {
		if got := codexURL(in); got != want {
			t.Errorf("codexURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitRefAndNewModel(t *testing.T) {
	for ref, want := range map[string][2]string{
		"ollama/qwen2.5:7b":      {"ollama", "qwen2.5:7b"},
		"ollama/hf.co/org/model": {"ollama", "hf.co/org/model"},
		"qwen2.5:7b":             {"", "qwen2.5:7b"},
	} {
		p, id := SplitRef(ref)
		if p != want[0] || id != want[1] {
			t.Errorf("SplitRef(%q) = %q, %q", ref, p, id)
		}
	}

	m, err := NewModel("ollama", "qwen", "")
	if err != nil || m.API != APICompletions || m.BaseURL != "http://localhost:11434/v1" || m.Ref() != "ollama/qwen" {
		t.Fatalf("m=%+v err=%v", m, err)
	}
	if _, err := NewModel("nope", "x", ""); err == nil {
		t.Error("unknown provider accepted")
	}
	if _, err := NewModel("ollama", "", ""); err == nil {
		t.Error("empty model ID accepted")
	}
	for _, id := range Providers() {
		m, _ := NewModel(id, "x", "")
		if _, err := ProviderFor(m); err != nil {
			t.Errorf("provider %s: %v", id, err)
		}
	}
}

func TestReadSSE(t *testing.T) {
	in := ": keepalive\nevent: a\ndata: one\ndata: two\n\ndata:three\n\nevent: ignored\n\ndata: last"
	var got []string
	err := readSSE(strings.NewReader(in), func(ev, data string) error {
		got = append(got, ev+"|"+data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "a|one\ntwo,|three,|last"; strings.Join(got, ",") != want {
		t.Errorf("got %q, want %q", strings.Join(got, ","), want)
	}
}

func TestMessageJSONRoundTrip(t *testing.T) {
	in := Message{Role: RoleAssistant, Provider: "ollama", Model: "m", StopReason: StopToolUse,
		Content:   []Block{{Type: BlockToolCall, ID: "1", Name: "x", Arguments: json.RawMessage(`{"a":1}`)}},
		Timestamp: time.Unix(100, 0).UTC()}
	raw, _ := json.Marshal(in)
	var out Message
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Timestamp.Equal(in.Timestamp) || string(out.Content[0].Arguments) != `{"a":1}` || out.StopReason != StopToolUse {
		t.Errorf("round trip = %+v", out)
	}
}

func TestListModelsOllama(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		fmt.Fprint(w, `{"data":[{"id":"qwen2.5:7b"},{"id":"gpt-oss:20b"},{"id":""}]}`)
	}))
	defer srv.Close()

	got, err := ListModels(context.Background(), "ollama", srv.URL+"/v1/", "ollama")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "gpt-oss:20b,qwen2.5:7b" {
		t.Errorf("models = %v, want sorted, blank IDs dropped", got)
	}
	if gotPath != "/v1/models" || gotAuth != "Bearer ollama" {
		t.Errorf("request = %s with auth %q", gotPath, gotAuth)
	}
}

func TestListModelsServerError(t *testing.T) {
	srv, _ := sseServer(t, 500)
	defer srv.Close()
	if _, err := ListModels(context.Background(), "ollama", srv.URL, ""); err == nil {
		t.Error("a 500 was not reported")
	}
}

func TestListModelsCodexIsStatic(t *testing.T) {
	got, err := ListModels(context.Background(), "openai-codex", "", "")
	if err != nil || len(got) == 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	got[0] = "mutated"
	if again, _ := ListModels(context.Background(), "openai-codex", "", ""); again[0] == "mutated" {
		t.Error("caller can modify the catalog")
	}
}

func TestListModelsUnknownProvider(t *testing.T) {
	if _, err := ListModels(context.Background(), "nope", "", ""); err == nil {
		t.Error("unknown provider accepted")
	}
}
