package service

import (
	"encoding/json"
	"testing"
)

// Module 1 fixes-v1 / Codex P1-5.
//
// "Do not label Reel scheduling fixed until an API test exercises the
// exact mobile payload." This is that payload, copied field-for-field
// from reels_caption_screen.dart's `body` map, wrapped exactly as the
// client sends it to POST /v1/posts/drafts.

// exactMobileReelBody mirrors reels_caption_screen.dart:
//
//	'content_type': 'flick',
//	'media_ids': [...],
//	'text': fullText,
//	'visibility': _audience.name,
//	'cover_frame_ms': editorState.coverFrameMs,
//	'filter': editorState.activeFilter.name,
//	'distribution': _distribution.toPolicyJson(),
//	'audio_track_id': audio.id,           // when music was selected
const exactMobileReelBody = `{
  "content_type": "flick",
  "media_ids": ["11111111-1111-4111-8111-111111111111"],
  "text": "sunset at marine drive\n\n#mumbai #sunset",
  "visibility": "public",
  "cover_frame_ms": 1500,
  "filter": "vivid",
  "distribution": {"version":1,"main_feed":true,"notify_subscribers":true},
  "audio_track_id": "22222222-2222-4222-8222-222222222222"
}`

func TestParseDraftPayload_AcceptsExactMobileReelBody(t *testing.T) {
	p, err := parseDraftPayload(json.RawMessage(exactMobileReelBody))
	if err != nil {
		t.Fatalf("the exact mobile reel payload must be accepted, got: %v", err)
	}
	if p.ContentType != "flick" {
		t.Errorf("content_type lost: %q", p.ContentType)
	}
	if p.CoverFrameMs == nil || *p.CoverFrameMs != 1500 {
		t.Error("cover_frame_ms lost")
	}
	if p.Filter != "vivid" {
		t.Errorf("filter lost: %q", p.Filter)
	}
	if p.AudioTrackID == nil || *p.AudioTrackID == "" {
		t.Error("audio_track_id lost — background music would vanish on schedule")
	}
	if len(p.MediaIDs) != 1 {
		t.Errorf("media_ids lost: %v", p.MediaIDs)
	}
	if len(p.Distribution) == 0 {
		t.Error("distribution policy lost")
	}
}

// The payload must also pass validation under the reel post_type.
func TestValidateDraft_AcceptsReelPayload(t *testing.T) {
	p, err := parseDraftPayload(json.RawMessage(exactMobileReelBody))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, postType := range []string{"reel", "video", "post"} {
		if err := validateDraft(postType, p); err != nil {
			t.Errorf("validateDraft(%q) rejected the mobile reel payload: %v", postType, err)
		}
	}
}

// Reel drafts must publish as video posts, not text posts.
func TestValidDraftPostTypes_IncludesReel(t *testing.T) {
	for _, pt := range []string{"post", "poll", "article", "reel", "video"} {
		if !validDraftPostTypes[pt] {
			t.Errorf("post_type %q must be a valid draft type", pt)
		}
	}
	if validDraftPostTypes["nonsense"] {
		t.Error("unknown post types must stay rejected")
	}
}

// Regression guard: unknown fields are still rejected, so a future client
// field cannot be silently dropped (the reason DisallowUnknownFields
// exists). The fix widened the schema, it did not disable the check.
func TestParseDraftPayload_StillRejectsTrulyUnknownFields(t *testing.T) {
	_, err := parseDraftPayload(json.RawMessage(`{"text":"x","totally_new_field":1}`))
	if err == nil {
		t.Fatal("unknown fields must still be rejected loudly")
	}
}

// The PostTube scheduled-upload payload must round-trip too.
func TestParseDraftPayload_AcceptsPostTubeSchedulePayload(t *testing.T) {
	const body = `{
	  "content_type": "long_video",
	  "media_ids": ["33333333-3333-4333-8333-333333333333"],
	  "title": "How to make filter coffee",
	  "text": "full recipe\n\n#coffee",
	  "visibility": "public",
	  "distribution": {"version":1,"main_feed":false,"notify_subscribers":true}
	}`
	p, err := parseDraftPayload(json.RawMessage(body))
	if err != nil {
		t.Fatalf("the PostTube schedule payload must be accepted, got: %v", err)
	}
	if p.Title == "" {
		t.Error("title lost")
	}
	if err := validateDraft("video", p); err != nil {
		t.Errorf("validateDraft rejected the PostTube payload: %v", err)
	}
}
