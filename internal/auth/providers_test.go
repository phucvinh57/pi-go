package auth

import (
	"slices"
	"testing"
)

func TestSupportedChatProviders(t *testing.T) {
	wantProviders := []string{"ollama", "openai", "openai-codex"}
	if got := Supported(); !slices.Equal(got, wantProviders) {
		t.Fatalf("Supported() = %v, want %v", got, wantProviders)
	}
	if got, want := len(LoginMethods()), len(Primary()); got != want {
		t.Errorf("%d login methods for %d providers", got, want)
	}
}
