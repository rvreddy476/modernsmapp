package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	anthropicAPIURL    = "https://api.anthropic.com/v1/messages"
	anthropicModel     = "claude-haiku-4-5-20251001"
	anthropicVersion   = "2023-06-01"
	anthropicTimeout   = 10 * time.Second
	anthropicMaxTokens = 512
)

// AnthropicProvider implements TextProvider and ModerationProvider using the
// Anthropic Messages API via raw net/http — no SDK dependency.
type AnthropicProvider struct {
	apiKey     string
	httpClient *http.Client
}

// NewAnthropicProvider returns an AnthropicProvider configured with the given API key.
func NewAnthropicProvider(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: anthropicTimeout,
		},
	}
}

// anthropicRequest is the payload sent to the Anthropic Messages API.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse is the partial response from the Anthropic Messages API.
type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// call sends a single-turn prompt to the Anthropic API and returns the text reply.
func (a *AnthropicProvider) call(ctx context.Context, prompt string) (string, error) {
	payload := anthropicRequest{
		Model:     anthropicModel,
		MaxTokens: anthropicMaxTokens,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("anthropic: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("anthropic: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic: http call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var ar anthropicResponse
	if err := json.Unmarshal(respBody, &ar); err != nil {
		return "", fmt.Errorf("anthropic: unmarshal response: %w", err)
	}
	if ar.Error != nil {
		return "", fmt.Errorf("anthropic: api error %s: %s", ar.Error.Type, ar.Error.Message)
	}
	if len(ar.Content) == 0 {
		return "", fmt.Errorf("anthropic: empty response content")
	}
	return strings.TrimSpace(ar.Content[0].Text), nil
}

// parseJSONArray parses the first JSON string array found in text.
func parseJSONArray(text string) ([]string, error) {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON array found in: %s", text)
	}
	var result []string
	if err := json.Unmarshal([]byte(text[start:end+1]), &result); err != nil {
		return nil, fmt.Errorf("parse json array: %w", err)
	}
	return result, nil
}

// GenerateCaptions calls Anthropic to produce 3 caption suggestions.
func (a *AnthropicProvider) GenerateCaptions(ctx context.Context, content string, hints []string) ([]string, error) {
	hintStr := ""
	if len(hints) > 0 {
		hintStr = " Hints: " + strings.Join(hints, ", ") + "."
	}
	prompt := fmt.Sprintf(
		`Generate exactly 3 short, engaging social-media captions for the following content.%s
Return only a JSON array of 3 strings with no extra commentary.
Content: %s`, hintStr, content)

	reply, err := a.call(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("generate captions: %w", err)
	}
	captions, err := parseJSONArray(reply)
	if err != nil {
		slog.Warn("anthropic: could not parse captions JSON, splitting lines", "error", err)
		// Fallback: split on newlines.
		lines := strings.Split(reply, "\n")
		var result []string
		for _, l := range lines {
			l = strings.Trim(l, `"- `)
			if l != "" {
				result = append(result, l)
			}
		}
		if len(result) > 3 {
			result = result[:3]
		}
		return result, nil
	}
	return captions, nil
}

// GenerateHashtags calls Anthropic to suggest relevant hashtags.
func (a *AnthropicProvider) GenerateHashtags(ctx context.Context, content string) ([]string, error) {
	prompt := fmt.Sprintf(
		`Suggest 8-10 relevant hashtags for this social media post. Each hashtag must start with #.
Return only a JSON array of strings with no extra commentary.
Post content: %s`, content)

	reply, err := a.call(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("generate hashtags: %w", err)
	}
	tags, err := parseJSONArray(reply)
	if err != nil {
		slog.Warn("anthropic: could not parse hashtags JSON, extracting words", "error", err)
		var tags []string
		for _, word := range strings.Fields(reply) {
			if strings.HasPrefix(word, "#") {
				tags = append(tags, word)
			}
		}
		return tags, nil
	}
	return tags, nil
}

// SmartReply calls Anthropic for 3 reply suggestions.
func (a *AnthropicProvider) SmartReply(ctx context.Context, message string, conversationContext string) ([]string, error) {
	contextPart := ""
	if conversationContext != "" {
		contextPart = "\nConversation context: " + conversationContext
	}
	prompt := fmt.Sprintf(
		`Suggest exactly 3 short, natural reply options for the following message.%s
Return only a JSON array of 3 strings with no extra commentary.
Message: %s`, contextPart, message)

	reply, err := a.call(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("smart reply: %w", err)
	}
	replies, err := parseJSONArray(reply)
	if err != nil {
		slog.Warn("anthropic: could not parse smart-reply JSON, splitting lines", "error", err)
		lines := strings.Split(reply, "\n")
		var result []string
		for _, l := range lines {
			l = strings.Trim(l, `"- `)
			if l != "" {
				result = append(result, l)
			}
		}
		if len(result) > 3 {
			result = result[:3]
		}
		return result, nil
	}
	return replies, nil
}

// Summarize calls Anthropic to summarize text.
func (a *AnthropicProvider) Summarize(ctx context.Context, text string, maxLen int) (string, error) {
	prompt := fmt.Sprintf(
		`Summarize the following text in at most %d characters. Return only the summary, no extra commentary.
Text: %s`, maxLen, text)

	reply, err := a.call(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("summarize: %w", err)
	}
	if maxLen > 0 && len(reply) > maxLen {
		runes := []rune(reply)
		if maxLen > 3 {
			reply = string(runes[:maxLen-3]) + "..."
		} else {
			reply = string(runes[:maxLen])
		}
	}
	return reply, nil
}

// Translate calls Anthropic to translate text.
func (a *AnthropicProvider) Translate(ctx context.Context, text string, targetLang string) (string, error) {
	prompt := fmt.Sprintf(
		`Translate the following text into the language with BCP-47 code "%s".
Return only the translated text, no extra commentary.
Text: %s`, targetLang, text)

	reply, err := a.call(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("translate: %w", err)
	}
	return reply, nil
}

// ScamCheck calls Anthropic to assess scam/phishing risk.
func (a *AnthropicProvider) ScamCheck(ctx context.Context, text string) (float64, string, error) {
	prompt := fmt.Sprintf(
		`Analyse the following text for scam or phishing signals.
Return a JSON object with exactly two fields: "score" (float between 0 and 1) and "reason" (string).
No extra commentary.
Text: %s`, text)

	reply, err := a.call(ctx, prompt)
	if err != nil {
		return 0, "", fmt.Errorf("scam check: %w", err)
	}

	// Parse JSON object.
	start := strings.Index(reply, "{")
	end := strings.LastIndex(reply, "}")
	if start == -1 || end == -1 || end <= start {
		return 0.1, "parse error — treating as low risk", nil
	}
	var result struct {
		Score  float64 `json:"score"`
		Reason string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(reply[start:end+1]), &result); err != nil {
		slog.Warn("anthropic: could not parse scam check response", "error", err, "reply", reply)
		return 0.1, "parse error — treating as low risk", nil
	}
	return result.Score, result.Reason, nil
}

// PredictEngagement calls Anthropic to predict engagement.
func (a *AnthropicProvider) PredictEngagement(ctx context.Context, content string) (float64, error) {
	prompt := fmt.Sprintf(
		`Predict the social media engagement score (0.0 to 1.0) for the following post content.
Return a JSON object with one field: "score" (float between 0 and 1). No extra commentary.
Content: %s`, content)

	reply, err := a.call(ctx, prompt)
	if err != nil {
		return 0, fmt.Errorf("predict engagement: %w", err)
	}

	start := strings.Index(reply, "{")
	end := strings.LastIndex(reply, "}")
	if start == -1 || end == -1 || end <= start {
		return 0.5, nil
	}
	var result struct {
		Score float64 `json:"score"`
	}
	if err := json.Unmarshal([]byte(reply[start:end+1]), &result); err != nil {
		slog.Warn("anthropic: could not parse engagement prediction", "error", err, "reply", reply)
		return 0.5, nil
	}
	return result.Score, nil
}

// CheckContent calls Anthropic to evaluate content safety.
func (a *AnthropicProvider) CheckContent(ctx context.Context, text string, mediaURLs []string) (bool, float64, []string, error) {
	mediaInfo := ""
	if len(mediaURLs) > 0 {
		mediaInfo = fmt.Sprintf(" Media URLs: %s.", strings.Join(mediaURLs, ", "))
	}
	prompt := fmt.Sprintf(
		`Evaluate the following content for policy violations (hate speech, violence, spam, adult content, etc.).%s
Return a JSON object with three fields:
- "safe": boolean
- "score": float between 0 (safe) and 1 (very unsafe)
- "categories": array of violated category strings (empty array if safe)
No extra commentary.
Text: %s`, mediaInfo, text)

	reply, err := a.call(ctx, prompt)
	if err != nil {
		return true, 0, nil, fmt.Errorf("check content: %w", err)
	}

	start := strings.Index(reply, "{")
	end := strings.LastIndex(reply, "}")
	if start == -1 || end == -1 || end <= start {
		return true, 0, nil, nil
	}
	var result struct {
		Safe       bool     `json:"safe"`
		Score      float64  `json:"score"`
		Categories []string `json:"categories"`
	}
	if err := json.Unmarshal([]byte(reply[start:end+1]), &result); err != nil {
		slog.Warn("anthropic: could not parse moderation response", "error", err, "reply", reply)
		return true, 0, nil, nil
	}
	return result.Safe, result.Score, result.Categories, nil
}
