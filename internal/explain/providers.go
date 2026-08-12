package explain

import (
	"fmt"
	"net/http"
	"strings"
)

func buildPrompt(tool, ruleID, sampleMessage string, count int) string {
	return fmt.Sprintf(
		"A static analysis tool (%s, rule %s) flagged %d instance(s) in a production codebase. "+
			"Sample message: %q\n\n"+
			"In two short paragraphs: (1) why this matters in production, (2) a general fix pattern. "+
			"Label them exactly \"Why it matters:\" and \"Fix pattern:\".",
		tool, ruleID, count, sampleMessage)
}

func parseExplanation(text string) Explanation {
	why, fix := text, ""
	if idx := strings.Index(text, "Fix pattern:"); idx >= 0 {
		why = text[:idx]
		fix = strings.TrimSpace(text[idx+len("Fix pattern:"):])
	}
	why = strings.TrimSpace(strings.Replace(why, "Why it matters:", "", 1))
	return Explanation{Text: why, FixPattern: fix}
}

// SelectProvider picks the Explainer to use: an explicit flag wins outright,
// otherwise the first of ANTHROPIC_API_KEY/OPENAI_API_KEY/GEMINI_API_KEY that's set.
//
// ok=false with a nil error means nothing is configured — a normal state the
// CLI handles by printing raw findings. A non-nil error means the flag named
// an unrecognized provider, or named/detected a provider with no API key set;
// callers should treat that as a clean failure, never as a usable Explainer.
func SelectProvider(flagOverride string, getenv func(string) string) (Explainer, string, bool, error) {
	if flagOverride != "" {
		e, err := newProvider(flagOverride, getenv)
		if err != nil {
			return nil, "", false, err
		}
		return e, flagOverride, true, nil
	}
	for _, p := range []string{"anthropic", "openai", "gemini"} {
		if getenv(envVarFor(p)) != "" {
			e, err := newProvider(p, getenv)
			if err != nil {
				return nil, "", false, err
			}
			return e, p, true, nil
		}
	}
	return nil, "", false, nil
}

func envVarFor(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "gemini":
		return "GEMINI_API_KEY"
	}
	return ""
}

// newProvider constructs the named Explainer. It returns an error rather than
// a nil Explainer for both an unrecognized name and a recognized name with no
// API key configured, so a caller can never end up invoking Explain on nil.
func newProvider(name string, getenv func(string) string) (Explainer, error) {
	envVar := envVarFor(name)
	if envVar == "" {
		return nil, fmt.Errorf("unknown LLM provider %q (want anthropic|openai|gemini)", name)
	}
	apiKey := getenv(envVar)
	if apiKey == "" {
		return nil, fmt.Errorf("no API key found for provider %q (expected %s to be set)", name, envVar)
	}
	switch name {
	case "anthropic":
		return &AnthropicExplainer{APIKey: apiKey, HTTPClient: http.DefaultClient}, nil
	case "openai":
		return &OpenAIExplainer{APIKey: apiKey, HTTPClient: http.DefaultClient}, nil
	case "gemini":
		return &GeminiExplainer{APIKey: apiKey, HTTPClient: http.DefaultClient}, nil
	}
	return nil, fmt.Errorf("unknown LLM provider %q (want anthropic|openai|gemini)", name)
}
