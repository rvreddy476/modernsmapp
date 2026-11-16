package postgres

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A sound lives under audio/<uploader>/<media>/, outside the asset prefix.
// Since 2026-11-16 (migration 032) the author deleting the video RETIRES a
// ready sound (it keeps its objects; the reels that play it keep playing
// it); its objects go only on an erasure, a takedown, or once no post
// references it. These pin the SQL every path shares; the integration test
// runs it (sound_reclaim_integration_test.go).

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestSoundObjectKeysSelectsAudioAndWaveformOfTheMatch(t *testing.T) {
	q := squash(soundObjectKeysSQL("id = $1"))
	for _, want := range []string{
		"audio_key AS k FROM audio_tracks WHERE id = $1",
		"waveform_key FROM audio_tracks WHERE id = $1",
		"s.k IS NOT NULL AND s.k <> ''",
		// The ledger's media_id is NOT NULL: a retired sound has no source.
		"COALESCE(source_media_id, former_source_media_id, id) AS ledger_id",
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("sound key query lacks %q:\n%s", want, q)
		}
	}
	if !strings.HasPrefix(q, "SELECT DISTINCT ON (s.k) s.k, s.ledger_id FROM") {
		t.Fatalf("sound key query must select (key, ledger id): %s", q)
	}
}

// A key any OTHER sound row names (a library row, a retired sound, a row
// another service wrote) is never reclaimed with this one.
func TestSoundObjectKeysSparesKeysOtherSoundsName(t *testing.T) {
	q := squash(soundObjectKeysSQL("id = $1"))
	guard := regexp.MustCompile(`NOT EXISTS \( SELECT 1 FROM audio_tracks o WHERE o\.id NOT IN \(SELECT id FROM audio_tracks WHERE id = \$1\) AND \(o\.audio_key = s\.k OR o\.waveform_key = s\.k\)\)`)
	if !guard.MatchString(q) {
		t.Fatalf("sound key query reclaims keys other sounds still play:\n%s", q)
	}
}

// Retiring keeps the objects and records what the row needs later; only a
// READY sound is retired.
func TestSoundRetireKeepsKeysAndRecordsTheOwner(t *testing.T) {
	q := squash(soundRetireSQL("= $1"))
	for _, want := range []string{
		"UPDATE audio_tracks t SET",
		"former_source_media_id = t.source_media_id",
		"source_uploader_id = COALESCE(t.source_uploader_id, (SELECT m.uploader_id FROM media_assets m WHERE m.id = t.source_media_id))",
		"source_media_id = NULL", "status = 'retired'", "retired_at = NOW()",
		"WHERE t.source_media_id = $1 AND t.status = 'ready'",
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("sound retire lacks %q: %s", want, q)
		}
	}
	for _, never := range []string{"audio_key", "waveform_key", "'deleted'"} {
		if strings.Contains(q, never) {
			t.Fatalf("retiring a sound must keep its objects; it touches %s: %s", never, q)
		}
	}
}

func TestSoundDestroyMarksDeletedAndForgetsKeys(t *testing.T) {
	q := squash(soundDestroySQL("id = $1"))
	for _, want := range []string{
		"UPDATE audio_tracks SET",
		"source_media_id = NULL", "status = 'deleted'", "audio_key = NULL", "waveform_key = NULL",
		"WHERE id = $1",
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("sound destroy lacks %q: %s", want, q)
		}
	}
	if got := squash(soundUnplayableOfSource("= $1")); got != "source_media_id = $1 AND status <> 'ready'" {
		t.Fatalf("unplayable-of-source match = %q", got)
	}
}

// An account erasure records the keys of ALL the user's sounds, the ones
// retired when they deleted a video earlier included, BEFORE destroying
// them, and destroys them before the assets go.
func TestPurgeUserReclaimsSoundsBeforeDestroyingThem(t *testing.T) {
	record, destroy, assets := -1, -1, -1
	for i, st := range purgeUserSteps() {
		sql := squash(st.sql)
		switch {
		case st.table == "audio_tracks" && strings.HasPrefix(sql, "INSERT INTO media_blob_reclaim"):
			if !strings.Contains(sql, "waveform_key FROM audio_tracks") || !strings.Contains(sql, "NOT EXISTS") {
				t.Fatalf("erasure sound reclaim is malformed: %s", sql)
			}
			if !strings.Contains(sql, "source_media_id IS NULL AND source_uploader_id = $1") {
				t.Fatalf("erasure misses the sounds the user retired earlier: %s", sql)
			}
			record = i
		case st.table == "audio_tracks" && strings.HasPrefix(sql, "UPDATE audio_tracks"):
			if !strings.Contains(sql, "status = 'deleted'") || !strings.Contains(sql, "audio_key = NULL") ||
				!strings.Contains(sql, "source_uploader_id = $1") {
				t.Fatalf("erasure sound destroy is malformed: %s", sql)
			}
			destroy = i
		case st.table == "media_assets" && strings.HasPrefix(sql, "DELETE FROM media_assets"):
			assets = i
		}
	}
	if record < 0 {
		t.Fatal("PurgeUser never records the objects of the user's sounds; they leak in storage")
	}
	if destroy < 0 || record > destroy || destroy > assets {
		t.Fatalf("order must be record (%d) < destroy (%d) < assets (%d)", record, destroy, assets)
	}
}

// Every path by which the author deletes a video (the referrer purge after
// the restore window, the owner's delete) retires the ready sound, and
// reclaims only a sound that never became playable.
func TestEveryAuthorDeleteRetiresTheReadySound(t *testing.T) {
	for file, fn := range map[string]string{
		"asset_purge.go": "func (s *MediaAssetStore) DeleteAssetForReferrer(",
		"media.go":       "func (s *MediaAssetStore) DeleteMedia(",
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		body := string(src)
		i := strings.Index(body, fn)
		if i < 0 {
			t.Fatalf("%s: %s not found", file, fn)
		}
		body = body[i:]
		if j := strings.Index(body[1:], "\nfunc "); j > 0 {
			body = body[:j+1]
		}
		keys := strings.Index(body, `collectSoundKeys(ctx, tx, soundObjectKeysSQL(soundUnplayableOfSource("= $1"))`)
		reclaim := strings.Index(body, "INSERT INTO media_blob_reclaim")
		if keys < 0 || reclaim < 0 || keys > reclaim {
			t.Fatalf("%s: the unplayable sound's keys are not collected before the reclaim ledger is written", fn)
		}
		if strings.Contains(body, `soundObjectKeysSQL("= $1")`) {
			t.Fatalf("%s: reclaims every sound of the video; a ready sound must be retired with its objects", fn)
		}
		destroy := strings.Index(body, `soundDestroySQL(soundUnplayableOfSource("= $1"))`)
		retire := strings.Index(body, `soundRetireSQL("= $1")`)
		del := strings.Index(body, "DELETE FROM media_assets WHERE id = $1")
		if destroy < 0 || retire < 0 || destroy > retire || retire > del {
			t.Fatalf("%s: order must be destroy unplayable (%d) < retire (%d) < delete asset (%d)", fn, destroy, retire, del)
		}
	}
}

// A takedown is one extracted sound; a library sound is the library's.
func TestTakedownMatchesOneExtractedSound(t *testing.T) {
	if soundTakedownMatch != "id = $1 AND library_id IS NULL" {
		t.Fatalf("takedown match = %q", soundTakedownMatch)
	}
}

// The sweep takes only retired sounds past the grace that no post names
// (a soft-deleted post still does: it may be restored), skips rows another
// sweeper holds, and re-checks both inside the destroy.
func TestRetiredSoundSweepTakesOnlyUnreferencedSounds(t *testing.T) {
	q := squash(unusedRetiredSoundsSQL)
	for _, want := range []string{
		"t.status = 'retired'",
		"t.retired_at IS NOT NULL AND t.retired_at < $1",
		"NOT EXISTS (SELECT 1 FROM posts p WHERE p.audio_track_id = t.id)",
		"LIMIT $2", "FOR UPDATE OF t SKIP LOCKED",
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("sweep lacks %q: %s", want, q)
		}
	}
	if strings.Contains(q, "deleted_at") {
		t.Fatalf("sweep must count a soft-deleted post as a reference: %s", q)
	}
	m := squash(unusedRetiredSoundMatch)
	for _, want := range []string{"id = ANY($1::uuid[])", "status = 'retired'", "NOT EXISTS (SELECT 1 FROM posts p WHERE p.audio_track_id = audio_tracks.id)"} {
		if !strings.Contains(m, want) {
			t.Fatalf("sweep destroy match lacks %q: %s", want, m)
		}
	}
}

// A retired sound is heard only through a LIVE post that plays it, by the
// post's first video in carousel order.
func TestRetiredSoundCarrierIsALivePostPlayingTheSound(t *testing.T) {
	q := squash(retiredSoundCarrierSQL)
	for _, want := range []string{
		"p.id = $2", "p.audio_track_id = $1", "p.deleted_at IS NULL", "pm.kind = 'video'",
		"ORDER BY pm.position NULLS LAST, pm.media_id", "LIMIT 1",
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("carrier query lacks %q: %s", want, q)
		}
	}
}

// The wire says why a retired sound may not go on a new post.
func TestRetiredSoundIsUnavailableForNewPosts(t *testing.T) {
	a := AudioTrack{Status: SoundStatusRetired}
	if a.AvailableForNewPosts() || a.unavailableReason() != "retired" {
		t.Fatalf("retired: available=%v reason=%q", a.AvailableForNewPosts(), a.unavailableReason())
	}
}
