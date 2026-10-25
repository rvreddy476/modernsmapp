package store

// The store side of the media claim protocol (service/media_claims.go) and of
// its reconcile tool (internal/claimsync, cmd/media-claims).
//
// Four columns here hold a media-service asset with no foreign key, each a
// claim kind media-service registers for "dating-service":
//
//	dating_photo         dating_photos.media_id                ref = photo id
//	prompt_clip          dating_prompts.clip_media_id          ref = "<user id>:<prompt id>"
//	selfie_attempt       dating_selfie_attempts.video_media_id ref = attempt id
//	verification_selfie  dating_verifications.selfie_media_id  ref = user id
//
// A prompt clip is held by the (user, prompt) slot, not the row id: the row
// is deleted and re-created as an answer is cleared and written again, and
// every writer addresses it by (user, prompt).

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/atpost/dating-service/internal/mediaclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// optionalID is NULL for uuid.Nil, so the column default applies.
func optionalID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// PromptClipRef is the claim ref of a prompt's clip slot.
func PromptClipRef(userID uuid.UUID, promptID int) string {
	return userID.String() + ":" + strconv.Itoa(promptID)
}

// ParsePromptClipRef is PromptClipRef's inverse.
func ParsePromptClipRef(ref string) (uuid.UUID, int, error) {
	u, p, ok := strings.Cut(ref, ":")
	if !ok {
		return uuid.Nil, 0, fmt.Errorf("prompt clip ref %q: want <user>:<prompt>", ref)
	}
	userID, err := uuid.Parse(u)
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("prompt clip ref %q: %w", ref, err)
	}
	promptID, err := strconv.Atoi(p)
	if err != nil {
		return uuid.Nil, 0, fmt.Errorf("prompt clip ref %q: %w", ref, err)
	}
	return userID, promptID, nil
}

// LockMediaHolder serialises every write that changes which media one holder
// (kind, ref) names, for as long as the caller holds it. The claim protocol
// claims, writes and releases in three steps; without this, two writes of the
// same holder that each read it first can interleave so that one's release
// removes a claim the other's write relies on. Transaction-scoped, so a crash
// or a lost connection frees it. unlock is idempotent.
func (s *Store) LockMediaHolder(ctx context.Context, kind, ref string) (unlock func(), err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	if _, err := tx.Exec(ctx, lockMediaHolderSQL, mediaHolderLockKey(kind, ref)); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	done := false
	return func() {
		if !done {
			done = true
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}, nil
}

// lockMediaHolderSQL waits (never try_) for an exclusive, transaction-scoped
// lock. Pinned by TestMediaHolderLockWaitsAndIsTransactionScoped.
const lockMediaHolderSQL = `SELECT pg_advisory_xact_lock($1)`

func mediaHolderLockKey(kind, ref string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("dating-media-holder:" + kind + ":" + ref))
	return int64(h.Sum64())
}

// MediaHolder is one (kind, ref) that may hold a claim.
type MediaHolder struct {
	Kind string
	Ref  string
}

// ProfileMediaHolders lists what PurgeUserData erases that holds media: the
// user's photos, prompt clips and verification video. The purge releases
// their claims first (service.PurgeProfile).
func (s *Store) ProfileMediaHolders(ctx context.Context, userID uuid.UUID) ([]MediaHolder, error) {
	rows, err := s.db.Query(ctx, `
        SELECT 'p', id::text FROM dating_photos WHERE user_id = $1
        UNION ALL
        SELECT 'c', prompt_id::text FROM dating_prompts WHERE user_id = $1 AND clip_media_id IS NOT NULL
        UNION ALL
        SELECT 'v', user_id::text FROM dating_verifications WHERE user_id = $1 AND selfie_media_id IS NOT NULL`, userID)
	if err != nil {
		return nil, fmt.Errorf("list profile media holders: %w", err)
	}
	defer rows.Close()
	var out []MediaHolder
	for rows.Next() {
		var tag, ref string
		if err := rows.Scan(&tag, &ref); err != nil {
			return nil, err
		}
		switch tag {
		case "p":
			out = append(out, MediaHolder{Kind: mediaclient.KindPhoto, Ref: ref})
		case "c":
			promptID, err := strconv.Atoi(ref)
			if err != nil {
				return nil, err
			}
			out = append(out, MediaHolder{Kind: mediaclient.KindPromptClip, Ref: PromptClipRef(userID, promptID)})
		case "v":
			out = append(out, MediaHolder{Kind: mediaclient.KindVerificationSelfie, Ref: ref})
		}
	}
	return out, rows.Err()
}

// SelfieAttemptHolders lists the user's selfie attempts (PurgeUserAuxiliary
// erases them; service.PurgeUserAuxiliary releases their claims first).
func (s *Store) SelfieAttemptHolders(ctx context.Context, userID uuid.UUID) ([]MediaHolder, error) {
	rows, err := s.db.Query(ctx, `SELECT id::text FROM dating_selfie_attempts WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("list selfie attempts: %w", err)
	}
	defer rows.Close()
	var out []MediaHolder
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		out = append(out, MediaHolder{Kind: mediaclient.KindSelfieAttempt, Ref: ref})
	}
	return out, rows.Err()
}

// VerificationSelfieMedia is the video the user's verification names now.
func (s *Store) VerificationSelfieMedia(ctx context.Context, userID uuid.UUID) (*uuid.UUID, error) {
	var id *uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT selfie_media_id FROM dating_verifications WHERE user_id = $1`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read verification video: %w", err)
	}
	return id, nil
}

// MediaHolderRow is one holder and the asset it names now (nil: none, or the
// row is gone), for the reconcile tool.
type MediaHolderRow struct {
	Kind    string
	Ref     string
	MediaID *uuid.UUID
}

// holderQueries: each kind's page query ($1 cursor ref, $2 limit, $3 all)
// and re-read query. Pages are ordered by the ref's text, which is also the
// cursor.
var holderQueries = map[string]struct{ page, one string }{
	mediaclient.KindPhoto: {
		page: `SELECT id::text, media_id FROM dating_photos
		       WHERE $3::boolean IS NOT NULL AND id::text > $1 ORDER BY id::text LIMIT $2`,
		one: `SELECT media_id FROM dating_photos WHERE id = $1::uuid`,
	},
	mediaclient.KindPromptClip: {
		page: `SELECT user_id::text || ':' || prompt_id::text, clip_media_id FROM dating_prompts
		       WHERE ($3::boolean OR clip_media_id IS NOT NULL) AND user_id::text || ':' || prompt_id::text > $1
		       ORDER BY 1 LIMIT $2`,
		one: `SELECT clip_media_id FROM dating_prompts WHERE user_id = $1::uuid AND prompt_id = $2`,
	},
	mediaclient.KindSelfieAttempt: {
		page: `SELECT id::text, video_media_id FROM dating_selfie_attempts
		       WHERE $3::boolean IS NOT NULL AND id::text > $1 ORDER BY id::text LIMIT $2`,
		one: `SELECT video_media_id FROM dating_selfie_attempts WHERE id = $1::uuid`,
	},
	mediaclient.KindVerificationSelfie: {
		page: `SELECT user_id::text, selfie_media_id FROM dating_verifications
		       WHERE ($3::boolean OR selfie_media_id IS NOT NULL) AND user_id::text > $1 ORDER BY user_id::text LIMIT $2`,
		one: `SELECT selfie_media_id FROM dating_verifications WHERE user_id = $1::uuid`,
	},
}

// MediaHolderKinds is every kind the reconcile tool walks.
var MediaHolderKinds = []string{
	mediaclient.KindPhoto, mediaclient.KindPromptClip,
	mediaclient.KindSelfieAttempt, mediaclient.KindVerificationSelfie,
}

// MediaHolderRows pages through the holders of kind that name media (with
// all, also slots that name none: a cleared clip or verification may still
// hold a stale claim), in ref order after `after`.
func (s *Store) MediaHolderRows(ctx context.Context, kind, after string, limit int, all bool) ([]MediaHolderRow, error) {
	q, ok := holderQueries[kind]
	if !ok {
		return nil, fmt.Errorf("unknown media holder kind %q", kind)
	}
	rows, err := s.db.Query(ctx, q.page, after, limit, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaHolderRow
	for rows.Next() {
		r := MediaHolderRow{Kind: kind}
		if err := rows.Scan(&r.Ref, &r.MediaID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MediaHolderRowFor re-reads one holder under LockMediaHolder. A holder that
// no longer exists names nothing.
func (s *Store) MediaHolderRowFor(ctx context.Context, kind, ref string) (MediaHolderRow, error) {
	q, ok := holderQueries[kind]
	if !ok {
		return MediaHolderRow{}, fmt.Errorf("unknown media holder kind %q", kind)
	}
	row := MediaHolderRow{Kind: kind, Ref: ref}
	var err error
	if kind == mediaclient.KindPromptClip {
		userID, promptID, perr := ParsePromptClipRef(ref)
		if perr != nil {
			return row, perr
		}
		err = s.db.QueryRow(ctx, q.one, userID, promptID).Scan(&row.MediaID)
	} else {
		if _, perr := uuid.Parse(ref); perr != nil {
			return row, fmt.Errorf("%s ref %q: %w", kind, ref, perr)
		}
		err = s.db.QueryRow(ctx, q.one, ref).Scan(&row.MediaID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return MediaHolderRow{Kind: kind, Ref: ref}, nil
	}
	return row, err
}
