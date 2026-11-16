package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Platform suspension of a community (migration 005), behind admin-service's
// token family (permission chat:communities.moderate). The status column is
// the switch every read path already obeys; suspended_at, suspended_by and
// suspension_reason_code say who, when and why. Each call appends one
// community_admin_audit row in the same transaction as the change, so the
// change and its trail commit or roll back together. The actor is always the
// signed act claim the HTTP layer hands down.
//
// Both directions are idempotent: suspending a suspended community, or
// unsuspending an active one, changes nothing (changed=false) but is still
// audited. Archived and deleted communities are never touched: unsuspending
// must not resurrect an archived community.

// Community lifecycle statuses this file moves between.
const (
	CommunityStatusActive    = "active"
	CommunityStatusSuspended = "suspended"
)

// Audit actions.
const (
	AuditCommunitySuspended   = "community.suspended"
	AuditCommunityUnsuspended = "community.unsuspended"
)

// CommunitySuspensionChange is the outcome of a suspend or unsuspend.
type CommunitySuspensionChange struct {
	CommunityID    uuid.UUID `json:"community_id"`
	PreviousStatus string    `json:"previous_status"`
	Status         string    `json:"status"`
	// Changed is false when the community was already in the asked state.
	Changed     bool       `json:"changed"`
	SuspendedAt *time.Time `json:"suspended_at,omitempty"`
	SuspendedBy *uuid.UUID `json:"suspended_by,omitempty"`
	ReasonCode  string     `json:"reason_code,omitempty"`
	// Community is the row as committed, for community.updated. Never
	// serialised in the admin response.
	Community *Community `json:"-"`
}

// CommunitySuspension is what a member of a suspended community is shown on
// its card. It does not name the admin who suspended it.
type CommunitySuspension struct {
	SuspendedAt *time.Time `json:"suspended_at,omitempty"`
	ReasonCode  string     `json:"reason_code,omitempty"`
	// ReadOnly: members may read; nothing may be written until it is lifted.
	ReadOnly bool `json:"read_only"`
}

// SuspensionTransition is the whole state machine. Pure.
//
//	suspend   active    -> suspended (changed)
//	suspend   suspended -> suspended (no change)
//	unsuspend suspended -> active    (changed)
//	unsuspend active    -> active    (no change)
//	anything else (archived, deleted) -> ErrAdminConflict
func SuspensionTransition(current string, suspend bool) (to string, changed bool, err error) {
	switch {
	case suspend && current == CommunityStatusActive:
		return CommunityStatusSuspended, true, nil
	case suspend && current == CommunityStatusSuspended:
		return CommunityStatusSuspended, false, nil
	case !suspend && current == CommunityStatusSuspended:
		return CommunityStatusActive, true, nil
	case !suspend && current == CommunityStatusActive:
		return CommunityStatusActive, false, nil
	}
	return current, false, fmt.Errorf("community is %s: %w", current, ErrAdminConflict)
}

// suspensionAuditAction names the audit row for a call. Pure.
func suspensionAuditAction(suspend bool) string {
	if suspend {
		return AuditCommunitySuspended
	}
	return AuditCommunityUnsuspended
}

const (
	suspendCommunitySQL = `UPDATE communities SET status = 'suspended', suspended_at = NOW(), suspended_by = $2,
		suspension_reason_code = $3,
		updated_at = GREATEST(clock_timestamp(), communities.updated_at + interval '1 microsecond')
		WHERE id = $1 AND status = 'active'`
	unsuspendCommunitySQL = `UPDATE communities SET status = 'active', suspended_at = NULL, suspended_by = NULL,
		suspension_reason_code = NULL,
		updated_at = GREATEST(clock_timestamp(), communities.updated_at + interval '1 microsecond')
		WHERE id = $1 AND status = 'suspended'`
)

// AdminSetCommunitySuspended suspends (suspend=true) or unsuspends a
// community and audits the call. reasonCode is required to suspend and
// ignored to unsuspend; note is the admin's free text, stored on the audit
// row. The service validates both before calling.
func (s *Store) AdminSetCommunitySuspended(ctx context.Context, actor, communityID uuid.UUID, suspend bool, reasonCode, note string) (*CommunitySuspensionChange, error) {
	if actor == uuid.Nil {
		return nil, ErrAdminActorRequired
	}
	if suspend && reasonCode == "" {
		return nil, fmt.Errorf("invalid: reason_code is required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	var inForce *string
	if err := tx.QueryRow(ctx, `SELECT status, suspension_reason_code FROM communities
		WHERE id = $1 AND status != 'deleted' FOR UPDATE`, communityID).Scan(&current, &inForce); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAdminNotFound
		}
		return nil, err
	}
	to, changed, err := SuspensionTransition(current, suspend)
	if err != nil {
		return nil, err
	}
	if changed {
		var tag interface{ RowsAffected() int64 }
		if suspend {
			tag, err = tx.Exec(ctx, suspendCommunitySQL, communityID, actor, reasonCode)
		} else {
			tag, err = tx.Exec(ctx, unsuspendCommunitySQL, communityID)
		}
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, fmt.Errorf("community status moved under the lock: %w", ErrAdminConflict)
		}
	}

	metadata := map[string]any{"previous_status": current, "status": to, "changed": changed}
	if suspend {
		metadata["reason_code"] = reasonCode
		if !changed && inForce != nil {
			// A repeat keeps the code already in force; record both.
			metadata["reason_code_in_force"] = *inForce
		}
	} else if inForce != nil {
		metadata["lifted_reason_code"] = *inForce
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO community_admin_audit (actor_id, action, target_type, target_id, reason, metadata)
		VALUES ($1, $2, 'community', $3, $4, $5)`,
		actor, suspensionAuditAction(suspend), communityID, note, metadata); err != nil {
		return nil, fmt.Errorf("community admin audit %s: %w", suspensionAuditAction(suspend), err)
	}

	c, susAt, susBy, susCode, err := readCommunityForSuspension(ctx, tx, communityID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out := &CommunitySuspensionChange{
		CommunityID:    communityID,
		PreviousStatus: current,
		Status:         c.Status,
		Changed:        changed,
		SuspendedAt:    susAt,
		SuspendedBy:    susBy,
		Community:      c,
	}
	if susCode != nil {
		out.ReasonCode = *susCode
	}
	return out, nil
}

// readCommunityForSuspension reads the row as committed in tx, with the
// suspension columns, for the change's community.updated snapshot.
func readCommunityForSuspension(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Community, *time.Time, *uuid.UUID, *string, error) {
	c := &Community{}
	var susAt *time.Time
	var susBy *uuid.UUID
	var susCode *string
	err := tx.QueryRow(ctx, `SELECT id, owner_id, handle, name, description, avatar_media_id, banner_media_id,
		community_type, category, language, join_mode, email_domain_gate,
		join_questions, member_directory, cross_space_bans, max_sub_spaces,
		latitude, longitude, location_name, rules, topic_tags,
		member_count, space_count, is_verified, status, created_at, updated_at, deleted_at,
		suspended_at, suspended_by, suspension_reason_code
		FROM communities WHERE id = $1`, id).Scan(
		&c.ID, &c.OwnerID, &c.Handle, &c.Name, &c.Description, &c.AvatarMediaID, &c.BannerMediaID,
		&c.CommunityType, &c.Category, &c.Language, &c.JoinMode, &c.EmailDomainGate,
		&c.JoinQuestions, &c.MemberDirectory, &c.CrossSpaceBans, &c.MaxSubSpaces,
		&c.Latitude, &c.Longitude, &c.LocationName, &c.Rules, &c.TopicTags,
		&c.MemberCount, &c.SpaceCount, &c.IsVerified, &c.Status, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt,
		&susAt, &susBy, &susCode,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil, nil, ErrAdminNotFound
	}
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return c, susAt, susBy, susCode, nil
}

// GetCommunitySuspension reads the suspension shown on a suspended
// community's card. A community that is not suspended has none (nil).
func (s *Store) GetCommunitySuspension(ctx context.Context, communityID uuid.UUID) (*CommunitySuspension, error) {
	var status string
	var at *time.Time
	var code *string
	err := s.db.QueryRow(ctx, `SELECT status, suspended_at, suspension_reason_code FROM communities WHERE id = $1`,
		communityID).Scan(&status, &at, &code)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCommunityNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != CommunityStatusSuspended {
		return nil, nil
	}
	out := &CommunitySuspension{SuspendedAt: at, ReadOnly: true}
	if code != nil {
		out.ReasonCode = *code
	}
	return out, nil
}
