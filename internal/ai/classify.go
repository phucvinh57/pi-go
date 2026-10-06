package ai

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// A classifier model does not chat: it reads a JSON state and answers typed
// questions about it with probabilities, in one forward pass. PI calls this the
// classifier API; pi-go uses it with Laya, served locally by laya-serve.

// QuestionType is the kind of answer a Question expects.
type QuestionType string

const (
	QuestionChoice QuestionType = "choice" // one of a set of named options
	QuestionScore  QuestionType = "score"  // a level on an ordered scale
	QuestionBool   QuestionType = "bool"   // yes or no
)

// Question is one typed question about the state. Which fields apply depends
// on Type: Options for a choice, Levels for a score, True and False for a bool.
type Question struct {
	Type         QuestionType
	Instructions string
	// Options maps each option's key to what it means.
	Options map[string]string
	// Levels are the steps of the scale, lowest first.
	Levels []string
	// True and False say what yes and no mean.
	True, False string
}

// Choice asks which of options (key: meaning) fits best.
func Choice(instructions string, options map[string]string) Question {
	return Question{Type: QuestionChoice, Instructions: instructions, Options: options}
}

// Score asks where on the scale of levels, lowest first, the state sits.
func Score(instructions string, levels ...string) Question {
	return Question{Type: QuestionScore, Instructions: instructions, Levels: levels}
}

// Bool asks a yes-or-no question.
func Bool(instructions, yes, no string) Question {
	return Question{Type: QuestionBool, Instructions: instructions, True: yes, False: no}
}

// Answer is the classifier's answer to one Question.
type Answer struct {
	Type QuestionType
	// Choice is the most likely option, and Probabilities the chance of each.
	Choice        string
	Probabilities map[string]float64
	// Score is the expected level: an index into Levels, possibly between two.
	Score float64
	// Confidence, for a choice or a score, is how sure the answer is, from 0 to 1.
	Confidence float64
	// Probability, for a bool, is the chance the answer is yes.
	Probability float64
}

// Classification is the answer to every question of one call, by question ID,
// and the tokens the call used.
type Classification struct {
	Answers map[string]Answer
	Usage   Usage
}

// Classifier answers typed questions about a state. Implementations are
// network calls, so this is where tests substitute a fake.
type Classifier interface {
	Classify(ctx context.Context, m Model, state map[string]any, questions map[string]Question, o Options) (Classification, error)
}

// classifierSpecs are the providers that serve classifier models. They are kept
// apart from providerSpecs: a classifier cannot hold a conversation, so it must
// never be offered as a chat model.
var classifierSpecs = map[string]providerSpec{
	"laya": {api: APISystemOne, defaultBaseURL: DefaultLayaBaseURL},
}

var classifierAPIs = map[string]Classifier{
	APISystemOne: systemOne{},
}

// NewClassifierModel builds the Model of a classifier. baseURL overrides the
// provider's default when non-empty.
func NewClassifierModel(provider, id, baseURL string) (Model, error) {
	spec, ok := classifierSpecs[provider]
	if !ok {
		known := make([]string, 0, len(classifierSpecs))
		for p := range classifierSpecs {
			known = append(known, p)
		}
		sort.Strings(known)
		return Model{}, fmt.Errorf("no classifier adapter for provider %q (available: %s)", provider, strings.Join(known, ", "))
	}
	if id == "" {
		return Model{}, fmt.Errorf("empty classifier model ID for provider %q", provider)
	}
	if baseURL == "" {
		baseURL = spec.defaultBaseURL
	}
	return Model{Provider: provider, ID: id, API: spec.api, BaseURL: baseURL}, nil
}

// ClassifierFor returns the Classifier that speaks the model's API.
func ClassifierFor(m Model) (Classifier, error) {
	c, ok := classifierAPIs[m.API]
	if !ok {
		return nil, fmt.Errorf("model %s is not a classifier (API %q)", m.Ref(), m.API)
	}
	return c, nil
}
