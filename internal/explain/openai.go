package explain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type OpenAIExplainer struct {
	APIKey     string
	HTTPClient *http.Client
	BaseURL    string
}

func (o *OpenAIExplainer) Explain(ctx context.Context, tool, ruleID, sampleMessage string, count int) (Explanation, error) {
	url := o.BaseURL
	if url == "" {
		url = "https://api.openai.com/v1/chat/completions"
	}
	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o-mini",
		"messages": []map[string]string{{"role": "user", "content": buildPrompt(tool, ruleID, sampleMessage, count)}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Explanation{}, err
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.HTTPClient.Do(req)
	if err != nil {
		return Explanation{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Explanation{}, fmt.Errorf("openai: status %d", resp.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Explanation{}, err
	}
	if len(parsed.Choices) == 0 {
		return Explanation{}, fmt.Errorf("openai: empty response")
	}
	return parseExplanation(parsed.Choices[0].Message.Content), nil
}
