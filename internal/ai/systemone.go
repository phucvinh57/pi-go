package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"time"
)

// APISystemOne is the System One classification protocol: TypeSafe's, for its
// hosted Jev models, which Laya's local server (laya-serve) speaks too.
const APISystemOne = "system-one"

// DefaultLayaBaseURL is where laya-serve listens unless LAYA_HOST or
// LAYA_PORT say otherwise.
const DefaultLayaBaseURL = "http://127.0.0.1:8000/v1"

// classifyClient bounds a classification that the caller's context does not:
// a warm local checkpoint answers in tens of milliseconds, a cold one loads
// first.
var classifyClient = &http.Client{Timeout: 30 * time.Second}

// systemOne speaks System One: POST {baseURL}/systemone with the model, the
// state and the questions, and read back one answer per question. On the wire a
// bool question is called "noul", and so is its answer's probability. The
// model names a Laya checkpoint ("english", "multilingual").
type systemOne struct{}

var _ Classifier = systemOne{}

type soQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria"`
}

type soRequest struct {
	Model     string                `json:"model"`
	State     map[string]any        `json:"state"`
	Questions map[string]soQuestion `json:"questions"`
}

type soAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Score         *float64           `json:"score"`
	Confidence    *float64           `json:"confidence"`
	Noul          *float64           `json:"noul"`
}

type soResponse struct {
	Answers map[string]soAnswer `json:"answers"`
	Usage   *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (systemOne) Classify(ctx context.Context, m Model, state map[string]any, questions map[string]Question, o Options) (Classification, error) {
	if m.API != APISystemOne {
		return Classification{}, fmt.Errorf("unsupported classifier API %q", m.API)
	}
	req := soRequest{Model: m.ID, State: state, Questions: make(map[string]soQuestion, len(questions))}
	for id, q := range questions {
		wq, err := toWireQuestion(q)
		if err != nil {
			return Classification{}, fmt.Errorf("question %s: %w", id, err)
		}
		req.Questions[id] = wq
	}

	var resp soResponse
	url := strings.TrimRight(m.BaseURL, "/") + "/systemone"
	if err := postJSON(ctx, url, o.APIKey, req, &resp); err != nil {
		return Classification{}, err
	}

	out := Classification{Answers: make(map[string]Answer, len(questions))}
	for id, q := range questions {
		a, ok := resp.Answers[id]
		if !ok {
			return Classification{}, fmt.Errorf("classifier did not answer %s", id)
		}
		ans, err := fromWireAnswer(q, a)
		if err != nil {
			return Classification{}, fmt.Errorf("answer %s: %w", id, err)
		}
		out.Answers[id] = ans
	}
	if u := resp.Usage; u != nil {
		out.Usage = Usage{Input: max(0, u.InputTokens), Output: max(0, u.OutputTokens)}
		out.Usage.TotalTokens = out.Usage.Input + out.Usage.Output
		CalculateCost(m, &out.Usage)
	}
	return out, nil
}

func toWireQuestion(q Question) (soQuestion, error) {
	switch q.Type {
	case QuestionChoice:
		if len(q.Options) < 2 {
			return soQuestion{}, fmt.Errorf("a choice needs at least 2 options, got %d", len(q.Options))
		}
		return soQuestion{Type: "choice", Instructions: q.Instructions, Criteria: q.Options}, nil
	case QuestionScore:
		if len(q.Levels) < 2 {
			return soQuestion{}, fmt.Errorf("a score needs at least 2 levels, got %d", len(q.Levels))
		}
		return soQuestion{Type: "score", Instructions: q.Instructions, Criteria: q.Levels}, nil
	case QuestionBool:
		return soQuestion{Type: "noul", Instructions: q.Instructions,
			Criteria: map[string]string{"true": q.True, "false": q.False}}, nil
	}
	return soQuestion{}, fmt.Errorf("unknown question type %q", q.Type)
}

func fromWireAnswer(q Question, a soAnswer) (Answer, error) {
	switch q.Type {
	case QuestionChoice:
		if a.Type != "choice" || a.Choice == "" || a.Confidence == nil {
			return Answer{}, fmt.Errorf("not a choice answer")
		}
		if _, ok := q.Options[a.Choice]; !ok {
			return Answer{}, fmt.Errorf("unknown option %q", a.Choice)
		}
		return Answer{Type: QuestionChoice, Choice: a.Choice, Probabilities: a.Probabilities, Confidence: unit(*a.Confidence)}, nil
	case QuestionScore:
		if a.Type != "score" || a.Score == nil || a.Confidence == nil {
			return Answer{}, fmt.Errorf("not a score answer")
		}
		score := math.Min(math.Max(*a.Score, 0), float64(len(q.Levels)-1))
		return Answer{Type: QuestionScore, Score: score, Confidence: unit(*a.Confidence)}, nil
	default:
		if a.Type != "noul" || a.Noul == nil {
			return Answer{}, fmt.Errorf("not a bool answer")
		}
		return Answer{Type: QuestionBool, Probability: unit(*a.Noul)}, nil
	}
}

// unit clamps a probability or a confidence to [0, 1]; NaN becomes 0.
func unit(v float64) float64 {
	if !(v > 0) {
		return 0
	}
	return math.Min(v, 1)
}

// postJSON POSTs body as JSON and decodes the JSON reply into out. apiKey is
// sent as a bearer token when set: laya-serve asks for one only when it was
// started with LAYA_API_KEY. A non-2xx status becomes an *httpError.
func postJSON(ctx context.Context, url, apiKey string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("invalid request URL %q: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := classifyClient.Do(req)
	if err != nil {
		var op *net.OpError
		if ctx.Err() == nil && errors.As(err, &op) && op.Op == "dial" {
			return fmt.Errorf("no server is listening at %s", req.URL.Host)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &httpError{Status: resp.StatusCode, Body: strings.TrimSpace(string(snippet))}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode reply from %s: %w", url, err)
	}
	return nil
}
