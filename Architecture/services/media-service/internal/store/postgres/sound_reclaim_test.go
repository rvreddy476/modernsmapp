package postgres

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A purged source video's sound lives under audio/<uploader>/<media>/,
// outside the asset prefix, so the purge must find its keys by query and
// retire the row. These pin the SQL every purge path shares.

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestSoundObjectKeysSelectsAudioAndWaveformOfTheSource(t *testing.T) {
	q := squash(soundObjectKeysSQL("= $1"))
	for _, want := range []string{
		"SELECT source_media_id, audio_key AS k FROM audio_tracks WHERE source_media_id = $1",
		"SELECT source_media_id, waveform_key AS k FROM audio_tracks WHERE source_media_id = $1",
		"s.k IS NOT NULL AND s.k <> ''",
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("sound key query lacks %q:\n%s", want, q)
		}
	}
	// Two columns: the purge scans (key, source).
	if !strings.HasPrefix(q, "SELECT DISTINCT ON (s.k) s.k, s.source_media_id FROM") {
		t.Fatalf("sound key query must select (key, source): %s", q)
	}
}

// A key some sound of another source (or a library row, source NULL) also
// names must never be reclaimed by this source's purge.
func TestSoundObjectKeysSparesKeysOtherSoundsName(t *testing.T) {
	q := squash(soundObjectKeysSQL("= $1"))
	guard := regexp.MustCompile(`NOT EXISTS \( SELECT 1 FROM audio_tracks o WHERE \(o\.source_media_id IS NULL OR NOT \(o\.source_media_id = \$1\)\) AND \(o\.audio_key = s\.k OR o\.waveform_key = s\.k\)\)`)
	if !guard.MatchString(q) {
		t.Fatalf("sound key query reclaims keys other sounds still play:\n%s", q)
	}
}

func TestSoundRetireMarksDeletedAndForgetsKeys(t *testing.T) {
	q := squash(soundRetireSQL("= $1"))
	for _, want := range []string{
		"UPDATE audio_tracks SET",
		"source_media_id = NULL", "status = 'deleted'", "audio_key = NULL", "waveform_key = NULL",
		"WHERE source_media_id = $1",
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("sound retire lacks %q: %s", want, q)
		}
	}
}

// An account erasure records the sound keys BEFORE the rows that name them
// are retired, and retires them before the assets go.
func TestPurgeUserReclaimsSoundsBeforeRetiringThem(t *testing.T) {
	record, retire, assets := -1, -1, -1
	for i, st := range purgeUserSteps() {
		sql := squash(st.sql)
		switch {
		case st.table == "audio_tracks" && strings.HasPrefix(sql, "INSERT INTO media_blob_reclaim"):
			if !strings.Contains(sql, "waveform_key AS k") || !strings.Contains(sql, "NOT EXISTS") {
				t.Fatalf("erasure sound reclaim is malformed: %s", sql)
			}
			record = i
		case st.table == "audio_tracks" && strings.HasPrefix(sql, "UPDATE audio_tracks"):
			if !strings.Contains(sql, "status = 'deleted'") || !strings.Contains(sql, "audio_key = NULL") {
				t.Fatalf("erasure sound retire is malformed: %s", sql)
			}
			retire = i
		case st.table == "media_assets" && strings.HasPrefix(sql, "DELETE FROM media_assets"):
			assets = i
		}
	}
	if record < 0 {
		t.Fatal("PurgeUser never records the objects of the user's sounds; they leak in storage")
	}
	if retire < 0 || record > retire || retire > assets {
		t.Fatalf("order must be record (%d) < retire (%d) < assets (%d)", record, retire, assets)
	}
}

// Every path that hard-deletes a video asset reclaims and retires its sound:
// the referrer purge (a post purged after the restore window) and the
// owner's delete. PurgeUser is pinned by its step list above.
func TestEveryAssetDeleteReclaimsItsSound(t *testing.T) {
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
		keys := strings.Index(body, `collectSoundKeys(ctx, tx, soundObjectKeysSQL("= $1")`)
		reclaim := strings.Index(body, "INSERT INTO media_blob_reclaim")
		if keys < 0 || reclaim < 0 || keys > reclaim {
			t.Fatalf("%s: the sound's keys are not collected before the reclaim ledger is written", fn)
		}
		if !regexp.MustCompile(`(keys|objectKeys) = append\((keys|objectKeys), soundKeys\.\.\.\)`).MatchString(body) {
			t.Fatalf("%s: the sound's keys are collected but never reclaimed", fn)
		}
		if !strings.Contains(body, `soundRetireSQL("= $1")`) {
			t.Fatalf("%s: the sound row is not retired", fn)
		}
	}
}
