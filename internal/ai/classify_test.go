package ai

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testQuestions = map[string]Question{
	"category":     Choice("Classify the message", map[string]string{"success": "Successful", "failure": "Failed"}),
	"satisfaction": Score("Score satisfaction", "low", "neutral", "high"),
	"approved":     Bool("Does the user approve?", "Approval", "No approval"),
}

const wireAnswers = `{
	"category": {"type": "choice", "choice": "success", "probabilities": {"success": 0.9, "failure": 0.1}, "confidence": 0.8},
	"satisfaction": {"type": "score", "score": 2, "confidence": 0.7},
	"approved": {"type": "noul", "noul": 0.95}
}`

// systemOneServer answers every request with reply and keeps the last request.
func systemOneServer(t *testing.T, status int, reply string) (Model, *http.Request, *map[string]any) {
	t.Helper()
	var (
		got  http.Request
		body map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request body: %v", err)
		}
		w.WriteHeader(status)
		w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	m, err := NewClassifierModel("laya", "english", srv.URL+"/v1/")
	if err != nil {
		t.Fatal(err)
	}
	return m, &got, &body
}

func TestSystemOneClassify(t *testing.T) {
	m, req, body := systemOneServer(t, 200, `{"answers": `+wireAnswers+`}`)
	got, err := systemOne{}.Classify(context.Background(), m, map[string]any{"text": "thanks"}, testQuestions, Options{APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}

	if req.URL.Path != "/v1/systemone" || req.Header.Get("Authorization") != "Bearer secret" {
		t.Errorf("request = %s %s", req.URL.Path, req.Header.Get("Authorization"))
	}
	b := *body
	qs, _ := b["questions"].(map[string]any)
	if b["model"] != "english" || b["state"].(map[string]any)["text"] != "thanks" {
		t.Errorf("body = %v", b)
	}
	for id, want := range map[string]string{"category": "choice", "satisfaction": "score", "approved": "noul"} {
		if q, _ := qs[id].(map[string]any); q["type"] != want {
			t.Errorf("%s sent as %v, want %s", id, q["type"], want)
		}
	}

	if a := got.Answers["category"]; a.Choice != "success" || a.Confidence != 0.8 || a.Probabilities["failure"] != 0.1 {
		t.Errorf("choice = %+v", a)
	}
	if a := got.Answers["satisfaction"]; a.Score != 2 || a.Confidence != 0.7 {
		t.Errorf("score = %+v", a)
	}
	if a := got.Answers["approved"]; a.Type != QuestionBool || a.Probability != 0.95 {
		t.Errorf("bool = %+v", a)
	}
	if got.Usage != (Usage{}) {
		t.Errorf("usage = %+v, want none when the server reports none", got.Usage)
	}
}

func TestSystemOneUsageIsPriced(t *testing.T) {
	m, _, _ := systemOneServer(t, 200, `{"answers": `+wireAnswers+`, "usage": {"input_tokens": 308, "output_tokens": 23}}`)
	m.Cost = Rates{Input: 0.042}
	got, err := systemOne{}.Classify(context.Background(), m, map[string]any{}, testQuestions, Options{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Usage.Input != 308 || got.Usage.Output != 23 || got.Usage.TotalTokens != 331 {
		t.Errorf("usage = %+v", got.Usage)
	}
	if math.Abs(got.Usage.Cost.Total-0.000012936) > 1e-12 {
		t.Errorf("cost = %v", got.Usage.Cost.Total)
	}
}

func TestSystemOneErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		reply  string
		want   string
	}{
		"http error":      {401, `{"error":"bad key"}`, "401"},
		"missing answer":  {200, `{"answers": {}}`, "did not answer"},
		"wrong type":      {200, `{"answers": {"category": {"type": "score", "score": 1, "confidence": 1}}}`, "not a choice"},
		"unknown option":  {200, `{"answers": {"category": {"type": "choice", "choice": "maybe", "confidence": 1}}}`, "unknown option"},
		"not json at all": {200, `<html>`, "decode"},
	} {
		t.Run(name, func(t *testing.T) {
			m, _, _ := systemOneServer(t, tc.status, tc.reply)
			qs := map[string]Question{"category": testQuestions["category"]}
			_, err := systemOne{}.Classify(context.Background(), m, map[string]any{}, qs, Options{APIKey: "k"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestSystemOneWithoutKeySendsNoAuthorization(t *testing.T) {
	// laya-serve asks for a key only when started with LAYA_API_KEY.
	m, req, _ := systemOneServer(t, 200, `{"answers": `+wireAnswers+`}`)
	if _, err := (systemOne{}).Classify(context.Background(), m, map[string]any{}, testQuestions, Options{}); err != nil {
		t.Fatal(err)
	}
	if auth := req.Header.Get("Authorization"); auth != "" {
		t.Errorf("Authorization = %q, want none", auth)
	}
}

func TestSystemOneWithoutServerSaysSo(t *testing.T) {
	m, _ := NewClassifierModel("laya", "english", "http://127.0.0.1:1/v1")
	_, err := (systemOne{}).Classify(context.Background(), m, map[string]any{}, testQuestions, Options{})
	if err == nil || err.Error() != "no server is listening at 127.0.0.1:1" {
		t.Errorf("err = %v", err)
	}
}

func TestSystemOneRefusesInvalidQuestions(t *testing.T) {
	m, _ := NewClassifierModel("laya", "english", "http://127.0.0.1:1")
	one := map[string]Question{"q": Choice("pick", map[string]string{"only": "one"})}
	if _, err := (systemOne{}).Classify(context.Background(), m, nil, one, Options{APIKey: "k"}); err == nil {
		t.Error("a choice with one option was sent")
	}
}

func TestClassifierModelsAreNotChatModels(t *testing.T) {
	m, err := NewClassifierModel("laya", "english", "")
	if err != nil || m.BaseURL != DefaultLayaBaseURL || m.API != APISystemOne {
		t.Fatalf("model = %+v, err %v", m, err)
	}
	if _, err := ClassifierFor(m); err != nil {
		t.Error(err)
	}
	if _, err := ProviderFor(m); err == nil {
		t.Error("a classifier was offered as a chat provider")
	}
	for _, p := range Providers() {
		if p == "laya" {
			t.Error("laya is listed among the chat providers")
		}
	}
	if _, err := NewClassifierModel("ollama", "x", ""); err == nil {
		t.Error("a chat provider was accepted as a classifier")
	}
	if _, err := ClassifierFor(Model{Provider: "ollama", ID: "x", API: APICompletions}); err == nil {
		t.Error("a chat model was accepted as a classifier")
	}
}

func TestSystemOneHonoursContext(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	t.Cleanup(func() { close(block); srv.Close() })
	m, _ := NewClassifierModel("laya", "english", srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := systemOne{}.Classify(ctx, m, nil, testQuestions, Options{APIKey: "k"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
