package events

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Who is paged for a dating panic (Dating plan lane D8).
//
// identity-auth has no "list the users holding role X" route: its internal
// API answers the roles of ONE user id (GET /v1/auth/internal/roles/:id). So
// responders are an operator-configured list of staff user ids
// (DATING_SAFETY_RESPONDER_USER_IDS), and each one is verified against
// identity to still hold admin, moderator or superadmin before it is paged.
// A lookup failure still pages the configured id (an operator put it there;
// paging an unverified operator beats paging nobody); a definite "no staff
// role" answer drops it and logs at ERROR.

// responderRoleNames are the roles allowed to receive dating panic pages.
var responderRoleNames = map[string]bool{"superadmin": true, "admin": true, "moderator": true}

// roleLookup is identityroles.Client.Roles.
type roleLookup interface {
	Roles(ctx context.Context, userID string) ([]string, error)
}

type responderCheck struct {
	staff bool
	at    time.Time
}

// ResponderDirectory resolves the pageable responders.
type ResponderDirectory struct {
	ids   []uuid.UUID
	roles roleLookup
	ttl   time.Duration
	now   func() time.Time

	mu    sync.Mutex
	cache map[uuid.UUID]responderCheck
}

// ParseResponderIDs splits a comma/space separated list of user ids,
// returning the valid ids (deduplicated, in order) and the invalid entries.
func ParseResponderIDs(raw string) ([]uuid.UUID, []string) {
	var ids []uuid.UUID
	var invalid []string
	seen := map[uuid.UUID]bool{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		id, err := uuid.Parse(strings.TrimSpace(part))
		if err != nil || id == uuid.Nil {
			invalid = append(invalid, part)
			continue
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, invalid
}

// NewResponderDirectory builds the directory. roles may be nil (identity not
// configured): every configured id is paged unverified.
func NewResponderDirectory(ids []uuid.UUID, roles roleLookup) *ResponderDirectory {
	return &ResponderDirectory{
		ids:   append([]uuid.UUID(nil), ids...),
		roles: roles,
		ttl:   5 * time.Minute,
		now:   time.Now,
		cache: map[uuid.UUID]responderCheck{},
	}
}

// Configured returns how many responder ids are configured.
func (d *ResponderDirectory) Configured() int {
	if d == nil {
		return 0
	}
	return len(d.ids)
}

// Responders returns the configured ids that are (still) staff.
func (d *ResponderDirectory) Responders(ctx context.Context) []uuid.UUID {
	if d == nil || len(d.ids) == 0 {
		return nil
	}
	if d.roles == nil {
		return append([]uuid.UUID(nil), d.ids...)
	}
	out := make([]uuid.UUID, 0, len(d.ids))
	for _, id := range d.ids {
		d.mu.Lock()
		check, ok := d.cache[id]
		d.mu.Unlock()
		if !ok || d.now().Sub(check.at) > d.ttl {
			roles, err := d.roles.Roles(ctx, id.String())
			if err != nil {
				slog.Warn("dating responder role check failed; paging the configured id unverified", "responder_id", id, "error", err)
				out = append(out, id)
				continue
			}
			check = responderCheck{at: d.now()}
			for _, r := range roles {
				if responderRoleNames[strings.ToLower(strings.TrimSpace(r))] {
					check.staff = true
					break
				}
			}
			d.mu.Lock()
			d.cache[id] = check
			d.mu.Unlock()
		}
		if !check.staff {
			slog.Error("configured dating responder holds no admin/moderator role; not paged", "responder_id", id)
			continue
		}
		out = append(out, id)
	}
	return out
}
