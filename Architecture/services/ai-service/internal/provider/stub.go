package provider

import (
	"context"
	"strings"
	"unicode"
)

// StubTextProvider is the default dev/test implementation of TextProvider.
// It uses simple heuristics and keyword extraction — no external calls.
type StubTextProvider struct{}

// NewStubTextProvider returns a new StubTextProvider.
func NewStubTextProvider() *StubTextProvider {
	return &StubTextProvider{}
}

// GenerateCaptions generates 3 captions based on keyword extraction from content.
func (s *StubTextProvider) GenerateCaptions(_ context.Context, content string, hints []string) ([]string, error) {
	keywords := extractKeywords(content)
	if len(hints) > 0 {
		keywords = append(hints, keywords...)
	}

	if len(keywords) == 0 {
		return []string{
			"Sharing this moment with you",
			"Life is beautiful",
			"Making memories every day",
		}, nil
	}

	kw := strings.Join(dedupe(keywords[:min(3, len(keywords))]), ", ")
	return []string{
		"Loving every moment of this — " + kw,
		"Can't stop thinking about " + kw,
		"When " + kw + " hits different",
	}, nil
}

// GenerateHashtags extracts keywords and appends popular generic tags.
func (s *StubTextProvider) GenerateHashtags(_ context.Context, content string) ([]string, error) {
	keywords := extractKeywords(content)
	generic := []string{"#trending", "#viral", "#explore", "#postbook", "#lifestyle", "#content"}

	var tags []string
	for _, kw := range keywords {
		if len(tags) >= 6 {
			break
		}
		tags = append(tags, "#"+strings.ToLower(strings.ReplaceAll(kw, " ", "")))
	}
	tags = append(tags, generic...)
	return dedupe(tags)[:min(10, len(dedupe(tags)))], nil
}

// SmartReply returns 3 context-appropriate canned replies based on message tone.
func (s *StubTextProvider) SmartReply(_ context.Context, message string, _ string) ([]string, error) {
	lower := strings.ToLower(message)
	switch {
	case containsAny(lower, "thank", "thanks", "appreciate"):
		return []string{
			"You're welcome!",
			"Happy to help anytime",
			"Glad I could be of service",
		}, nil
	case containsAny(lower, "help", "assist", "support", "how"):
		return []string{
			"Sure, I can help with that!",
			"Let me look into this for you",
			"Of course — what do you need?",
		}, nil
	case containsAny(lower, "hi", "hello", "hey", "good morning", "good evening"):
		return []string{
			"Hey! How's it going?",
			"Hello there!",
			"Hi! Great to hear from you",
		}, nil
	case containsAny(lower, "bye", "goodbye", "see you", "later"):
		return []string{
			"Take care!",
			"See you soon!",
			"Goodbye — have a great day",
		}, nil
	default:
		return []string{
			"Sounds great!",
			"Thanks for letting me know",
			"I'll get back to you soon",
		}, nil
	}
}

// Summarize returns the first 2 sentences of the text, capped at maxLen.
func (s *StubTextProvider) Summarize(_ context.Context, text string, maxLen int) (string, error) {
	sentences := splitSentences(text)
	var summary string
	if len(sentences) >= 2 {
		summary = sentences[0] + " " + sentences[1]
	} else if len(sentences) == 1 {
		summary = sentences[0]
	} else {
		summary = text
	}
	if maxLen > 0 && len(summary) > maxLen {
		runes := []rune(summary)
		if maxLen > 3 {
			summary = string(runes[:maxLen-3]) + "..."
		} else {
			summary = string(runes[:maxLen])
		}
	}
	return strings.TrimSpace(summary), nil
}

// Translate returns the stub translation format: [targetLang] text.
func (s *StubTextProvider) Translate(_ context.Context, text string, targetLang string) (string, error) {
	return "[" + targetLang + "] " + text, nil
}

// ScamCheck returns 0.1 for normal text and 0.9 for text containing known scam phrases.
func (s *StubTextProvider) ScamCheck(_ context.Context, text string) (float64, string, error) {
	lower := strings.ToLower(text)
	scamPhrases := []string{
		"click here", "won $", "free gift", "claim your prize",
		"limited offer", "act now", "verify your account", "send money",
		"wire transfer", "nigerian prince", "lottery winner", "congratulations you",
	}
	for _, phrase := range scamPhrases {
		if strings.Contains(lower, phrase) {
			return 0.9, "contains known scam phrase: " + phrase, nil
		}
	}
	return 0.1, "no scam signals detected", nil
}

// PredictEngagement returns a fixed moderate engagement score.
func (s *StubTextProvider) PredictEngagement(_ context.Context, content string) (float64, error) {
	// Simple heuristic: longer content with questions/exclamations scores higher.
	score := 0.45
	lower := strings.ToLower(content)
	if strings.Contains(lower, "?") {
		score += 0.1
	}
	if strings.Contains(lower, "!") {
		score += 0.05
	}
	if len(content) > 100 {
		score += 0.05
	}
	if score > 1.0 {
		score = 1.0
	}
	return score, nil
}

// StubModerationProvider is the default dev/test implementation of ModerationProvider.
type StubModerationProvider struct{}

// NewStubModerationProvider returns a new StubModerationProvider.
func NewStubModerationProvider() *StubModerationProvider {
	return &StubModerationProvider{}
}

// bannedWords is a list of ~20 banned words/phrases used for stub moderation.
var bannedWords = []string{
	"fuck", "shit", "asshole", "bitch", "cunt", "bastard", "nigger", "faggot",
	"retard", "whore", "slut", "dick", "pussy", "cock", "motherfucker",
	"kill yourself", "kys", "rape", "pedophile", "terrorist",
}

// CheckContent scans text for banned words. Returns safe=false if any are found.
func (s *StubModerationProvider) CheckContent(_ context.Context, text string, _ []string) (bool, float64, []string, error) {
	lower := strings.ToLower(text)
	var violated []string
	for _, word := range bannedWords {
		if strings.Contains(lower, word) {
			violated = append(violated, word)
		}
	}
	if len(violated) > 0 {
		return false, 0.9, []string{"hate_speech", "profanity"}, nil
	}
	return true, 0.05, nil, nil
}

// --- helpers ---

// extractKeywords splits text on whitespace/punctuation and returns words longer than 4 chars.
func extractKeywords(text string) []string {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := map[string]bool{}
	var keywords []string
	for _, w := range words {
		if len(w) > 4 && !seen[strings.ToLower(w)] {
			seen[strings.ToLower(w)] = true
			keywords = append(keywords, w)
		}
	}
	return keywords
}

// splitSentences splits text into sentences on ., !, ?.
func splitSentences(text string) []string {
	var sentences []string
	var current strings.Builder
	for _, r := range text {
		current.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			s := strings.TrimSpace(current.String())
			if s != "" {
				sentences = append(sentences, s)
			}
			current.Reset()
		}
	}
	if rest := strings.TrimSpace(current.String()); rest != "" {
		sentences = append(sentences, rest)
	}
	return sentences
}

// containsAny reports whether s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// dedupe removes duplicate strings while preserving order.
func dedupe(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
