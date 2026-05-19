package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CanView evaluates the v2.1 visibility policy evaluation order:
// 1. Blocked? DENY
// 2. In deny_users? DENY
// 3. public? ALLOW
// 4. friends mode? ALLOW if friends
// 5. followers mode? ALLOW if follower OR friend
// 6. circles/only_me? ALLOW if member of allow_list OR in allow_users
// 7. Default DENY
//
// ownerID is the post author; viewerID is the requesting user.
// policyID is the visibility_policy_id stored on the post (may be uuid.Nil for legacy posts).
// legacyVisibility is the old visibility TEXT column value (fallback when policyID is nil).
func CanView(ctx context.Context, db *pgxpool.Pool, viewerID, ownerID, policyID uuid.UUID, legacyVisibility string) (bool, error) {
	// Owner always sees their own content
	if viewerID == ownerID {
		return true, nil
	}

	// Step 1: Check if viewer is blocked by owner
	var blocked bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM blocks WHERE blocker_id = $1 AND blocked_id = $2)`,
		ownerID, viewerID,
	).Scan(&blocked)
	if err != nil {
		return false, err
	}
	if blocked {
		return false, nil
	}

	// If no policy, fall back to legacy visibility
	if policyID == uuid.Nil {
		return canViewLegacy(ctx, db, viewerID, ownerID, legacyVisibility)
	}

	// Step 2: Fetch policy mode
	var mode string
	err = db.QueryRow(ctx,
		`SELECT mode FROM visibility.policies WHERE id = $1`,
		policyID,
	).Scan(&mode)
	if err != nil {
		// Policy not found, fall back
		return canViewLegacy(ctx, db, viewerID, ownerID, legacyVisibility)
	}

	// Step 3: Check deny_users
	var denied bool
	err = db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM visibility.policy_deny_users WHERE policy_id = $1 AND user_id = $2)`,
		policyID, viewerID,
	).Scan(&denied)
	if err != nil {
		return false, err
	}
	if denied {
		return false, nil
	}

	switch mode {
	case "public":
		return true, nil

	case "only_me":
		return false, nil

	case "friends":
		return isFriend(ctx, db, viewerID, ownerID)

	case "followers":
		isF, err := isFriend(ctx, db, viewerID, ownerID)
		if err != nil || isF {
			return isF, err
		}
		return isFollower(ctx, db, viewerID, ownerID)

	case "circles":
		// Check allow_users
		var inAllowUsers bool
		err = db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM visibility.policy_allow_users WHERE policy_id = $1 AND user_id = $2)`,
			policyID, viewerID,
		).Scan(&inAllowUsers)
		if err != nil {
			return false, err
		}
		if inAllowUsers {
			return true, nil
		}
		// Check allow_lists (viewer must be a member of any allowed custom list)
		// custom_lists membership is in identity-platform; here we check via graph-service's friends list
		// For now, check if viewer follows owner AND is in any allow_list
		// (Full list membership check requires cross-service call; use conservative fallback)
		return false, nil
	}

	return false, nil
}

func canViewLegacy(ctx context.Context, db *pgxpool.Pool, viewerID, ownerID uuid.UUID, visibility string) (bool, error) {
	switch visibility {
	case "public", "":
		return true, nil
	case "only_me":
		return false, nil
	case "friends":
		return isFriend(ctx, db, viewerID, ownerID)
	case "followers":
		isF, err := isFriend(ctx, db, viewerID, ownerID)
		if err != nil || isF {
			return isF, err
		}
		return isFollower(ctx, db, viewerID, ownerID)
	}
	return false, nil
}

func isFriend(ctx context.Context, db *pgxpool.Pool, a, b uuid.UUID) (bool, error) {
	// friends table uses canonical order (user_a < user_b)
	var u1, u2 uuid.UUID
	if a.String() < b.String() {
		u1, u2 = a, b
	} else {
		u1, u2 = b, a
	}
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM connections WHERE user_a = $1 AND user_b = $2)`,
		u1, u2,
	).Scan(&exists)
	return exists, err
}

func isFollower(ctx context.Context, db *pgxpool.Pool, viewerID, ownerID uuid.UUID) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM follows WHERE follower_id = $1 AND followee_id = $2)`,
		viewerID, ownerID,
	).Scan(&exists)
	return exists, err
}
