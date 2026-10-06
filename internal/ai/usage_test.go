package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

func TestCalculateCost(t *testing.T) {
	m := Model{Cost: Rates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}}
	u := Usage{Input: 1_000_000, Output: 200_000, CacheRead: 500_000, CacheWrite: 100_000}
	CalculateCost(m, &u)

	want := Cost{Input: 3, Output: 3, CacheRead: 0.15, CacheWrite: 0.375, Total: 6.525}
	if !near(u.Cost.Input, want.Input) || !near(u.Cost.Output, want.Output) ||
		!near(u.Cost.CacheRead, want.CacheRead) || !near(u.Cost.CacheWrite, want.CacheWrite) ||
		!near(u.Cost.Total, want.Total) {
		t.Fatalf("cost = %+v, want %+v", u.Cost, want)
	}

	free := Usage{Input: 10, Output: 10}
	CalculateCost(Model{}, &free)
	if free.Cost != (Cost{}) {
		t.Fatalf("unpriced model cost = %+v, want zero", free.Cost)
	}
}

func TestContextTokens(t *testing.T) {
	if got := ContextTokens(Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 99}); got != 99 {
		t.Errorf("with total = %d, want 99", got)
	}
	if got := ContextTokens(Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}); got != 10 {
		t.Errorf("without total = %d, want 10", got)
	}
}

func TestCompletionsUsageCacheAndCost(t *testing.T) {
	srv, _ := sseServer(t, 200,
		`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":100,"total_tokens":0,"prompt_tokens_details":{"cached_tokens":600,"cache_write_tokens":150}}}`,
		`[DONE]`,
	)
	m, _ := NewModel("ollama", "qwen", srv.URL+"/v1")
	m.Cost = Rates{Input: 1, Output: 2, CacheRead: 0.5, CacheWrite: 1.5}

	final, err := complete(context.Background(), wireProvider{completions{}}, m,
		Context{Messages: []Message{UserText("hi")}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	u := final.Usage
	// Input excludes both cache counts, and the total is the sum of the parts
	// even when the server reports none.
	if u.Input != 250 || u.Output != 100 || u.CacheRead != 600 || u.CacheWrite != 150 || u.TotalTokens != 1100 {
		t.Fatalf("usage = %+v", u)
	}
	wantTotal := (250*1 + 100*2 + 600*0.5 + 150*1.5) / 1e6
	if !near(u.Cost.Total, wantTotal) {
		t.Fatalf("cost total = %v, want %v", u.Cost.Total, wantTotal)
	}
}

func TestCompletionsUsageDeepSeekCacheHit(t *testing.T) {
	srv, _ := sseServer(t, 200,
		`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":5,"prompt_cache_hit_tokens":40}}`,
		`[DONE]`,
	)
	m, _ := NewModel("ollama", "qwen", srv.URL+"/v1")
	final, err := complete(context.Background(), wireProvider{completions{}}, m,
		Context{Messages: []Message{UserText("hi")}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if u := final.Usage; u.Input != 60 || u.CacheRead != 40 || u.TotalTokens != 105 {
		t.Fatalf("usage = %+v", u)
	}
}

// A call that fails after the server reported usage was still billed, so the
// error message carries it, priced.
func TestFailedCallKeepsPricedUsage(t *testing.T) {
	srv, _ := sseServer(t, 200,
		`{"choices":[{"delta":{"content":"par"}}]}`,
		`{"choices":[],"usage":{"prompt_tokens":1000000,"completion_tokens":0}}`,
		// no [DONE] and no finish_reason: the stream ends incomplete
	)
	m, _ := NewModel("ollama", "qwen", srv.URL+"/v1")
	m.Cost = Rates{Input: 2}

	final, err := complete(context.Background(), wireProvider{completions{}}, m,
		Context{Messages: []Message{UserText("hi")}}, Options{})
	if err == nil || final.StopReason != StopError {
		t.Fatalf("want an error message, got %v / %+v", err, final)
	}
	if !near(final.Usage.Cost.Total, 2) {
		t.Fatalf("cost of the failed call = %v, want 2", final.Usage.Cost.Total)
	}
}

func TestCodexUsageCacheWrite(t *testing.T) {
	srv, _ := sseServer(t, 200,
		`{"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110,"input_tokens_details":{"cached_tokens":30,"cache_write_tokens":20}}}}`,
	)
	m, _ := NewModel("openai-codex", "gpt-5.5", srv.URL)
	final, err := complete(context.Background(), wireProvider{codex{}}, m,
		Context{Messages: []Message{UserText("hi")}}, Options{APIKey: fakeJWT("acct")})
	if err != nil {
		t.Fatal(err)
	}
	if u := final.Usage; u.Input != 50 || u.CacheRead != 30 || u.CacheWrite != 20 || u.Output != 10 || u.TotalTokens != 110 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestNewModelCodexLimits(t *testing.T) {
	m, err := NewModel("openai-codex", "gpt-5.5", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.ContextWindow != 272_000 || m.MaxTokens != 128_000 {
		t.Errorf("gpt-5.5 limits = %d/%d", m.ContextWindow, m.MaxTokens)
	}
	spark, _ := NewModel("openai-codex", "gpt-5.3-codex-spark", "")
	if spark.ContextWindow != 128_000 {
		t.Errorf("spark window = %d, want 128000", spark.ContextWindow)
	}
	local, _ := NewModel("ollama", "qwen", "")
	if local.ContextWindow != 0 {
		t.Errorf("ollama window = %d, want 0 until asked", local.ContextWindow)
	}
}

func TestContextWindowFromOllama(t *testing.T) {
	var gotPath, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		fmt.Fprint(w, `{"model_info":{"general.architecture":"qwen2","qwen2.context_length":32768,"qwen2.block_count":28}}`)
	}))
	t.Cleanup(srv.Close)

	m, _ := NewModel("ollama", "qwen2.5-coder:7b", srv.URL+"/v1")
	got, err := ContextWindow(context.Background(), m)
	if err != nil || got != 32768 {
		t.Fatalf("ContextWindow = %d, %v", got, err)
	}
	if gotPath != "/api/show" || gotModel != "qwen2.5-coder:7b" {
		t.Errorf("request = %s model %q", gotPath, gotModel)
	}
}

func TestContextWindowUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"model_info":{"general.architecture":"x"}}`)
	}))
	t.Cleanup(srv.Close)
	m, _ := NewModel("ollama", "x", srv.URL+"/v1")
	if got, err := ContextWindow(context.Background(), m); err == nil || got != 0 {
		t.Fatalf("want 0 and an error, got %d, %v", got, err)
	}

	codex, _ := NewModel("openai-codex", "gpt-5.5", "")
	if _, err := ContextWindow(context.Background(), codex); err == nil {
		t.Fatal("codex has no /api/show; want an error")
	}
}
