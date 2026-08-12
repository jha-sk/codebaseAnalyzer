package explain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicExplainer_Explain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing/wrong x-api-key header")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"text": "Why it matters: X\nFix pattern: Y"}},
		})
	}))
	defer srv.Close()

	e := &AnthropicExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	exp, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Text != "X" || exp.FixPattern != "Y" {
		t.Errorf("got %+v", exp)
	}
}

func TestAnthropicExplainer_nonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	e := &AnthropicExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	exp, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %q, want it to mention status 429", err)
	}
	if exp != (Explanation{}) {
		t.Errorf("expected zero-value Explanation, got %+v", exp)
	}
}

func TestAnthropicExplainer_emptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"content": []map[string]string{}})
	}))
	defer srv.Close()

	e := &AnthropicExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	_, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err == nil {
		t.Fatal("expected error for empty content array, not a panic")
	}
}

func TestOpenAIExplainer_Explain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing/wrong Authorization header")
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "Why it matters: X\nFix pattern: Y"}}},
		})
	}))
	defer srv.Close()

	e := &OpenAIExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	exp, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Text != "X" || exp.FixPattern != "Y" {
		t.Errorf("got %+v", exp)
	}
}

func TestOpenAIExplainer_nonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	e := &OpenAIExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	exp, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %q, want it to mention status 429", err)
	}
	if exp != (Explanation{}) {
		t.Errorf("expected zero-value Explanation, got %+v", exp)
	}
}

func TestOpenAIExplainer_emptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{}})
	}))
	defer srv.Close()

	e := &OpenAIExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	_, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err == nil {
		t.Fatal("expected error for empty choices array, not a panic")
	}
}

func TestGeminiExplainer_Explain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("missing/wrong x-goog-api-key header")
		}
		if r.URL.Query().Get("key") != "" {
			t.Errorf("API key must not appear in the query string, got %q", r.URL.Query().Get("key"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]string{{"text": "Why it matters: X\nFix pattern: Y"}}}}},
		})
	}))
	defer srv.Close()

	e := &GeminiExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	exp, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Text != "X" || exp.FixPattern != "Y" {
		t.Errorf("got %+v", exp)
	}
}

func TestGeminiExplainer_nonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	e := &GeminiExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	exp, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %q, want it to mention status 429", err)
	}
	if exp != (Explanation{}) {
		t.Errorf("expected zero-value Explanation, got %+v", exp)
	}
}

func TestGeminiExplainer_emptyCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"candidates": []map[string]any{}})
	}))
	defer srv.Close()

	e := &GeminiExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	_, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err == nil {
		t.Fatal("expected error for empty candidates array, not a panic")
	}
}

func TestGeminiExplainer_emptyParts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]string{}}}},
		})
	}))
	defer srv.Close()

	e := &GeminiExplainer{APIKey: "test-key", HTTPClient: srv.Client(), BaseURL: srv.URL}
	_, err := e.Explain(context.Background(), "gosec", "G101", "sample", 3)
	if err == nil {
		t.Fatal("expected error for a candidate with an empty parts array, not a panic")
	}
}

func TestSelectProvider_priorityOrder(t *testing.T) {
	env := map[string]string{"OPENAI_API_KEY": "o-key", "GEMINI_API_KEY": "g-key"}
	getenv := func(k string) string { return env[k] }

	_, provider, ok, err := SelectProvider("", getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || provider != "openai" {
		t.Fatalf("provider = %q, ok = %v; want openai, true", provider, ok)
	}
}

func TestSelectProvider_flagOverridesEnv(t *testing.T) {
	env := map[string]string{"ANTHROPIC_API_KEY": "a-key", "GEMINI_API_KEY": "g-key"}
	getenv := func(k string) string { return env[k] }

	_, provider, ok, err := SelectProvider("gemini", getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || provider != "gemini" {
		t.Fatalf("provider = %q, ok = %v; want gemini, true", provider, ok)
	}
}

func TestSelectProvider_noneConfigured(t *testing.T) {
	getenv := func(k string) string { return "" }
	_, _, ok, err := SelectProvider("", getenv)
	if err != nil {
		t.Fatalf("expected nil error for the normal 'nothing configured' state, got %v", err)
	}
	if ok {
		t.Fatal("expected ok = false when no provider is configured")
	}
}

func TestSelectProvider_unknownProviderName(t *testing.T) {
	getenv := func(k string) string { return "" }
	e, _, ok, err := SelectProvider("bogus", getenv)
	if ok {
		t.Fatal("expected ok = false for an unrecognized provider name")
	}
	if err == nil {
		t.Fatal("expected an error for an unrecognized provider name")
	}
	if e != nil {
		t.Fatalf("expected a nil Explainer, got %+v", e)
	}
}

func TestSelectProvider_flagNamesProviderWithNoAPIKey(t *testing.T) {
	getenv := func(k string) string { return "" }
	e, _, ok, err := SelectProvider("anthropic", getenv)
	if ok {
		t.Fatal("expected ok = false when the named provider has no API key set")
	}
	if err == nil {
		t.Fatal("expected an error when the named provider has no API key set")
	}
	if e != nil {
		t.Fatalf("expected a nil Explainer, got %+v", e)
	}
}

func TestParseExplanation_noFixPatternLabelStripsPrefix(t *testing.T) {
	exp := parseExplanation("Why it matters: it just does")
	if exp.Text != "it just does" {
		t.Errorf("Text = %q, want prefix stripped and trimmed", exp.Text)
	}
	if exp.FixPattern != "" {
		t.Errorf("FixPattern = %q, want empty", exp.FixPattern)
	}
}
