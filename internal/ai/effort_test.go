package ai

import "testing"

func TestEffortLevelsAndProviderMapping(t *testing.T) {
	want := []struct {
		level, codex, claude, openai, ollama string
	}{
		{"", "", "", "", ""},
		{"low", "light", "low", "low", "low"},
		{"medium", "medium", "medium", "medium", "medium"},
		{"high", "high", "high", "high", "high"},
		{"xhigh", "xhigh", "xhigh", "xhigh", "high"},
		{"max", "ultra", "max", "xhigh", "high"},
	}
	for _, tc := range want {
		if !ValidEffort(tc.level) {
			t.Errorf("%q is not valid", tc.level)
		}
		for _, p := range []struct{ name, mapped string }{
			{"openai-codex", tc.codex}, {"claude", tc.claude},
			{"openai", tc.openai}, {"ollama", tc.ollama},
		} {
			if got := providerEffort(p.name, tc.level); got != p.mapped {
				t.Errorf("%s / %q = %q, want %q", p.name, tc.level, got, p.mapped)
			}
		}
	}
	for _, bad := range []string{"light", "ultra", "xhign", "untra", "turbo"} {
		if ValidEffort(bad) {
			t.Errorf("%q should not be a normalized effort", bad)
		}
	}
}

func TestEffortSerializedInRequests(t *testing.T) {
	for _, tc := range []struct {
		provider, level, want string
	}{
		{"openai-codex", "low", "light"},
		{"openai-codex", "max", "ultra"},
		{"claude", "max", "max"},
		{"ollama", "max", "high"},
	} {
		m := Model{Provider: tc.provider, ID: "test"}
		o := Options{Reasoning: tc.level}
		if tc.provider == "openai-codex" {
			if got := codexBody(m, Context{}, o).Reasoning.Effort; got != tc.want {
				t.Errorf("codex %s: got %q, want %q", tc.level, got, tc.want)
			}
		} else if got := completionsBody(m, Context{}, o).ReasoningEffort; got != tc.want {
			t.Errorf("%s %s: got %q, want %q", tc.provider, tc.level, got, tc.want)
		}
	}
	if codexBody(Model{Provider: "openai-codex"}, Context{}, Options{}).Reasoning != nil {
		t.Error("default Codex effort should be omitted")
	}
	if completionsBody(Model{Provider: "ollama"}, Context{}, Options{}).ReasoningEffort != "" {
		t.Error("default completions effort should be omitted")
	}
}
