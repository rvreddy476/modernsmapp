package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AuditTrailFilter selects rows of admin.audit_log for the console's audit
// page. Empty fields do not filter.
type AuditTrailFilter struct {
	// Apps restricts the result to these apps. nil means every app (a holder
	// of *:audit.read); an empty non-nil slice matches nothing.
	Apps      []string
	App       string
	Actor     string
	Operation string
	Outcome   string
	From      *time.Time // created_at >= From
	To        *time.Time // created_at <  To
	Cursor    string     // next_cursor of the previous page
	Limit     int
}

// AuditTrailEntry is one row as the console sees it: ids only. The free-text
// reason and the payload are not returned (they can carry personal data).
type AuditTrailEntry struct {
	ID         string    `json:"id"`
	App        string    `json:"app"`
	Operation  string    `json:"operation"`
	Actor      string    `json:"actor"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Outcome    *string   `json:"outcome"`
	StatusCode *int      `json:"status_code"`
	RequestID  *string   `json:"request_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// AuditTrailPage is one page, newest first. NextCursor is empty on the last page.
type AuditTrailPage struct {
	Items      []AuditTrailEntry `json:"items"`
	NextCursor string            `json:"next_cursor"`
}

// ErrInvalidAuditCursor: the cursor was not produced by this service.
var ErrInvalidAuditCursor = errors.New("invalid audit cursor")

const (
	auditTrailDefaultLimit = 50
	auditTrailMaxLimit     = 200
)

func encodeAuditCursor(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(t.UnixMicro(), 10) + "|" + id))
}

func decodeAuditCursor(s string) (time.Time, uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidAuditCursor
	}
	micro, idStr, ok := strings.Cut(string(b), "|")
	if !ok {
		return time.Time{}, uuid.Nil, ErrInvalidAuditCursor
	}
	n, err := strconv.ParseInt(micro, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidAuditCursor
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidAuditCursor
	}
	return time.UnixMicro(n).UTC(), id, nil
}

// ListAuditTrail returns one page of admin.audit_log, newest first, ordered by
// (created_at, id) so a page boundary never skips or repeats a row.
func (s *Store) ListAuditTrail(ctx context.Context, f AuditTrailFilter) (AuditTrailPage, error) {
	if f.Apps != nil && len(f.Apps) == 0 {
		return AuditTrailPage{Items: []AuditTrailEntry{}}, nil
	}
	limit := f.Limit
	if limit <= 0 {
		limit = auditTrailDefaultLimit
	}
	if limit > auditTrailMaxLimit {
		limit = auditTrailMaxLimit
	}

	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.Apps != nil {
		where = append(where, "app = ANY("+arg(f.Apps)+")")
	}
	if f.App != "" {
		where = append(where, "app = "+arg(f.App))
	}
	if f.Actor != "" {
		where = append(where, "admin_actor = "+arg(f.Actor))
	}
	if f.Operation != "" {
		where = append(where, "action = "+arg(f.Operation))
	}
	if f.Outcome != "" {
		where = append(where, "outcome = "+arg(f.Outcome))
	}
	if f.From != nil {
		where = append(where, "created_at >= "+arg(*f.From))
	}
	if f.To != nil {
		where = append(where, "created_at < "+arg(*f.To))
	}
	if f.Cursor != "" {
		t, id, err := decodeAuditCursor(f.Cursor)
		if err != nil {
			return AuditTrailPage{}, err
		}
		where = append(where, "(created_at, id) < ("+arg(t)+", "+arg(id)+")")
	}
	sql := `SELECT id, COALESCE(app, ''), action, admin_actor, entity_type, entity_id, outcome, status_code, request_id, created_at
		FROM admin.audit_log`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += " ORDER BY created_at DESC, id DESC LIMIT " + arg(limit+1)

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return AuditTrailPage{}, err
	}
	defer rows.Close()
	page := AuditTrailPage{Items: []AuditTrailEntry{}}
	for rows.Next() {
		var e AuditTrailEntry
		var id uuid.UUID
		if err := rows.Scan(&id, &e.App, &e.Operation, &e.Actor, &e.TargetType, &e.TargetID,
			&e.Outcome, &e.StatusCode, &e.RequestID, &e.CreatedAt); err != nil {
			return AuditTrailPage{}, err
		}
		e.ID = id.String()
		page.Items = append(page.Items, e)
	}
	if err := rows.Err(); err != nil {
		return AuditTrailPage{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeAuditCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}
