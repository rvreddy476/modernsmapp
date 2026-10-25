// The holder queries of the media claim protocol (media_holders.go) against a
// real Postgres. TEST_PG_DSN on a "_test" database, CI only.
package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/dating-service/internal/mediaclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func holdersITStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping media holder integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.BootstrapSchema(ctx, pool); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	return New(pool)
}

func TestMediaHoldersIT_ChosenIDsAndHolderListings(t *testing.T) {
	s := holdersITStore(t)
	ctx := context.Background()
	user := uuid.New()
	ensureProfileForTest(t, s, user)

	photoID, photoMedia := uuid.New(), uuid.New()
	photo, err := s.CreateModeratedPhoto(ctx, user, CreatePhotoParams{ID: photoID, MediaID: photoMedia}, 6,
		PhotoDecision{Status: PhotoStatusApproved, Source: PhotoSourceAuto})
	if err != nil {
		t.Fatalf("photo: %v", err)
	}
	if photo.ID != photoID {
		t.Fatalf("photo id %s, want the chosen %s (its claim names it)", photo.ID, photoID)
	}
	clip := uuid.New()
	if _, err := s.SetPromptClip(ctx, user, 3, clip, "audio", 5000, ClipStatusApproved, nil); err != nil {
		t.Fatalf("clip: %v", err)
	}
	video := uuid.New()
	if _, err := s.db.Exec(ctx, `INSERT INTO dating_verifications (user_id, selfie_status, selfie_media_id) VALUES ($1, 'failed', $2)`, user, video); err != nil {
		t.Fatalf("verification: %v", err)
	}

	holders, err := s.ProfileMediaHolders(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	want := map[MediaHolder]bool{
		{Kind: mediaclient.KindPhoto, Ref: photoID.String()}:            true,
		{Kind: mediaclient.KindPromptClip, Ref: PromptClipRef(user, 3)}: true,
		{Kind: mediaclient.KindVerificationSelfie, Ref: user.String()}:  true,
	}
	if len(holders) != len(want) {
		t.Fatalf("holders = %v", holders)
	}
	for _, h := range holders {
		if !want[h] {
			t.Fatalf("unexpected holder %v (all: %v)", h, holders)
		}
	}

	for kind, wantID := range map[string]uuid.UUID{
		mediaclient.KindPhoto: photoMedia, mediaclient.KindPromptClip: clip, mediaclient.KindVerificationSelfie: video,
	} {
		ref := map[string]string{
			mediaclient.KindPhoto: photoID.String(), mediaclient.KindPromptClip: PromptClipRef(user, 3),
			mediaclient.KindVerificationSelfie: user.String(),
		}[kind]
		row, err := s.MediaHolderRowFor(ctx, kind, ref)
		if err != nil || row.MediaID == nil || *row.MediaID != wantID {
			t.Fatalf("%s re-read = %+v, %v", kind, row, err)
		}
		found := false
		after := ""
		for !found {
			rows, err := s.MediaHolderRows(ctx, kind, after, 200, false)
			if err != nil {
				t.Fatalf("%s page: %v", kind, err)
			}
			for _, r := range rows {
				if r.Ref == ref {
					found = r.MediaID != nil && *r.MediaID == wantID
				}
			}
			if len(rows) < 200 {
				break
			}
			after = rows[len(rows)-1].Ref
		}
		if !found {
			t.Fatalf("%s %s was not listed with its media", kind, ref)
		}
	}

	if got, err := s.VerificationSelfieMedia(ctx, user); err != nil || got == nil || *got != video {
		t.Fatalf("verification video = %v, %v", got, err)
	}
	if got, err := s.VerificationSelfieMedia(ctx, uuid.New()); err != nil || got != nil {
		t.Fatalf("no verification = %v, %v", got, err)
	}
	gone, err := s.MediaHolderRowFor(ctx, mediaclient.KindPhoto, uuid.NewString())
	if err != nil || gone.MediaID != nil {
		t.Fatalf("a missing photo = %+v, %v", gone, err)
	}
}

func TestMediaHoldersIT_AttemptIDsAndLock(t *testing.T) {
	s := holdersITStore(t)
	ctx := context.Background()
	user := uuid.New()
	if _, err := s.db.Exec(ctx, `INSERT INTO dating_selfie_attempts (id, user_id, challenge_id, video_media_id, instruction)
		VALUES ($1, $2, $3, $4, 'blink_twice')`, uuid.New(), user, uuid.New(), uuid.New()); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}
	attempts, err := s.SelfieAttemptHolders(ctx, user)
	if err != nil || len(attempts) != 1 || attempts[0].Kind != mediaclient.KindSelfieAttempt {
		t.Fatalf("attempt holders = %v, %v", attempts, err)
	}

	ref := uuid.NewString()
	unlock, err := s.LockMediaHolder(ctx, mediaclient.KindPhoto, ref)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.LockMediaHolder(ctx, mediaclient.KindPromptClip, ref)
	if err != nil {
		t.Fatalf("another holder waited: %v", err)
	}
	other()
	got := make(chan struct{})
	go func() {
		if u, err := s.LockMediaHolder(ctx, mediaclient.KindPhoto, ref); err == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		unlock()
		t.Fatal("a second writer of the same holder did not wait")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("unlock did not free the holder")
	}
}
