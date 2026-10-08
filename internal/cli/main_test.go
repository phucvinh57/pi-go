package cli

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/phucvinh57/pi-go/internal/ai"
)

// TestMain keeps every test off the network: resolveModel would otherwise ask
// the (possibly real) Ollama server about model context windows.
func TestMain(m *testing.M) {
	windowFinder = func(context.Context, ai.Model) (int, error) { return 0, errors.New("offline in tests") }
	os.Exit(m.Run())
}
