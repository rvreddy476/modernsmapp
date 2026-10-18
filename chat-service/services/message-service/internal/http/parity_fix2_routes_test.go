package http

import (
	"context"
	"testing"

	"github.com/atpost/chat-message-service/internal/service"
	"github.com/google/uuid"
)

// Fix pass 2 (lane M): the mark-unread and conversation-report routes reach
// the service with the right inputs and map its errors.

type stubFix2 struct {
	stubParity
	unread  []bool
	reports []string
	fix2Err error
}

func (s *stubFix2) SetConversationUnread(_ context.Context, _, _ uuid.UUID, unread bool) error {
	s.unread = append(s.unread, unread)
	return s.fix2Err
}

func (s *stubFix2) ReportConversation(_ context.Context, _, _ uuid.UUID, reason, note string) error {
	s.reports = append(s.reports, reason+"|"+note)
	return s.fix2Err
}

func TestMarkUnreadRoute(t *testing.T) {
	stub := &stubFix2{}
	r := parityRouter(stub)
	path := "/v1/chat/conversations/" + uuid.NewString() + "/unread"
	if w := callParity(r, "PUT", path, `{"unread":true}`, nil); w.Code != 204 {
		t.Fatalf("mark unread: %d %s", w.Code, w.Body)
	}
	if w := callParity(r, "PUT", path, `{"unread":false}`, nil); w.Code != 204 {
		t.Fatalf("clear mark: %d %s", w.Code, w.Body)
	}
	if len(stub.unread) != 2 || !stub.unread[0] || stub.unread[1] {
		t.Fatalf("service saw %v", stub.unread)
	}
	// A body without the flag is refused, never read as false.
	for _, body := range []string{`{}`, `{"unread":"yes"}`, ``} {
		if w := callParity(r, "PUT", path, body, nil); w.Code != 400 {
			t.Fatalf("body %q: %d", body, w.Code)
		}
	}
	if len(stub.unread) != 2 {
		t.Fatalf("a refused body reached the service: %v", stub.unread)
	}
	stub.fix2Err = service.ErrNotMember
	if w := callParity(r, "PUT", path, `{"unread":true}`, nil); w.Code != 404 {
		t.Fatalf("non-member: %d", w.Code)
	}
	if w := callParity(r, "PUT", "/v1/chat/conversations/nope/unread", `{"unread":true}`, nil); w.Code != 400 {
		t.Fatalf("bad id: %d", w.Code)
	}
}

func TestConversationReportRoute(t *testing.T) {
	stub := &stubFix2{}
	r := parityRouter(stub)
	path := "/v1/chat/conversations/" + uuid.NewString() + "/report"
	if w := callParity(r, "POST", path, `{"reason":"scam","note":"asked for money"}`, nil); w.Code != 202 {
		t.Fatalf("report: %d %s", w.Code, w.Body)
	}
	if len(stub.reports) != 1 || stub.reports[0] != "scam|asked for money" {
		t.Fatalf("service saw %v", stub.reports)
	}
	if w := callParity(r, "POST", path, `{}`, nil); w.Code != 400 {
		t.Fatalf("missing reason: %d", w.Code)
	}
	stub.fix2Err = service.ErrInvalidReport
	if w := callParity(r, "POST", path, `{"reason":"boring"}`, nil); w.Code != 400 {
		t.Fatalf("invalid report: %d", w.Code)
	}
	stub.fix2Err = service.ErrNotMember
	if w := callParity(r, "POST", path, `{"reason":"spam"}`, nil); w.Code != 404 {
		t.Fatalf("non-member: %d", w.Code)
	}
}
