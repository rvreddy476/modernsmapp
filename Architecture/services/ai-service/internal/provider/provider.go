package provider

import "context"

// TextProvider is the abstraction for all text-based AI operations.
type TextProvider interface {
	// GenerateCaptions returns up to 3 caption suggestions for the given content.
	// hints are optional keywords or context hints.
	GenerateCaptions(ctx context.Context, content string, hints []string) ([]string, error)

	// GenerateHashtags returns hashtag suggestions for the given content.
	GenerateHashtags(ctx context.Context, content string) ([]string, error)

	// SmartReply returns 3 reply suggestions given a message and optional conversation context.
	SmartReply(ctx context.Context, message string, conversationContext string) ([]string, error)

	// Summarize returns a summary of text no longer than maxLen characters.
	Summarize(ctx context.Context, text string, maxLen int) (string, error)

	// Translate translates text into the target language (BCP-47 code, e.g. "fr", "es").
	Translate(ctx context.Context, text string, targetLang string) (string, error)

	// ScamCheck analyses text for scam/phishing signals.
	// Returns a risk score in [0,1], a human-readable reason, and an error.
	ScamCheck(ctx context.Context, text string) (score float64, reason string, err error)

	// PredictEngagement returns a predicted engagement score in [0,1].
	PredictEngagement(ctx context.Context, content string) (float64, error)
}

// ModerationProvider is the abstraction for content-safety checks.
type ModerationProvider interface {
	// CheckContent returns whether content is safe to publish.
	// categories contains the violated policy categories when safe is false.
	CheckContent(ctx context.Context, text string, mediaURLs []string) (safe bool, score float64, categories []string, err error)
}
