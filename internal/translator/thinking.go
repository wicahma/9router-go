package translator

// LevelToEffort maps a thinking-level suffix to the reasoning_effort value an
// upstream expects. Each format's level list is already its own wire
// vocabulary, so only the two Anthropic shapes need collapsing: the API caps
// effort at "high" and folds xhigh/max into it (same normalization
// TranslateClaudeToOpenAI applies at request.go).
// ok is false for a format this gateway cannot express as an effort level.
func LevelToEffort(format, level string) (string, bool) {
	switch format {
	case "openai", "gemini-level":
		return level, true
	case "claude-adaptive", "claude-budget":
		if level == "max" || level == "xhigh" {
			return "high", true
		}
		return level, true
	default:
		return "", false
	}
}

// LevelToBudget maps a thinking level to the Claude disabled-switch budget.
// Boundaries mirror budgetToEffort, which is the inverse used on the way in.
func LevelToBudget(level string) int {
	switch level {
	case "low":
		return 4000
	case "medium":
		return 8000
	case "xhigh":
		return 24000
	case "max":
		return 32000
	default:
		return 16000
	}
}
