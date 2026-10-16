//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The admin sound library (migration 029) against live PostgreSQL, on both
// shapes of audio_tracks, each in a schema of its own:
//
//	POSTGRES_DSN=postgres://…/<name>_test go test -tags integration ./internal/store/postgres/ -run AdminSound -v
//
// The properties under test: a new row is a draft that no public read and
// no /use sees; enabling makes it public and it never becomes a draft again;
// a withdrawn row can never be enabled again (the store refuses and the
// database CHECK refuses); withdraw everywhere silences the materialised
// sound; the legacy rows read as published.

func adminSoundSchema(t *testing.T, shape string) (*MediaAssetStore, func(sql string, args ...any)) {
	t.Helper()
	store, exec := librarySchema(t, shape)
	applyMediaMigration(t, store.db, "029_admin_sound_library.sql")
	return store, exec
}

func newAdminSound(key string, active bool) AdminLibrarySoundInsert {
	mood, ref := "calm", "INV-1"
	return AdminLibrarySoundInsert{
		StorageKey: key, Title: "Monsoon", Artist: "Momentum Library", Genre: "pop", Mood: &mood,
		DurationMs: 61_500, LicenseType: "royalty_free", RightsHolder: "Momentum Records",
		LicenseReference: &ref, IsActive: active, CreatedBy: uuid.New(),
	}
}

func adminKey() string { return "audio-library/" + uuid.NewString() + ".m4a" }

func TestAdminSoundMigrationBackfillsLegacyRowsOnce(t *testing.T) {
	for name, shape := range soundShapes() {
		t.Run(name, func(t *testing.T) {
			store, exec := librarySchema(t, shape)
			ctx := context.Background()
			licensed, retired, original, upload := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			exec(`INSERT INTO audio_library (id, title, duration_ms, audio_url, source, is_licensed)
				VALUES ($1, 'Licensed', 1000, 'library/a.m4a', 'licensed', TRUE)`, licensed)
			exec(`INSERT INTO audio_library (id, title, duration_ms, audio_url, source, is_active)
				VALUES ($1, 'Retired', 1000, 'library/b.m4a', 'licensed', FALSE)`, retired)
			exec(`INSERT INTO audio_library (id, title, duration_ms, audio_url, source)
				VALUES ($1, 'Original', 1000, 'library/c.m4a', 'original')`, original)
			exec(`INSERT INTO audio_library (id, title, duration_ms, audio_url, source)
				VALUES ($1, 'Upload', 1000, 'library/d.m4a', 'user_upload')`, upload)
			applyMediaMigration(t, store.db, "029_admin_sound_library.sql")

			for id, want := range map[uuid.UUID][2]string{
				licensed: {LibraryStatusActive, "licensed"},
				retired:  {LibraryStatusInactive, "licensed"},
				original: {LibraryStatusActive, "original"},
				upload:   {LibraryStatusActive, ""},
			} {
				got, err := store.GetAdminLibrarySound(ctx, id)
				if err != nil || got == nil {
					t.Fatalf("%s: %+v, %v", id, got, err)
				}
				lt := ""
				if got.LicenseType != nil {
					lt = *got.LicenseType
				}
				if got.Status != want[0] || lt != want[1] {
					t.Fatalf("%s: status %q licence %q, want %v", got.Title, got.Status, lt, want)
				}
			}

			// A draft made after the migration stays a draft on a replay.
			draft, err := store.CreateAdminLibrarySound(ctx, newAdminSound(adminKey(), false))
			if err != nil {
				t.Fatal(err)
			}
			applyMediaMigration(t, store.db, "029_admin_sound_library.sql")
			again, err := store.GetAdminLibrarySound(ctx, draft.ID)
			if err != nil || again.Status != LibraryStatusDraft {
				t.Fatalf("after replay: %+v, %v", again, err)
			}
		})
	}
}

func TestAdminSoundDraftIsNeverPublic(t *testing.T) {
	for name, shape := range soundShapes() {
		t.Run(name, func(t *testing.T) {
			store, _ := adminSoundSchema(t, shape)
			ctx := context.Background()
			in := newAdminSound(adminKey(), false)
			draft, err := store.CreateAdminLibrarySound(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			if draft.Status != LibraryStatusDraft || draft.IsActive || draft.DurationMs != 61_500 ||
				draft.CreatedBy == nil || *draft.CreatedBy != in.CreatedBy || draft.LicenseType == nil ||
				*draft.LicenseType != "royalty_free" || draft.AudioURL != in.StorageKey || draft.AudioTrackID != nil {
				t.Fatalf("draft = %+v", draft)
			}
			if got, err := store.GetAudioLibraryTrack(ctx, draft.ID); err != nil || got != nil {
				t.Fatalf("public get of a draft = %+v, %v", got, err)
			}
			if list, err := store.GetTrendingAudioLibrary(ctx, nil, 50, 0); err != nil || len(list) != 0 {
				t.Fatalf("public list = %+v, %v", list, err)
			}
			if list, err := store.SearchAudioLibrary(ctx, "Monsoon", 50, 0); err != nil || len(list) != 0 {
				t.Fatalf("public search = %+v, %v", list, err)
			}
			if _, err := store.EnsureLibrarySound(ctx, draft.ID, in.StorageKey); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("use of a draft: %v, want pgx.ErrNoRows", err)
			}
			// The same upload cannot back a second row.
			if _, err := store.CreateAdminLibrarySound(ctx, in); !errors.Is(err, ErrLibraryKeyInUse) {
				t.Fatalf("second row for one key: %v", err)
			}
			if used, err := store.AdminLibraryKeyInUse(ctx, in.StorageKey); err != nil || !used {
				t.Fatalf("AdminLibraryKeyInUse = %v, %v", used, err)
			}

			// Enable: public; disable: inactive, never a draft again.
			on := true
			now := time.Now()
			active, err := store.PatchAdminLibrarySound(ctx, draft.ID, AdminLibrarySoundPatch{IsActive: &on}, now)
			if err != nil || active.Status != LibraryStatusActive {
				t.Fatalf("enable: %+v, %v", active, err)
			}
			if list, err := store.GetTrendingAudioLibrary(ctx, nil, 50, 0); err != nil || len(list) != 1 || list[0].ID != draft.ID {
				t.Fatalf("public list after enable = %+v, %v", list, err)
			}
			off := false
			inactive, err := store.PatchAdminLibrarySound(ctx, draft.ID, AdminLibrarySoundPatch{IsActive: &off}, now)
			if err != nil || inactive.Status != LibraryStatusInactive {
				t.Fatalf("disable: %+v, %v", inactive, err)
			}
			if list, err := store.GetTrendingAudioLibrary(ctx, nil, 50, 0); err != nil || len(list) != 0 {
				t.Fatalf("public list after disable = %+v, %v", list, err)
			}

			// A lapsed licence cannot be enabled, in the same edit or later.
			past := now.Add(-time.Hour)
			if _, err := store.PatchAdminLibrarySound(ctx, draft.ID, AdminLibrarySoundPatch{
				IsActive: &on, LicenseExpiresAt: OptTime{Set: true, Value: &past}}, now); !errors.Is(err, ErrLibraryLicenseExpired) {
				t.Fatalf("enable with a lapsed licence: %v", err)
			}
			if got, _ := store.GetAdminLibrarySound(ctx, draft.ID); got.IsActive || got.LicenseExpiresAt != nil {
				t.Fatalf("a refused edit changed the row: %+v", got)
			}
		})
	}
}

func TestAdminSoundEditFollowsOntoTheSound(t *testing.T) {
	for name, shape := range soundShapes() {
		t.Run(name, func(t *testing.T) {
			store, _ := adminSoundSchema(t, shape)
			ctx := context.Background()
			in := newAdminSound(adminKey(), true)
			row, err := store.CreateAdminLibrarySound(ctx, in)
			if err != nil || row.Status != LibraryStatusActive {
				t.Fatalf("create active: %+v, %v", row, err)
			}
			track, err := store.EnsureLibrarySound(ctx, row.ID, in.StorageKey)
			if err != nil {
				t.Fatal(err)
			}
			if track.LicenseType != "standard" {
				t.Fatalf("royalty-free sound materialised as %q", track.LicenseType)
			}
			title, genre := "Monsoon (Edit)", "lofi"
			got, err := store.PatchAdminLibrarySound(ctx, row.ID, AdminLibrarySoundPatch{
				Title: OptString{Set: true, Value: &title}, Genre: OptString{Set: true, Value: &genre},
				Mood: OptString{Set: true}, LicenseReference: OptString{Set: true},
			}, time.Now())
			if err != nil || got.Title != title || got.Genre == nil || *got.Genre != genre || got.Mood != nil ||
				got.LicenseReference != nil || got.AudioTrackID == nil || *got.AudioTrackID != track.ID {
				t.Fatalf("patch = %+v, %v", got, err)
			}
			if !got.UpdatedAt.After(row.UpdatedAt) && !got.UpdatedAt.Equal(row.UpdatedAt) {
				t.Fatalf("updated_at went backwards")
			}
			after, err := store.GetAudioTrack(ctx, track.ID)
			if err != nil || after.Title != title || after.Genre == nil || *after.Genre != genre {
				t.Fatalf("the sound kept its old metadata: %+v, %v", after, err)
			}
			if missing, err := store.PatchAdminLibrarySound(ctx, uuid.New(), AdminLibrarySoundPatch{Title: OptString{Set: true, Value: &title}}, time.Now()); err != nil || missing != nil {
				t.Fatalf("patch of no row = %+v, %v", missing, err)
			}
		})
	}
}

func TestAdminSoundWithdraw(t *testing.T) {
	for name, shape := range soundShapes() {
		t.Run(name, func(t *testing.T) {
			store, exec := adminSoundSchema(t, shape)
			ctx := context.Background()
			in := newAdminSound(adminKey(), true)
			row, err := store.CreateAdminLibrarySound(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			track, err := store.EnsureLibrarySound(ctx, row.ID, in.StorageKey)
			if err != nil {
				t.Fatal(err)
			}

			// new_uses: no new posts, existing ones keep playing.
			w, err := store.WithdrawAdminLibrarySound(ctx, row.ID, WithdrawNewUses, "licence under review")
			if err != nil || w.Status != LibraryStatusWithdrawn || w.IsActive || w.WithdrawScope == nil ||
				*w.WithdrawScope != WithdrawNewUses || w.WithdrawReason == nil || *w.WithdrawReason != "licence under review" ||
				w.WithdrawnAt == nil || w.AudioTrackID == nil {
				t.Fatalf("withdraw new_uses = %+v, %v", w, err)
			}
			if tr, err := store.GetAudioTrack(ctx, track.ID); err != nil || tr.Status != "ready" {
				t.Fatalf("new_uses touched the sound: %+v, %v", tr, err)
			}
			if got, _ := store.GetAudioLibraryTrack(ctx, row.ID); got != nil {
				t.Fatal("a withdrawn row is still public")
			}
			if _, err := store.EnsureLibrarySound(ctx, row.ID, in.StorageKey); err != nil {
				// The row exists already: /use re-reads it and the service
				// refuses an inactive library row before that (Use).
				t.Fatalf("re-read of the existing sound: %v", err)
			}

			// Never enabled again: the store refuses, and so does the database.
			on := true
			if _, err := store.PatchAdminLibrarySound(ctx, row.ID, AdminLibrarySoundPatch{IsActive: &on}, time.Now()); !errors.Is(err, ErrLibrarySoundWithdrawn) {
				t.Fatalf("enable a withdrawn row: %v", err)
			}
			if _, err := store.db.Exec(ctx, `UPDATE audio_library SET is_active = TRUE WHERE id = $1`, row.ID); err == nil {
				t.Fatal("the database accepted an active withdrawn row")
			}

			// everywhere: the sound is silenced; a later new_uses never
			// narrows it back.
			first := *w.WithdrawnAt
			w, err = store.WithdrawAdminLibrarySound(ctx, row.ID, WithdrawEverywhere, "rights holder complaint")
			if err != nil || *w.WithdrawScope != WithdrawEverywhere || !w.WithdrawnAt.Equal(first) || *w.WithdrawReason != "rights holder complaint" {
				t.Fatalf("withdraw everywhere = %+v, %v", w, err)
			}
			if tr, err := store.GetAudioTrack(ctx, track.ID); err != nil || tr.Status != "deleted" {
				t.Fatalf("everywhere left the sound playing: %+v, %v", tr, err)
			}
			if w.AudioTrackID == nil || *w.AudioTrackID != track.ID {
				t.Fatalf("the admin row lost its sound link: %+v", w)
			}
			w, err = store.WithdrawAdminLibrarySound(ctx, row.ID, WithdrawNewUses, "again")
			if err != nil || *w.WithdrawScope != WithdrawEverywhere {
				t.Fatalf("narrowed to %+v, %v", w.WithdrawScope, err)
			}
			if tr, _ := store.GetAudioTrack(ctx, track.ID); tr.Status != "deleted" {
				t.Fatal("the sound came back")
			}

			// Withdraw everywhere on a never-used row touches no track.
			other, err := store.CreateAdminLibrarySound(ctx, newAdminSound(adminKey(), false))
			if err != nil {
				t.Fatal(err)
			}
			if w, err := store.WithdrawAdminLibrarySound(ctx, other.ID, WithdrawEverywhere, "never cleared"); err != nil || w.AudioTrackID != nil {
				t.Fatalf("withdraw unused: %+v, %v", w, err)
			}
			var ready int
			if err := store.db.QueryRow(ctx, `SELECT COUNT(*) FROM audio_tracks WHERE status = 'ready'`).Scan(&ready); err != nil || ready != 0 {
				t.Fatalf("%d ready tracks (%v)", ready, err)
			}
			if missing, err := store.WithdrawAdminLibrarySound(ctx, uuid.New(), WithdrawEverywhere, "nothing"); err != nil || missing != nil {
				t.Fatalf("withdraw of no row = %+v, %v", missing, err)
			}
			_ = exec
		})
	}
}

func TestAdminSoundList(t *testing.T) {
	store, exec := adminSoundSchema(t, mediaFirstAudioTracks)
	ctx := context.Background()
	mk := func(title, artist, genre string, active bool, age time.Duration) uuid.UUID {
		in := newAdminSound(adminKey(), active)
		in.Title, in.Artist, in.Genre = title, artist, genre
		row, err := store.CreateAdminLibrarySound(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE audio_library SET created_at = NOW() - $2 * INTERVAL '1 second' WHERE id = $1`, row.ID, int(age.Seconds()))
		return row.ID
	}
	draft := mk("Draft 100%", "Asha", "pop", false, 1*time.Hour)
	active := mk("Active", "Ravi_K", "Pop", true, 2*time.Hour)
	inactive := mk("Inactive", "Asha", "rock", true, 3*time.Hour)
	off := false
	if _, err := store.PatchAdminLibrarySound(ctx, inactive, AdminLibrarySoundPatch{IsActive: &off}, time.Now()); err != nil {
		t.Fatal(err)
	}
	withdrawn := mk("Withdrawn", "Zed", "pop", true, 4*time.Hour)
	if _, err := store.WithdrawAdminLibrarySound(ctx, withdrawn, WithdrawNewUses, "test reason"); err != nil {
		t.Fatal(err)
	}

	ids := func(f AdminSoundFilter) []uuid.UUID {
		t.Helper()
		rows, err := store.ListAdminLibrarySounds(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			out[i] = r.ID
		}
		return out
	}
	same := func(got []uuid.UUID, want ...uuid.UUID) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	for name, c := range map[string]struct {
		f    AdminSoundFilter
		want []uuid.UUID
	}{
		"all newest first": {AdminSoundFilter{}, []uuid.UUID{draft, active, inactive, withdrawn}},
		"active":           {AdminSoundFilter{Status: "active"}, []uuid.UUID{active}},
		"inactive":         {AdminSoundFilter{Status: "inactive"}, []uuid.UUID{draft, inactive}},
		"draft":            {AdminSoundFilter{Status: "draft"}, []uuid.UUID{draft}},
		"withdrawn":        {AdminSoundFilter{Status: "withdrawn"}, []uuid.UUID{withdrawn}},
		"q title":          {AdminSoundFilter{Q: "100%"}, []uuid.UUID{draft}},
		"q artist":         {AdminSoundFilter{Q: "asha"}, []uuid.UUID{draft, inactive}},
		"q underscore":     {AdminSoundFilter{Q: "i_k"}, []uuid.UUID{active}},
		"q underscore lit": {AdminSoundFilter{Q: "a_h"}, nil},
		"genre any case":   {AdminSoundFilter{Genre: "POP"}, []uuid.UUID{draft, active, withdrawn}},
		"limit":            {AdminSoundFilter{Limit: 2}, []uuid.UUID{draft, active}},
	} {
		if got := ids(c.f); !same(got, c.want...) {
			t.Fatalf("%s: %v, want %v", name, got, c.want)
		}
	}
	// Keyset paging continues after the last row.
	first, err := store.ListAdminLibrarySounds(ctx, AdminSoundFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	last := first[1]
	if got := ids(AdminSoundFilter{Limit: 2, AfterAt: &last.CreatedAt, AfterID: last.ID}); !same(got, inactive, withdrawn) {
		t.Fatalf("page 2: %v", got)
	}
	if _, err := store.ListAdminLibrarySounds(ctx, AdminSoundFilter{Status: "bogus"}); err == nil {
		t.Fatal("unknown status accepted")
	}
}
