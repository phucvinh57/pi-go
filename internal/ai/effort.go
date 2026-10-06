package ai

// providerEffort maps a session-level effort to the vocabulary accepted by a
// provider. Empty leaves the model's default untouched. Unknown OpenAI-compatible
// providers use the common low/medium/high range rather than sending a level
// they may not understand.
func providerEffort(provider, level string) string {
	if level == "" {
		return ""
	}
	switch provider {
	case "openai-codex":
		switch level {
		case "low":
			return "light"
		case "max":
			return "ultra"
		}
	case "claude", "anthropic":
		return level
	case "openai":
		if level == "max" {
			return "xhigh"
		}
	default:
		if level == "xhigh" || level == "max" {
			return "high"
		}
	}
	return level
}
