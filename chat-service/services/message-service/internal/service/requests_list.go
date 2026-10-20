package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// --- Requests list (contract §5): paged, incoming by default, Hidden list,
// badge count ---

// requestScanCap is how many request rows one store read returns; a page is
// cut in memory after the safe-mode filter, so a page may take several reads.
const requestScanCap = 200

// requestPageScanBudget bounds the rows one page request may evaluate. When
// it runs out the page is returned short with a next_cursor at the scan
// position, so paging still reaches every row.
const requestPageScanBudget = 2000

// requestCountCap bounds the badge count scan; past it the count is
// reported with capped=true (clients show "999+").
const requestCountCap = 1000

// requestPager is the Postgres store's request-list surface.
type requestPager interface {
	ListRequestEnvelopesPage(ctx context.Context, userID uuid.UUID, direction string, after *postgres.RequestPageCursor, limit int) ([]postgres.RequestEnvelope, error)
}

// RequestListQuery is GET /v1/chat/requests.
type RequestListQuery struct {
	// Filter is "all" (default: requests safe mode did NOT filter) or
	// "filtered" (the Hidden requests sub-list).
	Filter string
	// Direction is "incoming" (default: requests waiting for the caller),
	// "outgoing" or "all".
	Direction string
	// Cursor is the opaque next_cursor of the previous page.
	Cursor string
	Limit  int
	// Offset is the legacy paging; ignored when Cursor is set.
	Offset int
}

// RequestPage is one page of the requests list.
type RequestPage struct {
	Rows       []RequestRow
	NextCursor string
	HasMore    bool
}

// RequestCounts is GET /v1/chat/requests/count: the Requests badge. Count is
// the caller's incoming live requests that are NOT hidden; FilteredCount the
// Hidden ones. Capped means the scan stopped at requestCountCap rows.
type RequestCounts struct {
	Count         int  `json:"count"`
	FilteredCount int  `json:"filtered_count"`
	Capped        bool `json:"capped"`
}

func encodeRequestCursor(c postgres.RequestPageCursor) string {
	raw := strconv.FormatInt(c.At.UnixNano(), 10) + "|" + c.ID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeRequestCursor(s string) (*postgres.RequestPageCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid cursor", ErrInvalidMessageContent)
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return nil, fmt.Errorf("%w: invalid cursor", ErrInvalidMessageContent)
	}
	nanos, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid cursor", ErrInvalidMessageContent)
	}
	convID, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid cursor", ErrInvalidMessageContent)
	}
	return &postgres.RequestPageCursor{At: time.Unix(0, nanos).UTC(), ID: convID}, nil
}

func normalizeRequestQuery(q RequestListQuery) (RequestListQuery, error) {
	if q.Filter == "" {
		q.Filter = "all"
	}
	if q.Direction == "" {
		q.Direction = "incoming"
	}
	if q.Direction != "all" && q.Direction != "incoming" && q.Direction != "outgoing" {
		return q, fmt.Errorf("%w: direction must be incoming, outgoing or all", ErrInvalidMessageContent)
	}
	if q.Filter != "all" && q.Filter != "filtered" {
		return q, fmt.Errorf("%w: filter must be all or filtered", ErrInvalidMessageContent)
	}
	if q.Limit <= 0 {
		q.Limit = 20
	}
	if q.Limit > 100 {
		q.Limit = 100
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	return q, nil
}

// ListRequestsFiltered is GET /v1/chat/requests?filter=all|filtered over
// both directions (the pre-paging shape; kept for older callers).
func (s *Service) ListRequestsFiltered(ctx context.Context, userID uuid.UUID, filter string, limit, offset int) ([]RequestRow, error) {
	return s.ListRequestsDirected(ctx, userID, filter, "all", limit, offset)
}

// ListRequestsDirected is the offset form of ListRequestsPage. An empty
// direction keeps its original meaning, "all".
func (s *Service) ListRequestsDirected(ctx context.Context, userID uuid.UUID, filter, direction string, limit, offset int) ([]RequestRow, error) {
	if direction == "" {
		direction = "all"
	}
	page, err := s.ListRequestsPage(ctx, userID, RequestListQuery{Filter: filter, Direction: direction, Limit: limit, Offset: offset})
	if err != nil {
		return nil, err
	}
	return page.Rows, nil
}

// ListRequestsPage is GET /v1/chat/requests: newest request first, cursor
// paged. Only live requests are listed (pending, not past the 30-day
// expiry, not deleted); "filtered" is the Hidden sub-list (keyword match,
// or under safe mode a link or a sender account under 7 days old).
// Requests the caller sent are never filtered.
func (s *Service) ListRequestsPage(ctx context.Context, userID uuid.UUID, q RequestListQuery) (*RequestPage, error) {
	q, err := normalizeRequestQuery(q)
	if err != nil {
		return nil, err
	}
	var after *postgres.RequestPageCursor
	if q.Cursor != "" {
		if after, err = decodeRequestCursor(q.Cursor); err != nil {
			return nil, err
		}
	}
	pager, ok := s.convStore.(requestPager)
	if !ok {
		return s.listRequestsLegacy(ctx, userID, q)
	}
	wantFiltered := q.Filter == "filtered"
	skip := 0
	if after == nil {
		skip = q.Offset
	}
	policy := s.GetChatPolicy(ctx, userID)
	now := s.clock()
	page := &RequestPage{Rows: []RequestRow{}}
	senderOf := map[uuid.UUID]uuid.UUID{}
	scanned := 0
	exhausted := false
scan:
	for scanned < requestPageScanBudget {
		envs, err := pager.ListRequestEnvelopesPage(ctx, userID, q.Direction, after, requestScanCap)
		if err != nil {
			return nil, err
		}
		ages := s.requestSenderAges(ctx, userID, envs)
		for _, e := range envs {
			scanned++
			pos := &postgres.RequestPageCursor{At: e.RequestAt, ID: e.Conversation.ID}
			filtered := e.ReceiverID == userID && requestFiltered(e.Preview, policy, ages[e.SenderID].AccountCreatedAt, now)
			if filtered != wantFiltered {
				after = pos
				continue
			}
			if skip > 0 {
				skip--
				after = pos
				continue
			}
			if len(page.Rows) == q.Limit {
				// One more match exists: the next page starts after the
				// last row consumed (every row in between was skipped).
				page.HasMore = true
				break scan
			}
			dir := requestDirection(e.ReceiverID, e.SenderID, e.Conversation.CreatedBy, userID)
			page.Rows = append(page.Rows, RequestRow{Conversation: e.Conversation, Filtered: filtered, Direction: dir,
				Preview: e.Preview, RequestedAt: e.RequestAt.UTC()})
			// A purged sender's request row is gone: the creator is the sender.
			sender := e.SenderID
			if sender == uuid.Nil && e.Conversation.CreatedBy != nil {
				sender = *e.Conversation.CreatedBy
			}
			senderOf[e.Conversation.ID] = sender
			after = pos
		}
		if len(envs) < requestScanCap {
			exhausted = true
			break
		}
	}
	if !page.HasMore && !exhausted {
		// Scan budget spent: more rows may match beyond the scan position.
		page.HasMore = true
	}
	if page.HasMore && after != nil {
		page.NextCursor = encodeRequestCursor(*after)
	}
	blocks := s.requestSenders(ctx, userID, senderOf)
	for i := range page.Rows {
		page.Rows[i].Sender = blocks[page.Rows[i].ID]
	}
	return page, nil
}

// requestSenderAges looks up the account ages safe mode needs: only senders
// of requests the viewer received.
func (s *Service) requestSenderAges(ctx context.Context, viewerID uuid.UUID, envs []postgres.RequestEnvelope) map[uuid.UUID]postgres.ProfileExtras {
	var senders []uuid.UUID
	for _, e := range envs {
		if e.ReceiverID == viewerID && e.SenderID != uuid.Nil {
			senders = append(senders, e.SenderID)
		}
	}
	return s.profileExtras(ctx, senders)
}

// CountRequests is the Requests badge: live incoming requests, split into
// the main list and the Hidden (filtered) list.
func (s *Service) CountRequests(ctx context.Context, userID uuid.UUID) (*RequestCounts, error) {
	out := &RequestCounts{}
	pager, ok := s.convStore.(requestPager)
	if !ok {
		rows, err := s.listRequestsLegacy(ctx, userID, RequestListQuery{Filter: "all", Direction: "incoming", Limit: 100})
		if err != nil {
			return nil, err
		}
		out.Count = len(rows.Rows)
		return out, nil
	}
	policy := s.GetChatPolicy(ctx, userID)
	now := s.clock()
	var after *postgres.RequestPageCursor
	scanned := 0
	for {
		envs, err := pager.ListRequestEnvelopesPage(ctx, userID, "incoming", after, requestScanCap)
		if err != nil {
			return nil, err
		}
		ages := s.requestSenderAges(ctx, userID, envs)
		for _, e := range envs {
			if scanned == requestCountCap {
				out.Capped = true
				return out, nil
			}
			scanned++
			if requestFiltered(e.Preview, policy, ages[e.SenderID].AccountCreatedAt, now) {
				out.FilteredCount++
			} else {
				out.Count++
			}
			after = &postgres.RequestPageCursor{At: e.RequestAt, ID: e.Conversation.ID}
		}
		if len(envs) < requestScanCap {
			return out, nil
		}
	}
}

// listRequestsLegacy serves stores without the paged query (test fakes):
// offset paging over the legacy list, nothing filtered, no cursor.
func (s *Service) listRequestsLegacy(ctx context.Context, userID uuid.UUID, q RequestListQuery) (*RequestPage, error) {
	page := &RequestPage{Rows: []RequestRow{}}
	if q.Filter == "filtered" {
		return page, nil
	}
	convs, err := s.ListRequests(ctx, userID, q.Limit, q.Offset)
	if err != nil {
		return nil, err
	}
	for _, c := range convs {
		dir := requestDirection(uuid.Nil, uuid.Nil, c.CreatedBy, userID)
		if q.Direction != "all" && dir != q.Direction {
			continue
		}
		page.Rows = append(page.Rows, RequestRow{Conversation: c, Direction: dir, RequestedAt: c.CreatedAt})
	}
	return page, nil
}
