package service

import (
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/notification-service/internal/push"
)

// Audit 2026-10-30 H1/M2: every dating push names the Android app's private
// "dating" channel, and a safety alert carries its copy in data so the app
// can draw it in the foreground.

func TestSafetyAlertPushDataCarriesCopyAndDatingChannel(t *testing.T) {
	entity := uuid.New()
	for _, typ := range []string{
		"dating.safety.panic.trusted_contact",
		"dating.safety.location_shared",
		"dating.safety.panic.responder",
		DatingScamAlertType,
	} {
		a := SafetyAlert{
			Recipient: uuid.New(), NotifType: typ, EntityID: entity,
			DeepLink: "/dating/safety/alerts/" + entity.String(),
			Title:    "Someone who trusts you needs help", Body: "Open the app to check on them.",
			Identity: typ + ":" + entity.String(),
		}
		data := safetyAlertPushData(a)
		if data["title"] != a.Title || data["body"] != a.Body {
			t.Errorf("%s: data copy = %q / %q, want the notification copy", typ, data["title"], data["body"])
		}
		if data[push.AndroidChannelDataKey] != "dating" {
			t.Errorf("%s: channel = %q, want dating", typ, data[push.AndroidChannelDataKey])
		}
		if data["type"] != typ || data["entity_id"] != entity.String() ||
			data["deep_link"] != a.DeepLink || data["collapse_key"] != a.Identity {
			t.Errorf("%s: routing data changed: %v", typ, data)
		}
	}
}

func TestSafetyAlertPushDataNonDatingTypeHasNoChannel(t *testing.T) {
	data := safetyAlertPushData(SafetyAlert{NotifType: "account.security_alert", Title: "t", Body: "b"})
	if _, ok := data[push.AndroidChannelDataKey]; ok {
		t.Fatalf("non-dating safety alert named a channel: %v", data)
	}
	if data["title"] != "t" || data["body"] != "b" {
		t.Fatalf("data copy = %v", data)
	}
}

func TestBuildPushDataDatingTypesUseDatingChannel(t *testing.T) {
	user, entity := uuid.New(), uuid.New()
	for _, typ := range []string{
		"dating.spark.created",
		"dating.match.formed",
		"dating.match.first_message",
		"dating.match.new_message",
		"dating.match.expired",
		"dating.match.quiet",
		"dating.vouch.requested",
		"dating.vouch.accepted",
		"dating.safe_meet.reminder",
		"dating.safe_meet.missed_check_in",
		"dating.safety.panic.acknowledged",
		"dating.report.status_updated",
		"dating.verification.completed",
		"dating.verification.rejected",
		"dating.photo.rejected",
		"dating.premium.payment_failure",
		"dating.data_export.ready",
		DatingScamAlertType,
		DatingDateCheckinType,
	} {
		_, _, data := buildPushData(user, typ, "dating_match", entity, "/dating", RenderOverride{})
		if data[push.AndroidChannelDataKey] != "dating" {
			t.Errorf("%s: channel = %q, want dating", typ, data[push.AndroidChannelDataKey])
		}
	}
}

func TestBuildPushDataDatingChannelDoesNotOverrideCaller(t *testing.T) {
	_, _, data := buildPushData(uuid.New(), "dating.match.new_message", "dating_match", uuid.New(), "/dating",
		RenderOverride{Data: map[string]string{push.AndroidChannelDataKey: "dating_custom"}})
	if data[push.AndroidChannelDataKey] != "dating_custom" {
		t.Fatalf("channel = %q, a caller's channel must be kept", data[push.AndroidChannelDataKey])
	}
}

func TestBuildPushDataNonDatingTypesGetNoDatingChannel(t *testing.T) {
	for _, typ := range []string{"like", "comment", "follow", "food_order_new", "datingish"} {
		_, _, data := buildPushData(uuid.New(), typ, "post", uuid.New(), "/x", RenderOverride{})
		if data[push.AndroidChannelDataKey] == "dating" {
			t.Errorf("%s: got the dating channel", typ)
		}
	}
}
