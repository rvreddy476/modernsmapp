package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Chat-parity CONTRACT §2.6 / §8: message reports.

func TestResolveReportEntityMessage(t *testing.T) {
	conv, msg := uuid.New(), uuid.New()
	id, key, err := ResolveReportEntity(ReportEntityMessage, " "+strings.ToUpper(conv.String())+":"+msg.String()+" ")
	if err != nil {
		t.Fatal(err)
	}
	if id != msg || key != conv.String()+":"+msg.String() {
		t.Fatalf("stored (%s, %q)", id, key)
	}
	for _, bad := range []string{
		msg.String(),                              // no conversation
		conv.String() + ":",                       // no message
		conv.String() + ":nope",                   // not a UUID
		conv.String() + ":" + msg.String() + ":x", // three parts
		uuid.Nil.String() + ":" + msg.String(),    // nil conversation
		conv.String() + ":" + uuid.Nil.String(),   // nil message
		"",
	} {
		if _, _, err := ResolveReportEntity(ReportEntityMessage, bad); !errors.Is(err, ErrInvalidReport) {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}

func TestMessageReportReasonShortForms(t *testing.T) {
	for in, want := range map[string]string{
		"spam": "spam", "harassment": "harassment", "hate": "hate_abuse", "sexual": "sexual_content",
		"violence": "violence_threat", "scam": "scam_fraud", "other": "other",
	} {
		_, got := NormalizeReportInput("message", in)
		if got != want || !validReportCategories[got] {
			t.Errorf("reason %q -> %q, want %q (valid=%v)", in, got, want, validReportCategories[got])
		}
	}
}

func TestEncodeEvidence(t *testing.T) {
	media, sender := uuid.New(), uuid.New()
	raw, err := encodeEvidence(ReportEntityMessage, &ReportEvidence{
		Text: "hi", MediaID: strings.ToUpper(media.String()), SenderID: sender.String(), MessageType: " Media ",
		Source: EvidenceSourceMessageService,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got ReportEvidence
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.MediaID != media.String() || got.MessageType != "media" || got.Source != EvidenceSourceMessageService || got.CapturedAt.IsZero() {
		t.Fatalf("evidence %+v", got)
	}
	// Exactly the ceiling passes.
	if _, err := encodeEvidence(ReportEntityMessage, &ReportEvidence{Text: strings.Repeat("é", MaxEvidenceTextRunes), Source: EvidenceSourceReporter}); err != nil {
		t.Fatalf("2000 runes rejected: %v", err)
	}
	bad := []struct {
		name   string
		entity string
		ev     ReportEvidence
	}{
		{"text too long", ReportEntityMessage, ReportEvidence{Text: strings.Repeat("a", MaxEvidenceTextRunes+1), Source: EvidenceSourceReporter}},
		{"media id not a uuid", ReportEntityMessage, ReportEvidence{MediaID: "x", Source: EvidenceSourceReporter}},
		{"sender not a uuid", ReportEntityMessage, ReportEvidence{SenderID: "x", Source: EvidenceSourceReporter}},
		{"caller-chosen source", ReportEntityMessage, ReportEvidence{Text: "x", Source: "admin"}},
		{"empty source", ReportEntityMessage, ReportEvidence{Text: "x"}},
		{"evidence on a post", "post", ReportEvidence{Text: "x", Source: EvidenceSourceReporter}},
		{"type too long", ReportEntityMessage, ReportEvidence{MessageType: strings.Repeat("t", 33), Source: EvidenceSourceReporter}},
	}
	for _, tc := range bad {
		ev := tc.ev
		if _, err := encodeEvidence(tc.entity, &ev); !errors.Is(err, ErrInvalidReport) {
			t.Errorf("%s: accepted (%v)", tc.name, err)
		}
	}
	if raw, err := encodeEvidence(ReportEntityMessage, nil); raw != nil || err != nil {
		t.Fatalf("nil evidence: %s %v", raw, err)
	}
}

// Every validation failure happens before the store: a service with no
// database is enough, and would panic if a case reached it.
func TestFileReportWithValidatesBeforeTheStore(t *testing.T) {
	s := New(nil, nil)
	conv, msg := uuid.New(), uuid.New()
	ref := conv.String() + ":" + msg.String()
	cases := map[string]FileReportInput{
		"nil reporter":      {ReporterID: uuid.Nil, EntityType: "message", EntityRef: ref, Reason: "spam"},
		"bad message ref":   {ReporterID: uuid.New(), EntityType: "message", EntityRef: msg.String(), Reason: "spam"},
		"bad reason":        {ReporterID: uuid.New(), EntityType: "message", EntityRef: ref, Reason: "bored"},
		"evidence too long": {ReporterID: uuid.New(), EntityType: "message", EntityRef: ref, Reason: "spam", Evidence: &ReportEvidence{Text: strings.Repeat("a", 2001), Source: EvidenceSourceReporter}},
		"key too long":      {ReporterID: uuid.New(), EntityType: "message", EntityRef: ref, Reason: "spam", SourceEventID: strings.Repeat("k", MaxSourceEventIDLength+1)},
	}
	for name, in := range cases {
		if _, err := s.FileReportWith(context.Background(), in); !errors.Is(err, ErrInvalidReport) {
			t.Errorf("%s: %v, want ErrInvalidReport", name, err)
		}
	}
}

func TestMessageRequestReportInput(t *testing.T) {
	conv, sender, receiver, msg := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	e := MessageRequestReported{
		ConversationID: conv.String(), SenderID: sender.String(), ReceiverID: receiver.String(),
		Preview: "buy now", OccurredAt: at,
	}

	in, ok := MessageRequestReportInput(e)
	if !ok {
		t.Fatal("valid event rejected")
	}
	if in.ReporterID != receiver || in.EntityType != "user" || in.EntityRef != sender.String() {
		t.Fatalf("no message id: want a user report on the sender, got %+v", in)
	}
	if in.Reason != "other" || in.SourceEventID != MessageRequestReportSourceKey(conv) {
		t.Fatalf("reason/source %q %q", in.Reason, in.SourceEventID)
	}
	if in.Evidence == nil || in.Evidence.Text != "buy now" || in.Evidence.ConversationID != conv.String() ||
		in.Evidence.SenderID != sender.String() || in.Evidence.Source != EvidenceSourceRequestEvent || !in.Evidence.CapturedAt.Equal(at) {
		t.Fatalf("evidence %+v", in.Evidence)
	}
	if _, err := encodeEvidence(in.EntityType, in.Evidence); err != nil {
		t.Fatalf("mapped evidence does not validate: %v", err)
	}

	// A newer publisher naming the message and a reason.
	e.MessageID, e.Reason = msg.String(), "scam"
	in, _ = MessageRequestReportInput(e)
	if in.EntityType != ReportEntityMessage || in.EntityRef != conv.String()+":"+msg.String() || in.Reason != "scam_fraud" {
		t.Fatalf("with message id: %+v", in)
	}
	// The key is the conversation, whatever else changes.
	if in.SourceEventID != MessageRequestReportSourceKey(conv) {
		t.Fatalf("source key %q", in.SourceEventID)
	}
	// Unknown reason falls back to other.
	e.Reason = "bored"
	if in, _ = MessageRequestReportInput(e); in.Reason != "other" {
		t.Fatalf("unknown reason kept: %q", in.Reason)
	}
	// Long previews are capped to the evidence ceiling, never rejected.
	e.Preview = strings.Repeat("x", 2500)
	if in, _ = MessageRequestReportInput(e); len([]rune(in.Evidence.Text)) != MaxEvidenceTextRunes {
		t.Fatalf("preview not capped: %d", len([]rune(in.Evidence.Text)))
	}

	for name, bad := range map[string]MessageRequestReported{
		"no conversation": {SenderID: sender.String(), ReceiverID: receiver.String()},
		"bad sender":      {ConversationID: conv.String(), SenderID: "x", ReceiverID: receiver.String()},
		"no receiver":     {ConversationID: conv.String(), SenderID: sender.String()},
		"self report":     {ConversationID: conv.String(), SenderID: sender.String(), ReceiverID: sender.String()},
	} {
		if _, ok := MessageRequestReportInput(bad); ok {
			t.Errorf("%s accepted", name)
		}
	}
}
