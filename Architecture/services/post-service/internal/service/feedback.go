package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
)

// validFeedbackTypes are the accepted product-feedback categories.
var validFeedbackTypes = map[string]bool{
	"bug":         true,
	"feature":     true,
	"performance": true,
	"content":     true,
	"ui":          true,
	"other":       true,
}

// SubmitFeedback validates and stores a user feedback note.
func (s *Service) SubmitFeedback(ctx context.Context, f *postgres.Feedback) error {
	f.Message = strings.TrimSpace(f.Message)
	if f.Message == "" {
		return fmt.Errorf("message is required")
	}
	if len(f.Message) > 5000 {
		return fmt.Errorf("message is too long (max 5000 chars)")
	}
	if f.FeedbackType == "" {
		f.FeedbackType = "other"
	}
	if !validFeedbackTypes[f.FeedbackType] {
		return fmt.Errorf("invalid feedback_type")
	}
	return s.pgStore.CreateFeedback(ctx, f)
}
