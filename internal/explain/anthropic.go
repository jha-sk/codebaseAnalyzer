package explain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type AnthropicExplainer struct {
	APIKey     string
	HTTPClient *http.Client
	BaseURL    string // empty = real API; set in tests to an httptest.Server URL
}

func (a *AnthropicExplainer) Explain(ctx context.Context, tool, ruleID, sampleMessage string, count int) (Explanation, error) {
	url := a.BaseURL
	if url == "" {
		url = "https://api.anthropic.com/v1/messages"
	}
	body, _ := json.Marshal(map[string]any{
		"model":      "claude-sonnet-5",
		"max_tokens": 300,
		"messages":   []map[string]string{{"role": "user", "content": buildPrompt(tool, ruleID, sampleMessage, count)}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Explanation{}, err
	}
	req.Header.Set("x-api-key", a.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return Explanation{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Explanation{}, fmt.Errorf("anthropic: status %d", resp.StatusCode)
	}
	var parsed struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Explanation{}, err
	}
	if len(parsed.Content) == 0 {
		return Explanation{}, fmt.Errorf("anthropic: empty response")
	}
	return parseExplanation(parsed.Content[0].Text), nil
}
