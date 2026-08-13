package explain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type GeminiExplainer struct {
	APIKey     string
	HTTPClient *http.Client
	BaseURL    string
}

func (g *GeminiExplainer) Explain(ctx context.Context, tool, ruleID, sampleMessage string, count int) (Explanation, error) {
	reqURL := g.BaseURL
	if reqURL == "" {
		reqURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"
	}

	body, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{{"parts": []map[string]string{{"text": buildPrompt(tool, ruleID, sampleMessage, count)}}}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return Explanation{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	// The API key never goes in the URL: http.Client wraps transport failures
	// (DNS, connection, TLS, timeout) in a *url.Error whose Error() string
	// embeds the request URL verbatim, which would leak the key into any
	// logged or wrapped error.
	req.Header.Set("x-goog-api-key", g.APIKey)

	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return Explanation{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Explanation{}, fmt.Errorf("gemini: status %d", resp.StatusCode)
	}
	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Explanation{}, err
	}
	// We never set candidateCount, so Gemini returns exactly one candidate;
	// indexing Candidates[0] isn't a positional gamble the way Parts[0] is.
	if len(parsed.Candidates) == 0 {
		return Explanation{}, fmt.Errorf("gemini: empty response")
	}
	// The parts array is not guaranteed to have the text part first - Gemini
	// can emit thought/function-call/inline-data parts ahead of the text part.
	// Indexing Parts[0] positionally would silently return an empty part
	// instead of the answer. Select the first part whose Text is non-empty.
	for _, part := range parsed.Candidates[0].Content.Parts {
		if part.Text != "" {
			return parseExplanation(part.Text), nil
		}
	}
	return Explanation{}, fmt.Errorf("gemini: no text part in response")
}
