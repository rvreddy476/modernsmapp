//go:build integration

package postgres

import (
	"context"
	"github.com/atpost/media-service/database"
	"github.com/google/uuid"
	"testing"
)

func TestDoorstepPhotoScopeIntegration(t *testing.T) {
	pool := subtitleEventsPool(t)
	requireScratchDatabase(t, pool)
	ctx := context.Background()
	if err := BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	owner := uuid.New()
	st := New(pool)
	seed := func(kind, processing, moderation, key string, scope *string) uuid.UUID {
		id := uuid.New()
		_, err := pool.Exec(ctx, `INSERT INTO media_assets (id,uploader_id,file_type,media_subtype,mime_type,file_size_bytes,storage_bucket,storage_key,processing_status,moderation_status,access_scope,upload_purpose,created_at,updated_at) VALUES($1,$2,$3,'general','image/jpeg',100,'media',$4,$5,$6,$7,'post',NOW(),NOW())`, id, owner, kind, key, processing, moderation, scope)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM media_assets WHERE id=$1`, id) })
		return id
	}
	good := seed("image", "ready", "passed", "user/doorstep-it/good", nil)
	if ok, err := st.PrepareDoorstepPhoto(ctx, good, uuid.New()); err != nil || ok {
		t.Fatalf("foreign owner accepted: %v %v", ok, err)
	}
	for i := 0; i < 2; i++ {
		if ok, err := st.PrepareDoorstepPhoto(ctx, good, owner); err != nil || !ok {
			t.Fatalf("idempotent preparation: %v %v", ok, err)
		}
	}
	var scope string
	var purpose *string
	if err := pool.QueryRow(ctx, `SELECT access_scope,upload_purpose FROM media_assets WHERE id=$1`, good).Scan(&scope, &purpose); err != nil || scope != AccessScopeDoorstepPhoto || purpose != nil {
		t.Fatalf("scope=%s reclaim lease=%v error=%v", scope, purpose, err)
	}
	anonymous := "anonymous"
	for _, id := range []uuid.UUID{
		seed("video", "ready", "passed", "user/doorstep-it/video", nil),
		seed("image", "processing", "passed", "user/doorstep-it/pending", nil),
		seed("image", "ready", "rejected", "user/doorstep-it/rejected", nil),
		seed("image", "ready", "passed", "public/doorstep-it/public", nil),
		seed("image", "ready", "passed", "user/doorstep-it/anonymous", &anonymous),
	} {
		if ok, err := st.PrepareDoorstepPhoto(ctx, id, owner); err != nil || ok {
			t.Fatalf("unsafe photo accepted: %v %v", ok, err)
		}
	}
}
