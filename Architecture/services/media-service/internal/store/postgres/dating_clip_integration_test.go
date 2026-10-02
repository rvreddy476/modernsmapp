//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Migration 026 and MarkDatingClip against a real database: the widened
// access_scope CHECK takes 'dating_clip' (and still refuses anything else),
// and only the uploader can scope their own unscoped asset.
func TestDatingClipScopeIntegration(t *testing.T) {
	pool := subtitleEventsPool(t) // DSN, _test name guard, real bootstrap with migrations
	requireScratchDatabase(t, pool)
	ctx := context.Background()
	const prefix = "dating-clip-it/"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_assets WHERE storage_key LIKE $1`, prefix+"%")
	})
	seed := func(uploader uuid.UUID, scope *string) uuid.UUID {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO media_assets (id, uploader_id, file_type, media_subtype, mime_type,
			    file_size_bytes, storage_bucket, storage_key, processing_status, access_scope, created_at, updated_at)
			VALUES ($1, $2, 'audio', 'general', 'audio/mp4', 100, 'media', $3, 'ready', $4, NOW(), NOW())`,
			id, uploader, prefix+id.String()+"/original", scope); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return id
	}
	scopeOf := func(id uuid.UUID) string {
		var s *string
		if err := pool.QueryRow(ctx, `SELECT access_scope FROM media_assets WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		if s == nil {
			return ""
		}
		return *s
	}
	st := New(pool)
	owner := uuid.New()
	clip := seed(owner, nil)
	if err := st.MarkDatingClip(ctx, clip, owner); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := st.MarkDatingClip(ctx, clip, owner); err != nil {
		t.Fatalf("mark again: %v", err)
	}
	if got := scopeOf(clip); got != AccessScopeDatingClip {
		t.Fatalf("scope = %q", got)
	}
	if err := st.MarkDatingClip(ctx, clip, uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("someone else's asset: %v, want no rows", err)
	}
	anon := "anonymous"
	other := seed(owner, &anon)
	if err := st.MarkDatingClip(ctx, other, owner); !errors.Is(err, ErrScopeConflict) {
		t.Fatalf("an anonymous asset: %v, want a scope conflict", err)
	}
	if got := scopeOf(other); got != "anonymous" {
		t.Fatalf("a refused asset changed scope to %q", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_assets SET access_scope = 'bogus' WHERE id = $1`, clip); err == nil {
		t.Fatalf("the CHECK accepted an unknown scope")
	}
}
