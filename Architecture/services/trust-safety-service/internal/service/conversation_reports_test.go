package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Chat-parity fix pass 2: conversation reports.

func uuids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = uuid.NewString()
	}
	return out
}

func TestConversationReportInputGroup(t *testing.T) {
	reporter, conv := uuid.New(), uuid.New()
	ids := uuids(3)
	in, err := ConversationReportInput(ConversationReport{
		ReporterID: reporter.String(), EntityType: " Conversation ", EntityID: strings.ToUpper(conv.String()),
		ConversationID: conv.String(), ConversationType: "group", Reason: "hate", Note: "slurs in the group",
		MessageIDs: ids, SourceEventID: "message-service:k1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.ReporterID != reporter || in.EntityType != ReportEntityConversation || in.EntityRef != conv.String() ||
		in.Reason != "hate" || in.Details != "slurs in the group" || in.SourceEventID != "message-service:k1" {
		t.Fatalf("input %+v", in)
	}
	ev := in.Evidence
	if ev == nil || ev.ConversationID != conv.String() || ev.ConversationType != "group" || len(ev.MessageIDs) != 3 ||
		ev.Source != EvidenceSourceMessageService {
		t.Fatalf("evidence %+v", ev)
	}
}

func TestConversationReportInputDirect(t *testing.T) {
	reporter, other, conv := uuid.New(), uuid.New(), uuid.New()
	in, err := ConversationReportInput(ConversationReport{
		ReporterID: reporter.String(), EntityType: "user", EntityID: other.String(),
		ConversationID: conv.String(), ConversationType: "direct", Reason: "scam",
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.EntityType != "user" || in.EntityRef != other.String() || in.Evidence.ConversationID != conv.String() ||
		in.Evidence.ConversationType != "direct" || in.SourceEventID != "" {
		t.Fatalf("input %+v evidence %+v", in, in.Evidence)
	}
}

func TestConversationReportInputRejects(t *testing.T) {
	reporter, other, conv := uuid.NewString(), uuid.NewString(), uuid.NewString()
	ok := func() ConversationReport {
		return ConversationReport{ReporterID: reporter, EntityType: "conversation", EntityID: conv,
			ConversationID: conv, ConversationType: "group", Reason: "spam"}
	}
	for name, mutate := range map[string]func(*ConversationReport){
		"bad reporter":         func(r *ConversationReport) { r.ReporterID = "x" },
		"nil reporter":         func(r *ConversationReport) { r.ReporterID = uuid.Nil.String() },
		"message entity":       func(r *ConversationReport) { r.EntityType = "message" },
		"post entity":          func(r *ConversationReport) { r.EntityType = "post" },
		"empty entity type":    func(r *ConversationReport) { r.EntityType = "" },
		"bad conversation":     func(r *ConversationReport) { r.ConversationID = "nope" },
		"nil conversation":     func(r *ConversationReport) { r.ConversationID = uuid.Nil.String() },
		"bad entity id":        func(r *ConversationReport) { r.EntityID = "nope" },
		"nil entity id":        func(r *ConversationReport) { r.EntityID = uuid.Nil.String() },
		"group names another":  func(r *ConversationReport) { r.EntityID = other },
		"self report":          func(r *ConversationReport) { r.EntityType, r.EntityID = "user", reporter },
		"no conversation type": func(r *ConversationReport) { r.ConversationType = "  " },
		"21 message ids":       func(r *ConversationReport) { r.MessageIDs = uuids(21) },
		"bad message id":       func(r *ConversationReport) { r.MessageIDs = []string{uuid.NewString(), "x"} },
		"nil message id":       func(r *ConversationReport) { r.MessageIDs = []string{uuid.Nil.String()} },
		"long source key":      func(r *ConversationReport) { r.SourceEventID = strings.Repeat("k", MaxSourceEventIDLength+1) },
	} {
		r := ok()
		mutate(&r)
		if _, err := ConversationReportInput(r); !errors.Is(err, ErrInvalidReport) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	// 20 ids is the ceiling, not over it.
	r := ok()
	r.MessageIDs = uuids(MaxEvidenceMessageIDs)
	if _, err := ConversationReportInput(r); err != nil {
		t.Fatalf("20 ids refused: %v", err)
	}
}

func TestEncodeEvidenceConversation(t *testing.T) {
	conv, a, b := uuid.New(), uuid.New(), uuid.New()
	raw, err := encodeEvidence(ReportEntityConversation, &ReportEvidence{
		ConversationID: conv.String(), ConversationType: " Group ",
		MessageIDs: []string{strings.ToUpper(a.String()), b.String(), a.String()},
		Source:     EvidenceSourceMessageService,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got ReportEvidence
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.ConversationType != "group" || len(got.MessageIDs) != 2 || got.MessageIDs[0] != a.String() || got.MessageIDs[1] != b.String() {
		t.Fatalf("evidence %s", raw)
	}
	for name, ev := range map[string]*ReportEvidence{
		"spaced type":   {ConversationID: conv.String(), ConversationType: "group chat", Source: EvidenceSourceMessageService},
		"long type":     {ConversationID: conv.String(), ConversationType: strings.Repeat("g", 33), Source: EvidenceSourceMessageService},
		"21 ids":        {ConversationID: conv.String(), MessageIDs: uuids(21), Source: EvidenceSourceMessageService},
		"bad id":        {ConversationID: conv.String(), MessageIDs: []string{"x"}, Source: EvidenceSourceMessageService},
		"client source": {ConversationID: conv.String(), Source: "client"},
	} {
		if _, err := encodeEvidence(ReportEntityConversation, ev); !errors.Is(err, ErrInvalidReport) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	// Only the ids alone still count as a snapshot.
	if raw, err := encodeEvidence("user", &ReportEvidence{MessageIDs: []string{a.String()}, Source: EvidenceSourceMessageService}); err != nil || raw == nil {
		t.Fatalf("ids-only evidence: %s, %v", raw, err)
	}
}

// FileReportWith validates conversation evidence itself (not only the route
// builder) and accepts the conversation entity type; a service with no store
// would panic past validation, so reaching an ErrInvalidReport proves it.
func TestFileReportWithConversationValidation(t *testing.T) {
	s := New(nil, nil)
	conv := uuid.New()
	_, err := s.FileReportWith(context.Background(), FileReportInput{
		ReporterID: uuid.New(), EntityType: ReportEntityConversation, EntityRef: conv.String(), Reason: "spam",
		Evidence: &ReportEvidence{ConversationID: conv.String(), MessageIDs: uuids(21), Source: EvidenceSourceMessageService},
	})
	if !errors.Is(err, ErrInvalidReport) || !strings.Contains(err.Error(), "message_ids") {
		t.Fatalf("21 ids through FileReportWith: %v", err)
	}
	_, err = s.FileReportWith(context.Background(), FileReportInput{
		ReporterID: uuid.New(), EntityType: ReportEntityConversation, EntityRef: "not-a-uuid", Reason: "spam",
	})
	if !errors.Is(err, ErrInvalidReport) || strings.Contains(err.Error(), "entity_type") {
		t.Fatalf("conversation entity type not accepted (or bad id not refused): %v", err)
	}
}
