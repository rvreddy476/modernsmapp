package service

import (
	"reflect"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
)

// Creator Hub rows (2026-09-27): processing_status and flags are pure
// functions of the post's media state and publishing state.
func TestUploadProcessingStatusAndFlags(t *testing.T) {
	at := time.Now().Add(time.Hour)
	cases := []struct {
		name       string
		post       *postgres.Post
		wantStatus string
		wantFlags  []string
	}{
		{"nil", nil, "none", []string{}},
		{"no media", &postgres.Post{ReviewStatus: "approved"}, "none", []string{}},
		{"ready", &postgres.Post{ReviewStatus: "approved", Media: []postgres.PostMedia{{ProcessingStatus: "ready", ModerationStatus: "passed"}}}, "ready", []string{}},
		{"processing", &postgres.Post{ReviewStatus: "approved", IsProcessing: true, Media: []postgres.PostMedia{{ProcessingStatus: "processing"}}}, "processing", []string{}},
		{"failed asset", &postgres.Post{ReviewStatus: "approved", IsProcessing: true, Media: []postgres.PostMedia{{ProcessingStatus: "ready"}, {ProcessingStatus: "failed"}}}, "failed", []string{UploadFlagProcessingFailed}},
		{"rejected by moderation", &postgres.Post{ReviewStatus: "approved", Media: []postgres.PostMedia{{ProcessingStatus: "ready", ModerationStatus: "rejected"}}}, "failed", []string{UploadFlagProcessingFailed}},
		{"review hold", &postgres.Post{ReviewStatus: "flagged", Media: []postgres.PostMedia{{ProcessingStatus: "ready"}}}, "ready", []string{UploadFlagReviewHold}},
		{"pending review", &postgres.Post{ReviewStatus: "pending"}, "none", []string{UploadFlagReviewHold}},
		{"kids + scheduled", &postgres.Post{ReviewStatus: "approved", IsMadeForKids: true, PublishAt: &at}, "none", []string{UploadFlagMadeForKids, UploadFlagScheduled}},
		{"everything", &postgres.Post{ReviewStatus: "flagged", IsMadeForKids: true, PublishAt: &at, Media: []postgres.PostMedia{{ProcessingStatus: "failed"}}}, "failed",
			[]string{UploadFlagProcessingFailed, UploadFlagReviewHold, UploadFlagMadeForKids, UploadFlagScheduled}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UploadProcessingStatus(tc.post); got != tc.wantStatus {
				t.Fatalf("status=%q want %q", got, tc.wantStatus)
			}
			if got := UploadFlags(tc.post); !reflect.DeepEqual(got, tc.wantFlags) {
				t.Fatalf("flags=%v want %v", got, tc.wantFlags)
			}
		})
	}
}
