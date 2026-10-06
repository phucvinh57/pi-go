package cli

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// TestMain keeps every test off the network: resolveModel and canReason would
// otherwise ask the (possibly real) Ollama server about models.
func TestMain(m *testing.M) {
	windowFinder = func(context.Context, ai.Model) (int, error) { return 0, errors.New("offline in tests") }
	thinkingFinder = func(context.Context, ai.Model) (bool, error) { return false, errors.New("offline in tests") }
	os.Exit(m.Run())
}
