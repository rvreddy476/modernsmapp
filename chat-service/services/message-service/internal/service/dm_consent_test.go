package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/ratelimit"
	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// DM consent model unit tests (contract §4-§5). The Postgres behaviour of
// open/reopen/promote, the expiry sweep, the request list query and the
// accepted-chat state is proven against a real *_test database in
// internal/store/postgres/dm_consent_integration_test.go and end to end in
// internal/http/dm_consent_integration_test.go.

var errStopAfterOpen = errors.New("stop: open recorded")

// consentFake is the governance fake plus the consent-model surfaces.
type consentFake struct {
	*governanceFake
	opens       []postgres.DirectOpenMode
	openOutcome postgres.DirectOpenOutcome
	gateErr     error
	envelopes   []postgres.RequestEnvelope
	pageReads   int
	expiryDue   int
	expiryCalls int
	cursors     map[uuid.UUID]postgres.ReadCursor
}

func newConsentFake() *consentFake {
	return &consentFake{governanceFake: newGovernanceFake(), cursors: map[uuid.UUID]postgres.ReadCursor{}}
}

// OpenDirectConversation records the mode, runs the rate-limit gate when
// the configured outcome writes a new request (as the store does), then
// stops the flow before the response is built.
func (f *consentFake) OpenDirectConversation(ctx context.Context, actorID, otherID uuid.UUID, mode postgres.DirectOpenMode, gate func(context.Context) error) (postgres.DirectOpenResult, error) {
	f.opens = append(f.opens, mode)
	if f.openOutcome.IsNewRequest() {
		if err := gate(ctx); err != nil {
			f.gateErr = err
			return postgres.DirectOpenResult{}, err
		}
	}
	return postgres.DirectOpenResult{}, errStopAfterOpen
}

func (f *consentFake) ListRequestEnvelopesPage(_ context.Context, _ uuid.UUID, _ string, after *postgres.RequestPageCursor, limit int) ([]postgres.RequestEnvelope, error) {
	f.pageReads++
	var out []postgres.RequestEnvelope
	for _, e := range f.envelopes { // newest first, like the SQL
		if after != nil {
			if !(e.RequestAt.Before(after.At) || (e.RequestAt.Equal(after.At) && e.Conversation.ID.String() < after.ID.String())) {
				continue
			}
		}
		out = append(out, e)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *consentFake) ExpireDueMessageRequests(_ context.Context, limit int) ([]postgres.ExpiredRequest, error) {
	f.expiryCalls++
	n := f.expiryDue
	if n > limit {
		n = limit
	}
	f.expiryDue -= n
	out := make([]postgres.ExpiredRequest, n)
	for i := range out {
		out[i] = postgres.ExpiredRequest{ConversationID: uuid.New(), SenderID: uuid.New(), ReceiverID: uuid.New()}
	}
	return out, nil
}

func (f *consentFake) GetConversationReadCursors(context.Context, uuid.UUID) (map[uuid.UUID]postgres.ReadCursor, error) {
	return f.cursors, nil
}

// fakeLimiter is a scripted rate limiter.
type fakeLimiter struct {
	allowed bool
	err     error
	calls   int
}

func (l *fakeLimiter) Allow(context.Context, ratelimit.Limit, string) (bool, error) {
	l.calls++
	return l.allowed, l.err
}

// graphDecision serves one `message` decision from a fake graph-service.
func graphDecision(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/permissions/check" || r.URL.Query().Get("actions") != "message" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"decisions":{"message":` + body + `}}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func consentService(f *consentFake, graphURL string) *Service {
	s := &Service{convStore: f, log: newDiscardLogger(), httpClient: &http.Client{Timeout: 2 * time.Second}}
	s.graphServiceURL = graphURL
	return s
}

// --- 4. rate limit fails CLOSED ---

func TestRequestRateLimitFailsClosed(t *testing.T) {
	ctx := context.Background()
	user := uuid.New()
	cases := []struct {
		name string
		lim  *fakeLimiter
		want error
	}{
		{"limiter store down", &fakeLimiter{err: errors.New("redis: connection refused")}, ErrRateLimiterUnavailable},
		{"over quota", &fakeLimiter{allowed: false}, ErrRateLimited},
		{"within quota", &fakeLimiter{allowed: true}, nil},
	}
	for _, tc := range cases {
		s := &Service{log: newDiscardLogger(), rateLimiter: tc.lim}
		if err := s.checkRateLimitFailClosed(ctx, messageRequestRateLimit, user); !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	// The ordinary DM limit keeps failing open (unchanged behaviour).
	s := &Service{log: newDiscardLogger(), rateLimiter: &fakeLimiter{err: errors.New("down")}}
	if err := s.checkRateLimit(ctx, dmRateLimit, user); err != nil {
		t.Fatalf("dm limit must still fail open, got %v", err)
	}
}

func TestCreateRequestRefusedWhenLimiterDown(t *testing.T) {
	f := newConsentFake()
	actor, target := uuid.New(), uuid.New()
	f.freshPolicy(actor, false)
	f.openOutcome = postgres.RequestCreated
	s := consentService(f, graphDecision(t, `{"allowed":false,"fallback":"message_request"}`).URL)
	lim := &fakeLimiter{err: errors.New("redis down")}
	s.rateLimiter = lim
	_, err := s.CreateDirectConversation(context.Background(), actor, target, "k1")
	if !errors.Is(err, ErrRateLimiterUnavailable) {
		t.Fatalf("request with the limiter down: got %v, want ErrRateLimiterUnavailable", err)
	}
	if lim.calls != 1 || len(f.opens) != 1 || f.opens[0] != postgres.OpenAsRequest {
		t.Fatalf("limiter calls=%d opens=%v", lim.calls, f.opens)
	}
	// An existing conversation (no new request) never consults the limiter.
	f.openOutcome = postgres.DirectExisting
	lim.calls = 0
	if _, err := s.CreateDirectConversation(context.Background(), actor, target, "k2"); !errors.Is(err, errStopAfterOpen) {
		t.Fatalf("existing: %v", err)
	}
	if lim.calls != 0 {
		t.Fatal("the limiter was charged for a conversation that wrote no new request")
	}
}

// --- 2. decline cooldown on create ---

func TestDeclineCooldownWithinAndAfterSevenDays(t *testing.T) {
	ctx := context.Background()
	actor, target := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name   string
		status string
		age    time.Duration
		// want: "same" = the existing request handed back unchanged,
		// "unavailable" = graph's neutral block answer, "open" = a fresh
		// request is opened.
		want string
	}{
		{"deleted 1 hour ago", "ignored", time.Hour, "same"},
		{"deleted 6 days 23 hours ago", "ignored", 7*24*time.Hour - time.Hour, "same"},
		{"deleted 7 days 1 hour ago", "ignored", 7*24*time.Hour + time.Hour, "open"},
		{"blocked 2 days ago", "blocked", 48 * time.Hour, "unavailable"},
		{"reported 2 days ago", "reported", 48 * time.Hour, "unavailable"},
		{"blocked 8 days ago", "blocked", 8 * 24 * time.Hour, "open"},
		{"expired yesterday", "expired", 24 * time.Hour, "open"},
	} {
		f := newConsentFake()
		f.freshPolicy(actor, false)
		f.openOutcome = postgres.RequestReopened
		existing := uuid.New()
		f.conversations[existing] = &postgres.Conversation{ID: existing, Type: "direct", IsRequest: true}
		f.roles[existing] = map[uuid.UUID]string{actor: "member", target: "member"}
		responded := time.Now().Add(-tc.age)
		f.latestRequest = &postgres.MessageRequest{ConversationID: existing, SenderID: actor, ReceiverID: target, Status: tc.status, RespondedAt: &responded}
		s := consentService(f, graphDecision(t, `{"allowed":false,"fallback":"message_request"}`).URL)
		s.rateLimiter = &fakeLimiter{allowed: true}
		resp, err := s.CreateDirectConversation(ctx, actor, target, "key-"+tc.name)
		switch tc.want {
		case "same":
			// Nothing reveals the delete: success, the same conversation,
			// still a request, and no write that could put it back in the
			// recipient's Requests.
			if err != nil || resp == nil || resp.ID != existing || !resp.IsRequest || len(f.opens) != 0 {
				t.Errorf("%s: got %+v %v (opens %d), want the existing request unchanged", tc.name, resp, err, len(f.opens))
			}
		case "unavailable":
			var denied *MessagingNotAllowedError
			if !errors.As(err, &denied) || denied.Reason != ReasonUnavailable || len(f.opens) != 0 {
				t.Errorf("%s: got %v (opens %d), want the neutral 'unavailable' and no write", tc.name, err, len(f.opens))
			}
		default:
			if !errors.Is(err, errStopAfterOpen) || len(f.opens) != 1 || f.opens[0] != postgres.OpenAsRequest {
				t.Errorf("%s: got %v opens=%v, want a fresh request to be opened", tc.name, err, f.opens)
			}
		}
	}
}

// --- 6. allow -> a normal conversation; 7. deny reasons ---

func TestAllowOpensDirectAndDenyCarriesReason(t *testing.T) {
	ctx := context.Background()
	actor, target := uuid.New(), uuid.New()

	f := newConsentFake()
	f.freshPolicy(actor, false)
	s := consentService(f, graphDecision(t, `{"allowed":true}`).URL)
	if _, err := s.CreateDirectConversation(ctx, actor, target, "a1"); !errors.Is(err, errStopAfterOpen) {
		t.Fatalf("allow: %v", err)
	}
	if len(f.opens) != 1 || f.opens[0] != postgres.OpenDirect {
		t.Fatalf("recipient-follows-sender allow must open a DIRECT chat, opens=%v", f.opens)
	}

	for _, tc := range []struct{ graph, want string }{
		{"followers_only", ReasonFollowersOnly},
		{"people_i_follow", ReasonPeopleIFollow},
		{"messages_off", ReasonMessagesOff},
		{"no_one", ReasonNoOne},
		{"unavailable", ReasonUnavailable},
		{"blocked", ReasonUnavailable}, // a block is never revealed
		{"some_internal_rule", ""},     // unknown values never leak
		{"", ""},
	} {
		f := newConsentFake()
		f.freshPolicy(actor, false)
		s := consentService(f, graphDecision(t, fmt.Sprintf(`{"allowed":false,"reason":%q}`, tc.graph)).URL)
		_, err := s.CreateDirectConversation(ctx, actor, target, "d-"+tc.graph)
		if !errors.Is(err, ErrMessagingNotAllowed) {
			t.Fatalf("deny %q: got %v", tc.graph, err)
		}
		var denied *MessagingNotAllowedError
		got := ""
		if errors.As(err, &denied) {
			got = denied.Reason
		}
		if got != tc.want || len(f.opens) != 0 {
			t.Errorf("deny %q: reason %q (opens %d), want %q and no write", tc.graph, got, len(f.opens), tc.want)
		}
	}

	// The sender's own pause is "you_paused".
	f = newConsentFake()
	f.freshPolicy(actor, true)
	_, err := consentService(f, "").CreateDirectConversation(ctx, actor, target, "p")
	var denied *MessagingNotAllowedError
	if !errors.As(err, &denied) || denied.Reason != ReasonYouPaused {
		t.Fatalf("paused sender: %v", err)
	}
}

// --- 5. no read receipts before acceptance ---

func TestNoReadReceiptsOnARequestConversation(t *testing.T) {
	f := newConsentFake()
	sender, recipient := uuid.New(), uuid.New()
	convID := uuid.New()
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct", IsRequest: true}
	f.roles[convID] = map[uuid.UUID]string{sender: "member", recipient: "member"}
	f.freshPolicy(sender, false)
	f.freshPolicy(recipient, false) // read receipts: everyone
	f.cursors[recipient] = postgres.ReadCursor{UserID: recipient, LastReadAt: time.Now()}
	s := consentService(f, "")

	// The live frame: rdb is nil, so a publish attempt would panic — a
	// clean return proves the request gate fired first.
	if err := s.MarkRead(context.Background(), recipient, convID, uuid.NewString()); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	// The reload path: the sender's view of the recipient's cursor.
	resp := &ConversationResponse{ID: convID, Type: "direct", IsRequest: true,
		Members: []MemberWithProfile{{UserID: sender}, {UserID: recipient}}}
	s.attachReadCursors(context.Background(), sender, resp)
	if resp.Members[1].LastReadAt != nil {
		t.Fatal("a request conversation disclosed the recipient's read cursor to the requester")
	}
	// Control: once it is a normal chat the same cursor is disclosed.
	resp.IsRequest = false
	s.attachReadCursors(context.Background(), sender, resp)
	if resp.Members[1].LastReadAt == nil {
		t.Fatal("control: an accepted chat must disclose the cursor under 'everyone'")
	}
}

// --- 3. 30-day expiry sweep ---

func TestRequestExpiryPassDrainsInBatches(t *testing.T) {
	f := newConsentFake()
	f.expiryDue = 450
	s := consentService(f, "")
	n, err := s.runRequestExpiryPass(context.Background(), 200)
	if err != nil || n != 450 || f.expiryCalls != 3 {
		t.Fatalf("expired %d in %d calls (err %v), want 450 in 3", n, f.expiryCalls, err)
	}
	// A huge backlog is bounded per pass.
	f.expiryDue, f.expiryCalls = 100000, 0
	if n, _ := s.runRequestExpiryPass(context.Background(), 10); n != 10*requestExpiryMaxRounds {
		t.Fatalf("one pass expired %d, want the bound %d", n, 10*requestExpiryMaxRounds)
	}
}

// --- 6. paging / filtered / count ---

func seedEnvelopes(f *consentFake, viewer uuid.UUID, n int, filteredEvery int) {
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		preview := fmt.Sprintf("hello %d", i)
		if filteredEvery > 0 && i%filteredEvery == 0 {
			preview = fmt.Sprintf("spamword %d", i)
		}
		convID := uuid.New()
		creator := uuid.New()
		f.envelopes = append(f.envelopes, postgres.RequestEnvelope{
			Conversation: postgres.Conversation{ID: convID, Type: "direct", IsRequest: true, CreatedBy: &creator},
			SenderID:     creator, ReceiverID: viewer, Preview: preview, Status: "pending",
			RequestAt: base.Add(-time.Duration(i) * time.Minute), // newest first
		})
	}
}

func TestRequestsPagingFilteredAndCount(t *testing.T) {
	ctx := context.Background()
	viewer := uuid.New()
	f := newConsentFake()
	f.policies[viewer] = &postgres.UserPolicy{UserID: viewer, ReadReceiptsVisibility: "everyone", RefreshedAt: time.Now(),
		DMSafeMode: true, DMFilteredKeywords: []string{"spamword"}}
	seedEnvelopes(f, viewer, 450, 3) // 150 filtered, 300 not
	s := consentService(f, "")

	seen := map[uuid.UUID]bool{}
	cursor := ""
	pages := 0
	for {
		page, err := s.ListRequestsPage(ctx, viewer, RequestListQuery{Cursor: cursor, Limit: 40})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, r := range page.Rows {
			if r.Filtered || seen[r.ID] || r.Direction != "incoming" {
				t.Fatalf("page %d: bad row %+v (dup=%v)", pages, r, seen[r.ID])
			}
			seen[r.ID] = true
		}
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Fatal("last page carries a cursor")
			}
			break
		}
		if page.NextCursor == "" || len(page.Rows) != 40 {
			t.Fatalf("page %d: has_more with %d rows, cursor %q", pages, len(page.Rows), page.NextCursor)
		}
		cursor = page.NextCursor
	}
	if len(seen) != 300 || pages != 8 {
		t.Fatalf("paged %d rows over %d pages, want 300 over 8", len(seen), pages)
	}

	hidden, err := s.ListRequestsPage(ctx, viewer, RequestListQuery{Filter: "filtered", Limit: 100})
	if err != nil || len(hidden.Rows) != 100 || !hidden.HasMore || !hidden.Rows[0].Filtered {
		t.Fatalf("hidden page: %d rows has_more=%v err=%v", len(hidden.Rows), hidden.HasMore, err)
	}
	if hidden.Rows[0].Preview == "" || hidden.Rows[0].RequestedAt.IsZero() {
		t.Fatal("rows must carry preview and requested_at")
	}

	// Legacy offset paging still works and agrees with the cursor order.
	byOffset, err := s.ListRequestsPage(ctx, viewer, RequestListQuery{Offset: 40, Limit: 40})
	if err != nil {
		t.Fatal(err)
	}
	second, _ := s.ListRequestsPage(ctx, viewer, RequestListQuery{Limit: 40})
	next, _ := s.ListRequestsPage(ctx, viewer, RequestListQuery{Cursor: second.NextCursor, Limit: 40})
	if byOffset.Rows[0].ID != next.Rows[0].ID {
		t.Fatal("offset=40 and the second cursor page disagree")
	}

	counts, err := s.CountRequests(ctx, viewer)
	if err != nil || counts.Count != 300 || counts.FilteredCount != 150 || counts.Capped {
		t.Fatalf("counts = %+v err=%v, want 300/150", counts, err)
	}
	f.envelopes = nil
	seedEnvelopes(f, viewer, requestCountCap+5, 0)
	if counts, _ := s.CountRequests(ctx, viewer); counts.Count != requestCountCap || !counts.Capped {
		t.Fatalf("capped counts = %+v", counts)
	}

	if _, err := s.ListRequestsPage(ctx, viewer, RequestListQuery{Cursor: "not-a-cursor"}); !errors.Is(err, ErrInvalidMessageContent) {
		t.Fatalf("bad cursor: %v", err)
	}
	if _, err := s.ListRequestsPage(ctx, viewer, RequestListQuery{Direction: "sideways"}); !errors.Is(err, ErrInvalidMessageContent) {
		t.Fatalf("bad direction: %v", err)
	}
}

// A page whose matches are sparse stops at the scan budget with a cursor
// at the scan position, so paging never loses or repeats a row.
func TestRequestsPagingStopsAtScanBudget(t *testing.T) {
	ctx := context.Background()
	viewer := uuid.New()
	f := newConsentFake()
	f.policies[viewer] = &postgres.UserPolicy{UserID: viewer, RefreshedAt: time.Now(), DMFilteredKeywords: []string{"spamword"}}
	seedEnvelopes(f, viewer, requestPageScanBudget+300, 1) // every row filtered
	// Two unfiltered rows at the very end.
	for i := 0; i < 2; i++ {
		last := &f.envelopes[len(f.envelopes)-1-i]
		last.Preview = "fine"
	}
	s := consentService(f, "")
	first, err := s.ListRequestsPage(ctx, viewer, RequestListQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Rows) != 0 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("budget page: %d rows has_more=%v cursor=%q", len(first.Rows), first.HasMore, first.NextCursor)
	}
	second, err := s.ListRequestsPage(ctx, viewer, RequestListQuery{Limit: 20, Cursor: first.NextCursor})
	if err != nil || len(second.Rows) != 2 || second.HasMore {
		t.Fatalf("after the budget: %d rows has_more=%v err=%v", len(second.Rows), second.HasMore, err)
	}
}

func newDiscardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
