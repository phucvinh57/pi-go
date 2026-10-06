package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phucvinh57/pi-go/internal/agent"
	"github.com/phucvinh57/pi-go/internal/ai"
	sessionfile "github.com/phucvinh57/pi-go/internal/session"
	"github.com/phucvinh57/pi-go/internal/tui"
)

// autoSetup is recordingSetup with settings.toml naming the ladder (other is
// the weak model, qwen the strong one) and the classifier.
func autoSetup(t *testing.T, classifier string) environment {
	t.Helper()
	env := recordingSetup(t)
	settings := "[auto]\nmodels = [\"ollama/other\", \"ollama/qwen\"]\nclassifier = \"" + classifier + "\"\n"
	if err := os.WriteFile(filepath.Join(env.agentDir, "settings.toml"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	return env
}

func routes(events []tui.Event) []tui.Route {
	var out []tui.Route
	for _, e := range events {
		if e.Kind == tui.EventRoute && e.Route != nil {
			out = append(out, *e.Route)
		}
	}
	return out
}

func modelChanges(t *testing.T, dir string) []string {
	t.Helper()
	sessions, err := sessionfile.LoadAll(dir)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %v, err %v", sessions, err)
	}
	var changes []string
	for _, e := range sessions[0].Entries {
		if e.Type == sessionfile.TypeModelChange {
			changes = append(changes, e.ModelID)
		}
	}
	return changes
}

func TestAutoRoutesEachPromptWithHeuristics(t *testing.T) {
	env := autoSetup(t, "none")
	s := newSession(env, "", "auto")
	if err := s.SetEffort("auto"); err != nil {
		t.Fatal(err)
	}

	var first, second []tui.Event
	if err := s.Prompt(context.Background(), "thanks, that was all", collectEvents(&first)); err != nil {
		t.Fatal(err)
	}
	if err := s.Prompt(context.Background(), "find the root cause of the deadlock in the worker pool and fix it", collectEvents(&second)); err != nil {
		t.Fatal(err)
	}

	r1, r2 := routes(first), routes(second)
	if len(r1) != 1 || r1[0].Model != "ollama/other" || r1[0].Effort != "low" || !strings.Contains(r1[0].Note, "heuristic") {
		t.Errorf("first prompt routed %+v, want the weak model at low effort", r1)
	}
	if len(r2) != 1 || r2[0].Model != "ollama/qwen" || r2[0].Effort != "high" {
		t.Errorf("second prompt routed %+v, want the strong model at high effort", r2)
	}
	if st := lastStats(second); st == nil || st.UserMessages != 2 || st.ContextWindow != 1000 {
		t.Errorf("the conversation must carry on, on qwen's window: %+v", st)
	}
	// The stand-in model the agent was built on is not a model that answered.
	if got := modelChanges(t, env.agentDir); strings.Join(got, ",") != "other,qwen" {
		t.Errorf("model changes = %v", got)
	}
	if s.Current() != "auto" || s.Effort() != "auto" {
		t.Errorf("Current %q, Effort %q; want auto to stay selected", s.Current(), s.Effort())
	}
}

// layaAnswers is a classifier that rates every prompt with fixed answers.
type layaAnswers struct {
	complexity float64
	calls      int
}

func (j *layaAnswers) Classify(_ context.Context, _ ai.Model, _ map[string]any, _ map[string]ai.Question, o ai.Options) (ai.Classification, error) {
	j.calls++
	if o.APIKey != "laya" {
		return ai.Classification{}, os.ErrPermission // the placeholder key for a server without one
	}
	return ai.Classification{
		Answers: map[string]ai.Answer{
			"task_kind":  {Type: ai.QuestionChoice, Choice: "refactor", Confidence: 0.9},
			"complexity": {Type: ai.QuestionScore, Score: j.complexity, Confidence: 0.9},
		},
		Usage: ai.Usage{Input: 300, Output: 20, Cost: ai.Cost{Total: 0.5}},
	}, nil
}

func withClassifier(s *session, c ai.Classifier) {
	s.activeRouter() // builds the router
	s.router.classifier = func(m ai.Model) (ai.Classifier, error) {
		if m.Ref() != LayaClassifier {
			panic("asked for " + m.Ref())
		}
		return c, nil
	}
}

func TestAutoAsksLayaAndChargesIt(t *testing.T) {
	env := autoSetup(t, LayaClassifier)
	t.Setenv("LAYA_API_KEY", "")
	s := newSession(env, "", "auto")
	laya := &layaAnswers{complexity: 3}
	withClassifier(s, laya)

	var events []tui.Event
	if err := s.Prompt(context.Background(), "split the session package into three", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	r := routes(events)
	if laya.calls != 1 || len(r) != 1 || r[0].Model != "ollama/qwen" || r[0].Note != "refactor, architectural" {
		t.Errorf("after %d classifier calls, routed %+v", laya.calls, r)
	}
	st := lastStats(events)
	if st == nil || st.Cost != 16.5 {
		t.Fatalf("stats = %+v, want the reply ($16) and Laya ($0.5)", st)
	}
	var layaCost float64
	for _, m := range st.ByModel {
		if m.Ref == LayaClassifier {
			layaCost = m.Cost
		}
	}
	if layaCost != 0.5 {
		t.Errorf("by model = %+v", st.ByModel)
	}
}

func TestAutoClassifierIsOffUnlessConfigured(t *testing.T) {
	env := autoSetup(t, "")
	s := newSession(env, "", "auto")
	laya := &layaAnswers{complexity: 3}
	withClassifier(s, laya)
	var events []tui.Event
	if err := s.Prompt(context.Background(), "split the session package into three", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	if r := routes(events); laya.calls != 0 || len(r) != 1 || !strings.HasSuffix(r[0].Note, "· heuristic") {
		t.Errorf("after %d classifier calls, routed %+v", laya.calls, r)
	}
}

func TestAutoLoggedOutOfLayaUsesHeuristicsQuietly(t *testing.T) {
	env := autoSetup(t, LayaClassifier)
	t.Setenv("LAYA_API_KEY", "")
	if _, err := env.auth().Logout("laya"); err != nil {
		t.Fatal(err)
	}
	s := newSession(env, "", "auto")
	laya := &layaAnswers{complexity: 3}
	withClassifier(s, laya)

	var events []tui.Event
	if err := s.Prompt(context.Background(), "thanks, that was all", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	r := routes(events)
	if laya.calls != 0 || len(r) != 1 || !strings.HasSuffix(r[0].Note, "· heuristic") {
		t.Errorf("after %d classifier calls, routed %+v", laya.calls, r)
	}
	for _, e := range events {
		if e.Kind == tui.EventWarning {
			t.Errorf("warning %q: a missing key is not a failure", e.Text)
		}
	}
}

func TestAutoEffortKeepsTheChosenModel(t *testing.T) {
	env := autoSetup(t, "none")
	s := newSession(env, "", "ollama/qwen")
	if err := s.SetEffort("auto"); err != nil {
		t.Fatal(err)
	}
	var events []tui.Event
	if err := s.Prompt(context.Background(), "rename the variable x to count in main.go", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	r := routes(events)
	if len(r) != 1 || r[0].Model != "ollama/qwen" || r[0].Effort != "low" {
		t.Errorf("routed %+v", r)
	}
	if got := modelChanges(t, env.agentDir); strings.Join(got, ",") != "qwen" {
		t.Errorf("model changes = %v", got)
	}

	// A fixed level turns auto effort off; the router goes with it.
	if err := s.SetEffort("medium"); err != nil {
		t.Fatal(err)
	}
	events = nil
	if err := s.Prompt(context.Background(), "find the root cause of the deadlock and fix it", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	if r := routes(events); len(r) != 0 {
		t.Errorf("routed %+v with nothing on auto", r)
	}
}

func TestAutoModelKeepsFixedEffort(t *testing.T) {
	env := autoSetup(t, "none")
	s := newSession(env, "", "auto")
	if err := s.SetEffort("xhigh"); err != nil {
		t.Fatal(err)
	}
	var events []tui.Event
	if err := s.Prompt(context.Background(), "thanks, that was all", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	if r := routes(events); len(r) != 1 || r[0].Model != "ollama/other" || r[0].Effort != "xhigh" {
		t.Errorf("routed %+v, want the weak model at the chosen effort", r)
	}
}

func TestAutoWithNoModelFailsThePrompt(t *testing.T) {
	env := agentDir(t, `{"providers":{"ollama":{"baseUrl":"http://127.0.0.1:1/v1"}}}`)
	s := newSession(env, "", "auto")
	_, err := s.Respond(context.Background(), "hello there, anyone?")
	if err == nil || !strings.Contains(err.Error(), "no model is available") || !strings.Contains(err.Error(), "ollama") {
		t.Errorf("err = %v, want no model, and why", err)
	}
}

func TestAutoSelection(t *testing.T) {
	env := recordingSetup(t)
	s := newSession(env, "", "")
	if err := s.Select(context.Background(), "auto"); err != nil || s.Current() != "auto" {
		t.Fatalf("Select(auto): %v, Current %q", err, s.Current())
	}
	if err := s.Select(context.Background(), "ollama/qwen"); err != nil || s.Current() != "ollama/qwen" {
		t.Fatalf("Select(ollama/qwen): %v, Current %q", err, s.Current())
	}
	if err := s.SetDefault(context.Background(), "auto"); err != nil {
		t.Fatal(err)
	}
	if saved, _ := env.settings().DefaultModel(); saved != "auto" {
		t.Errorf("saved = %q", saved)
	}
	if got := newSession(env, "", "").Current(); got != "auto" {
		t.Errorf("a new session starts on %q, want the saved auto", got)
	}
	if got := newSession(env, "", "ollama/qwen").Current(); got != "ollama/qwen" {
		t.Errorf("--model must win over the saved auto, got %q", got)
	}

	if levels := s.Levels(); levels[0] != "auto" || len(levels) != len(ai.EffortLevels)+1 {
		t.Errorf("levels = %v", levels)
	}
	if err := s.SetEffort("auto"); err != nil || s.Effort() != "auto" {
		t.Errorf("SetEffort(auto): %v, Effort %q", err, s.Effort())
	}
	if err := s.SetEffort("high"); err != nil || s.Effort() != "high" {
		t.Errorf("SetEffort(high): %v, Effort %q", err, s.Effort())
	}
}

func TestToTUIEventRouteAndWarning(t *testing.T) {
	got, ok := toTUIEvent(agent.Event{Type: agent.EventRoute, Model: "ollama/qwen", Effort: "high", Text: "debug, demanding"})
	if !ok || got.Kind != tui.EventRoute || got.Route == nil || *got.Route != (tui.Route{Model: "ollama/qwen", Effort: "high", Note: "debug, demanding"}) {
		t.Errorf("route event = %+v", got)
	}
	got, ok = toTUIEvent(agent.Event{Type: agent.EventWarning, Text: "routing failed"})
	if !ok || got.Kind != tui.EventWarning || got.Text != "routing failed" {
		t.Errorf("warning event = %+v", got)
	}
	if got, ok := toTUIEvent(agent.Event{Type: "something new"}); ok {
		t.Errorf("an unknown event was passed on as %+v", got)
	}
}

func TestAutoWithoutLayaServerFallsBackAndSaysWhy(t *testing.T) {
	t.Setenv("LAYA_API_KEY", "")
	env := environment{agentDir: t.TempDir(), cwd: t.TempDir()}
	// Nothing listens at laya's address: the real System One adapter says so.
	models := fmt.Sprintf(`{"providers":{
		"ollama":{"baseUrl":%q,"models":[{"id":"small"}]},
		"laya":{"baseUrl":"http://127.0.0.1:1/v1"}
	}}`, fakeOllamaListing(t, "small"))
	if err := os.WriteFile(filepath.Join(env.agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := "[auto]\nclassifier = \"" + LayaClassifier + "\"\n"
	if err := os.WriteFile(filepath.Join(env.agentDir, "settings.toml"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newSession(env, "", "auto")
	var events []tui.Event
	if err := s.Prompt(context.Background(), "thanks, that was all", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	r := routes(events)
	if len(r) != 1 || r[0].Model != "ollama/small" || !strings.HasSuffix(r[0].Note, "· heuristic (laya/english: no server is listening at 127.0.0.1:1)") {
		t.Errorf("routed %+v", r)
	}
}

// writeModels replaces the agent dir's models.json.
func writeModels(t *testing.T, env environment, models string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(env.agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAutoEffortSkipsModelsThatCannotReason(t *testing.T) {
	env := autoSetup(t, "none")
	writeModels(t, env, fmt.Sprintf(`{"providers":{"ollama":{"baseUrl":%q,"models":[
		{"id":"qwen","reasoning":false},{"id":"other","reasoning":true}
	]}}}`, fakeOllama(t)))

	// A fixed model that cannot reason: no effort at all, and nothing to say.
	s := newSession(env, "", "ollama/qwen")
	if err := s.SetEffort("auto"); err != nil {
		t.Fatal(err)
	}
	var events []tui.Event
	if err := s.Prompt(context.Background(), "find the root cause of the deadlock in the worker pool and fix it", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	if r := routes(events); len(r) != 1 || r[0].Model != "ollama/qwen" || r[0].Effort != "" || r[0].Note != "" {
		t.Errorf("routed %+v, want qwen with no effort", r)
	}
	if got := s.conv.agent.Stats(); got.UserMessages != 1 {
		t.Fatalf("stats = %+v", got)
	}

	// An auto model: the effort goes only to the model that can take it.
	s = newSession(env, "", "auto")
	if err := s.SetEffort("auto"); err != nil {
		t.Fatal(err)
	}
	events = nil
	if err := s.Prompt(context.Background(), "find the root cause of the deadlock in the worker pool and fix it", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	if r := routes(events); len(r) != 1 || r[0].Model != "ollama/qwen" || r[0].Effort != "" {
		t.Errorf("routed %+v, want the strong model, which cannot reason, with no effort", r)
	}
	events = nil
	if err := s.Prompt(context.Background(), "thanks, that is all for today", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	if r := routes(events); len(r) != 1 || r[0].Model != "ollama/other" || r[0].Effort != "low" {
		t.Errorf("routed %+v, want the weak model, which reasons, at low", r)
	}
}

func TestCanReasonAsksOllamaOnce(t *testing.T) {
	env := agentDir(t, `{"providers":{"ollama":{"models":[{"id":"said","reasoning":true}]}}}`)
	var asked []string
	old := thinkingFinder
	thinkingFinder = func(_ context.Context, m ai.Model) (bool, error) {
		asked = append(asked, m.Ref())
		return m.ID == "deepseek-r1", nil
	}
	t.Cleanup(func() { thinkingFinder = old })

	cache := map[string]bool{}
	model := func(provider, id string) ai.Model {
		m, err := ai.NewModel(provider, id, "")
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if !env.canReason(model("ollama", "said"), cache) || !env.canReason(model("openai-codex", "gpt-5.5"), cache) {
		t.Error("models.json and Codex say yes without asking")
	}
	for range 2 {
		if !env.canReason(model("ollama", "deepseek-r1"), cache) || env.canReason(model("ollama", "qwen"), cache) {
			t.Error("the server's answer was not used")
		}
	}
	if strings.Join(asked, ",") != "ollama/deepseek-r1,ollama/qwen" {
		t.Errorf("asked about %v, want each unknown model once", asked)
	}
}

// countingOllama is a fake Ollama listing ids, counting the listings. With no
// ids, every listing fails as a server in trouble would.
func countingOllama(t *testing.T, ids ...string) (string, *int) {
	t.Helper()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if len(ids) == 0 {
			http.Error(w, "loading", http.StatusServiceUnavailable)
			return
		}
		var data []map[string]string
		for _, id := range ids {
			data = append(data, map[string]string{"id": id})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1", &n
}

// loginCodex stores a ChatGPT login, so Codex's fixed catalog is listed.
func loginCodex(t *testing.T, env environment) {
	t.Helper()
	data := fmt.Sprintf(`{"openai-codex":{"type":"oauth","access":"tok","expires":%d}}`, time.Now().Add(time.Hour).UnixMilli())
	if err := os.WriteFile(filepath.Join(env.agentDir, "auth.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogExpires(t *testing.T) {
	url, listings := countingOllama(t, "qwen")
	env := agentDir(t, fmt.Sprintf(`{"providers":{"ollama":{"baseUrl":%q}}}`, url))
	now := time.Unix(0, 0)
	cat := &catalog{now: func() time.Time { return now }}

	cat.get(context.Background(), env)
	now = now.Add(catalogTTL - time.Second)
	cat.get(context.Background(), env)
	if *listings != 1 {
		t.Fatalf("listed %d times within the TTL, want 1", *listings)
	}
	now = now.Add(2 * time.Second)
	if l := cat.get(context.Background(), env); *listings != 2 || len(l.cands) != 1 {
		t.Errorf("listed %d times after the TTL, got %+v", *listings, l)
	}
}

func TestCatalogRetriesSoonAfterAFailure(t *testing.T) {
	url, listings := countingOllama(t) // Ollama is still starting
	env := agentDir(t, fmt.Sprintf(`{"providers":{"ollama":{"baseUrl":%q}}}`, url))
	loginCodex(t, env)
	now := time.Unix(0, 0)
	cat := &catalog{now: func() time.Time { return now }}

	l := cat.get(context.Background(), env)
	if l.down["ollama"] == nil || l.err == nil || len(l.cands) == 0 {
		t.Fatalf("listing = %+v, want ollama down and codex listed", l)
	}
	now = now.Add(catalogRetry + time.Second)
	cat.get(context.Background(), env)
	if *listings != 2 {
		t.Errorf("listed %d times, want a retry after %s", *listings, catalogRetry)
	}
}

func TestAutoLeavesOutProvidersThatAreDownAndSaysSoOnce(t *testing.T) {
	url, _ := countingOllama(t)
	// Ollama is down, but its models.json models would be the lowest rungs.
	env := agentDir(t, fmt.Sprintf(`{"providers":{"ollama":{"baseUrl":%q,"models":[{"id":"qwen"}]}}}`, url))
	loginCodex(t, env)
	r := newAutoRouter(env, &catalog{})
	r.autoModel = true

	stuck := ai.Model{Provider: "ollama", ID: "qwen"}
	req := agent.RouteRequest{
		Reason:   agent.RouteUser,
		Current:  agent.Route{Model: stuck},
		Messages: []ai.Message{ai.UserText("thanks, that was all")},
	}
	var warnings []string
	for range 2 {
		out, err := r.Route(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if out.Model.Provider != "openai-codex" {
			t.Errorf("routed to %s, want a model whose provider is up", out.Model.Ref())
		}
		if out.Warning != "" {
			warnings = append(warnings, out.Warning)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "ollama: cannot list models") {
		t.Errorf("warnings = %q, want ollama's failure once", warnings)
	}
}

func TestAutoModelsMayNameUnlistedModels(t *testing.T) {
	env := autoSetup(t, "none")
	settings := "[auto]\nclassifier = \"none\"\nmodels = [\"ollama/other\", \"ollama/hidden\", \"nowhere/x\"]\n"
	if err := os.WriteFile(filepath.Join(env.agentDir, "settings.toml"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSession(env, "", "auto")
	s.activeRouter()
	cfg, err := env.settings().Auto()
	if err != nil {
		t.Fatal(err)
	}
	ladder, problem, err := s.router.ladder(context.Background(), cfg)
	if err != nil || len(ladder) != 2 || ladder[0].Ref != "ollama/other" || ladder[1].Ref != "ollama/hidden" {
		t.Fatalf("ladder = %+v, err %v", ladder, err)
	}
	if !strings.Contains(problem, "nowhere/x") {
		t.Errorf("problem = %q, want the model that cannot be used named", problem)
	}
}

func TestAutoResolveReadsCredentialsEveryTime(t *testing.T) {
	env := agentDir(t, "")
	asked := noWindowLookups(t, 4096)
	r := newAutoRouter(env, &catalog{})

	t.Setenv("OLLAMA_API_KEY", "old")
	first, err := r.resolve("ollama/qwen")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OLLAMA_API_KEY", "new")
	second, err := r.resolve("ollama/qwen")
	if err != nil {
		t.Fatal(err)
	}
	if first.Options.APIKey != "old" || second.Options.APIKey != "new" {
		t.Errorf("keys = %q, %q; want the new login used at once", first.Options.APIKey, second.Options.APIKey)
	}
	if len(*asked) != 1 || second.Model.ContextWindow != 4096 {
		t.Errorf("asked the server %d times, window %d; want once, and the answer kept", len(*asked), second.Model.ContextWindow)
	}
}

func TestAutoEffortRouterFailureStillRecordsTheModel(t *testing.T) {
	env := recordingSetup(t)
	if err := os.WriteFile(filepath.Join(env.agentDir, "settings.toml"), []byte("[auto]\ntimeout_ms = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSession(env, "", "ollama/qwen")
	if err := s.SetEffort("auto"); err != nil {
		t.Fatal(err)
	}
	var events []tui.Event
	if err := s.Prompt(context.Background(), "rename x to count", collectEvents(&events)); err != nil {
		t.Fatal(err)
	}
	if got := modelChanges(t, env.agentDir); strings.Join(got, ",") != "qwen" {
		t.Errorf("model changes = %v, want the model that answered", got)
	}
	sessions, _ := sessionfile.LoadAll(env.agentDir)
	if e := sessions[0].Entries[0]; e.Type != sessionfile.TypeModelChange {
		t.Errorf("first entry = %+v, want the model before the prompt", e)
	}
}
