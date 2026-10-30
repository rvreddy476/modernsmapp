package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AudioTrack is a sound: the audio of a source video, extracted once.
//
// audio_tracks is ONE table shared with post-service, which declares its own
// columns on it (use_count, creator_user_id, is_public, ...). Whichever
// service migrated first created it, so every read here COALESCEs what the
// other shape leaves NULL (audioTrackCols) and migration 024 adds the
// post-service columns this service reads.
type AudioTrack struct {
	ID            uuid.UUID  `json:"id"`
	SourceMediaID *uuid.UUID `json:"source_media_id,omitempty"`
	// SourceReelID is the post the sound was taken from. On the wire it is
	// source_post_id, and source_reel_id as before (MarshalJSON).
	SourceReelID *uuid.UUID `json:"source_reel_id,omitempty"`
	Title        string     `json:"title"`
	Artist       string     `json:"artist"`
	Genre        *string    `json:"genre,omitempty"`
	// AudioKey and WaveformKey are storage paths, and the path names the
	// uploader: they never leave the service. The bytes are
	// GET /v1/audio/:audioId/serve.
	AudioKey    string  `json:"-"`
	WaveformKey *string `json:"-"`
	DurationMs  int     `json:"duration_ms"`
	SampleRate  *int    `json:"sample_rate,omitempty"`
	Status      string  `json:"status"`
	IsOriginal  bool    `json:"is_original"`
	LicenseType string  `json:"license_type"`
	// UsageCount is this service's counter and UseCount post-service's
	// (use_count) of the same thing. The wire carries the greater of the two
	// as usage_count (WireUsageCount).
	UsageCount    int        `json:"usage_count"`
	UseCount      int        `json:"-"`
	CreatorUserID *uuid.UUID `json:"creator_user_id"`
	// LibraryID is the audio_library row this sound is (migration 028): a
	// platform sound with no source video and no creator, heard by
	// everyone. Nil on every extracted sound.
	LibraryID *uuid.UUID `json:"library_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	// LibraryCoverURL is the library row's artwork (audio_library.cover_url)
	// of a platform sound; nil on every other sound. On the wire only as
	// cover_url, and only when it is https (CoverURL).
	LibraryCoverURL *string `json:"-"`
	// ReuseBlock says why a platform library sound may no longer be put on
	// a NEW post, from its audio_library row: SoundReuseWithdrawn,
	// SoundReuseInactive or SoundReuseLicenseExpired; "" when it may (and
	// on every extracted sound). Status is untouched by it, so posts that
	// already play the sound keep playing it (reels audit 2026-10-30, 13).
	ReuseBlock string `json:"-"`
}

// Why a library sound is unavailable for new posts (AudioTrack.ReuseBlock,
// unavailable_reason on the wire).
const (
	SoundReuseWithdrawn      = "withdrawn"
	SoundReuseInactive       = "inactive"
	SoundReuseLicenseExpired = "license_expired"
)

// AvailableForNewPosts reports whether a new post may attach or "use" this
// sound: it is ready, and, for a library sound, its library row is active,
// not withdrawn and inside its licence. Playback of a post that already
// carries the sound is NOT decided here (Status is).
func (a AudioTrack) AvailableForNewPosts() bool {
	return a.Status == "ready" && a.ReuseBlock == ""
}

// soundCoverVariant is the still of a source video a sound's cover is: the
// thumbnail every ready video has (cmd/worker), served behind the poster
// question of the media gate.
const soundCoverVariant = "thumb_150"

// CoverURL is the sound's cover (trending sounds, 2026-10-24): a library
// sound's https artwork, or the site path of its source video's thumbnail
// (/v1/media/:id/serve/thumb_150, gated like every media read). nil when
// the sound has neither. The thumbnail is the video's frame, not a custom
// cover its source post may carry.
func (a AudioTrack) CoverURL() *string {
	if a.LibraryID != nil && *a.LibraryID != uuid.Nil {
		if a.LibraryCoverURL != nil && strings.HasPrefix(*a.LibraryCoverURL, "https://") {
			cover := *a.LibraryCoverURL
			return &cover
		}
		return nil
	}
	if a.SourceMediaID != nil && *a.SourceMediaID != uuid.Nil {
		cover := "/v1/media/" + a.SourceMediaID.String() + "/serve/" + soundCoverVariant
		return &cover
	}
	return nil
}

// IsLibrarySound reports whether the row is a platform library sound.
func (a *AudioTrack) IsLibrarySound() bool {
	return a != nil && a.LibraryID != nil && *a.LibraryID != uuid.Nil
}

// WireUsageCount is GREATEST(usage_count, use_count).
func (a AudioTrack) WireUsageCount() int {
	if a.UseCount > a.UsageCount {
		return a.UseCount
	}
	return a.UsageCount
}

// MarshalJSON is the Sound shape: the row, usage_count as the greater of the
// two counters, and source_post_id (null when there is none) beside the
// source_reel_id it has always carried.
func (a AudioTrack) MarshalJSON() ([]byte, error) {
	type row AudioTrack // the fields, without this method
	sound := struct {
		row
		SourcePostID *uuid.UUID `json:"source_post_id"`
		CoverURL     *string    `json:"cover_url"`
		// Reels audit 2026-10-30, 13: whether a new post may use the sound,
		// and why not. Existing posts keep playing it whatever this says.
		AvailableForNewPosts bool   `json:"available_for_new_posts"`
		UnavailableReason    string `json:"unavailable_reason,omitempty"`
	}{row: row(a), SourcePostID: a.SourceReelID, CoverURL: a.CoverURL(),
		AvailableForNewPosts: a.AvailableForNewPosts(), UnavailableReason: a.unavailableReason()}
	sound.UsageCount = a.WireUsageCount()
	return json.Marshal(sound)
}

// unavailableReason is the wire reason a sound may not go on a new post:
// the library block, else "not_ready" for a sound that is not ready.
func (a AudioTrack) unavailableReason() string {
	if a.ReuseBlock != "" {
		return a.ReuseBlock
	}
	if a.Status != "ready" {
		return "not_ready"
	}
	return ""
}

// audioTrackReuseBlock computes AudioTrack.ReuseBlock from the sound's
// library row (NULL library_id, or no row: ""). Withdrawn wins over
// inactive (a withdrawn row is always inactive, CHECK in migration 029);
// an expired licence blocks even a row an admin left active.
const audioTrackReuseBlock = `COALESCE((SELECT CASE
		           WHEN al.withdrawn_at IS NOT NULL THEN '` + SoundReuseWithdrawn + `'
		           WHEN NOT al.is_active THEN '` + SoundReuseInactive + `'
		           WHEN al.license_expires_at IS NOT NULL AND al.license_expires_at <= NOW() THEN '` + SoundReuseLicenseExpired + `'
		           ELSE '' END
		      FROM audio_library al WHERE al.id = library_id), '')`

// audioTrackCols is the one column list of every sound read, in
// scanSound's order. audio_key is NULL on a row post-service wrote, and
// use_count is its column.
const audioTrackCols = `id, source_media_id, source_reel_id, title, artist, genre,
		       COALESCE(audio_key, ''), waveform_key, duration_ms, sample_rate, status, is_original,
		       license_type, COALESCE(usage_count, 0), COALESCE(use_count, 0), creator_user_id,
		       library_id, created_at, updated_at,
		       (SELECT al.cover_url FROM audio_library al WHERE al.id = library_id),
		       ` + audioTrackReuseBlock

// audioTrackUses is the counter the lists order by: the one the wire carries.
const audioTrackUses = `GREATEST(COALESCE(usage_count, 0), COALESCE(use_count, 0))`

func scanSound(row pgx.Row, a *AudioTrack) error {
	return row.Scan(
		&a.ID, &a.SourceMediaID, &a.SourceReelID, &a.Title, &a.Artist, &a.Genre,
		&a.AudioKey, &a.WaveformKey, &a.DurationMs, &a.SampleRate, &a.Status, &a.IsOriginal,
		&a.LicenseType, &a.UsageCount, &a.UseCount, &a.CreatorUserID,
		&a.LibraryID, &a.CreatedAt, &a.UpdatedAt, &a.LibraryCoverURL, &a.ReuseBlock,
	)
}

// InsertSoundIfAbsent inserts the sound of a.SourceMediaID unless that asset
// already has one, and reports whether this call's row is the one kept. The
// unique index of migration 024 is the arbiter, so two callers racing on one
// asset leave one row; the loser re-reads it (GetAudioTrackByMedia).
//
// genre is left to the column default: post-service's shape declares it
// NOT NULL with an empty default, and a NULL written here would break it.
func (s *MediaAssetStore) InsertSoundIfAbsent(ctx context.Context, a *AudioTrack) (bool, error) {
	if a.SourceMediaID == nil || *a.SourceMediaID == uuid.Nil {
		return false, errors.New("a sound needs a source media asset")
	}
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO audio_tracks (id, source_media_id, source_reel_id, creator_user_id, title, artist,
		    audio_key, waveform_key, duration_ms, sample_rate, status, is_original, is_public,
		    license_type, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, TRUE, $13, NOW(), NOW())
		ON CONFLICT (source_media_id) WHERE source_media_id IS NOT NULL DO NOTHING
	`, a.ID, a.SourceMediaID, a.SourceReelID, a.CreatorUserID, a.Title, a.Artist,
		a.AudioKey, a.WaveformKey, a.DurationMs, a.SampleRate, a.Status, a.IsOriginal, a.LicenseType)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// FillSoundOrigin records the source post and the creator of a sound that
// has none yet (a row the owner's extract route wrote before any post named
// it). A value already there is kept: COALESCE, never an overwrite.
func (s *MediaAssetStore) FillSoundOrigin(ctx context.Context, id uuid.UUID, sourcePostID, creatorUserID *uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_tracks
		   SET source_reel_id  = COALESCE(source_reel_id, $2),
		       creator_user_id = COALESCE(creator_user_id, $3),
		       updated_at      = NOW()
		 WHERE id = $1
		   AND (source_reel_id IS NULL OR creator_user_id IS NULL)
	`, id, sourcePostID, creatorUserID)
	return err
}

// GetAudioTrack returns a single audio track by ID.
func (s *MediaAssetStore) GetAudioTrack(ctx context.Context, id uuid.UUID) (*AudioTrack, error) {
	var a AudioTrack
	err := scanSound(s.db.QueryRow(ctx, `
		SELECT `+audioTrackCols+`
		FROM audio_tracks WHERE id = $1
	`, id), &a)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// GetAudioTrackByMedia returns the audio track extracted from a specific
// media asset: the one row the unique index of migration 024 allows.
func (s *MediaAssetStore) GetAudioTrackByMedia(ctx context.Context, mediaID uuid.UUID) (*AudioTrack, error) {
	var a AudioTrack
	err := scanSound(s.db.QueryRow(ctx, `
		SELECT `+audioTrackCols+`
		FROM audio_tracks WHERE source_media_id = $1
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, mediaID), &a)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// UpdateAudioTrackStatus sets the status of an audio track.
func (s *MediaAssetStore) UpdateAudioTrackStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_tracks SET status = $1, updated_at = NOW() WHERE id = $2
	`, status, id)
	return err
}

// UpdateAudioTrackWaveform sets the waveform_key after waveform generation.
func (s *MediaAssetStore) UpdateAudioTrackWaveform(ctx context.Context, id uuid.UUID, waveformKey string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_tracks SET waveform_key = $1, updated_at = NOW() WHERE id = $2
	`, waveformKey, id)
	return err
}

// SearchAudioTracks searches audio tracks by title or artist.
func (s *MediaAssetStore) SearchAudioTracks(ctx context.Context, query string, limit, offset int) ([]AudioTrack, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+audioTrackCols+`
		FROM audio_tracks
		WHERE status = 'ready'
		  AND (title ILIKE '%' || $1 || '%' OR artist ILIKE '%' || $1 || '%')
		ORDER BY `+audioTrackUses+` DESC
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAudioTracks(rows)
}

// IncrementAudioUsageCount counts one use (snapshot — not the truth source).
// Both counters are written to the same value, one more than the greater of
// the two, so a use counted here is not hidden behind post-service's column.
func (s *MediaAssetStore) IncrementAudioUsageCount(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_tracks
		   SET usage_count = `+audioTrackUses+` + 1,
		       use_count   = `+audioTrackUses+` + 1,
		       updated_at  = NOW()
		 WHERE id = $1
	`, id)
	return err
}

func scanAudioTracks(rows pgx.Rows) ([]AudioTrack, error) {
	var tracks []AudioTrack
	for rows.Next() {
		var a AudioTrack
		if err := scanSound(rows, &a); err != nil {
			return nil, err
		}
		tracks = append(tracks, a)
	}
	return tracks, rows.Err()
}

// ─── Audio Library (audio_library table) ───────────────────────────

// AudioLibraryTrack is a track in the curated audio library used by Flicks.
type AudioLibraryTrack struct {
	ID           uuid.UUID       `json:"id"`
	Title        string          `json:"title"`
	Artist       string          `json:"artist"`
	DurationMs   int             `json:"duration_ms"`
	WaveformData json.RawMessage `json:"waveform_data,omitempty"`
	CoverURL     *string         `json:"cover_url,omitempty"`
	// AudioURL is the row's audio: an https URL, or a storage key in this
	// service's bucket. The read routes answer a storage key as the
	// library serve path instead (service.PresentLibraryTrack), so a key
	// never reaches a client.
	AudioURL     string     `json:"audio_url"`
	Source       string     `json:"source"`
	SourcePostID *uuid.UUID `json:"source_post_id,omitempty"`
	SourceUserID *uuid.UUID `json:"source_user_id,omitempty"`
	// UsageCount is the greater of the row's own counter and the uses of
	// its materialised sound (audio_tracks.usage_count / use_count), which
	// is where post-service counts an attach.
	UsageCount int64   `json:"usage_count"`
	IsTrending bool    `json:"is_trending"`
	IsLicensed bool    `json:"is_licensed"`
	Language   string  `json:"language"`
	Genre      *string `json:"genre,omitempty"`
	Mood       *string `json:"mood,omitempty"`
	IsActive   bool    `json:"is_active"`
	// AudioTrackID is the sound this row was materialised as on its first
	// use (POST /v1/audio-library/:id/use); null until then.
	AudioTrackID *uuid.UUID `json:"audio_track_id"`
	CreatedAt    time.Time  `json:"created_at"`
}

// audioLibraryUses is a row's use count: its own counter, or its
// materialised sound's, whichever is greater.
const audioLibraryUses = `GREATEST(l.usage_count, COALESCE(t.usage_count, 0)::BIGINT, COALESCE(t.use_count, 0)::BIGINT)`

const audioLibrarySelectCols = `l.id, l.title, l.artist, l.duration_ms, l.waveform_data, l.cover_url, l.audio_url,
    l.source, l.source_post_id, l.source_user_id, ` + audioLibraryUses + `, l.is_trending, l.is_licensed,
    COALESCE(l.language, ''), l.genre, l.mood, l.is_active, t.id, l.created_at`

// audioLibraryLicensed keeps library rows inside their licence: a sound
// whose license_expires_at has passed is not offered, previewed or
// materialised for a new post (reels audit 2026-10-30, 13), even while an
// admin has not yet switched it off.
const audioLibraryLicensed = `(l.license_expires_at IS NULL OR l.license_expires_at > NOW())`

// audioLibraryFrom joins each row to its materialised sound (at most one:
// uq_audio_tracks_library). A withdrawn sound is not joined.
const audioLibraryFrom = ` FROM audio_library l
    LEFT JOIN audio_tracks t ON t.library_id = l.id AND t.status = 'ready'`

func scanAudioLibraryTrack(row pgx.Row, t *AudioLibraryTrack) error {
	return row.Scan(
		&t.ID, &t.Title, &t.Artist, &t.DurationMs, &t.WaveformData, &t.CoverURL, &t.AudioURL,
		&t.Source, &t.SourcePostID, &t.SourceUserID, &t.UsageCount, &t.IsTrending, &t.IsLicensed,
		&t.Language, &t.Genre, &t.Mood, &t.IsActive, &t.AudioTrackID, &t.CreatedAt,
	)
}

func scanAudioLibraryRows(rows pgx.Rows) ([]AudioLibraryTrack, error) {
	var tracks []AudioLibraryTrack
	for rows.Next() {
		var t AudioLibraryTrack
		if err := scanAudioLibraryTrack(rows, &t); err != nil {
			return nil, err
		}
		tracks = append(tracks, t)
	}
	return tracks, rows.Err()
}

// GetAudioLibraryTrack returns a single active track from audio_library;
// (nil, nil) when there is none or it is inactive.
func (s *MediaAssetStore) GetAudioLibraryTrack(ctx context.Context, id uuid.UUID) (*AudioLibraryTrack, error) {
	t := &AudioLibraryTrack{}
	err := scanAudioLibraryTrack(
		s.db.QueryRow(ctx, `SELECT `+audioLibrarySelectCols+audioLibraryFrom+` WHERE l.id = $1 AND l.is_active = TRUE AND `+audioLibraryLicensed, id),
		t,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// CreateAudioLibraryTrack inserts a new audio_library record.
func (s *MediaAssetStore) CreateAudioLibraryTrack(ctx context.Context, t *AudioLibraryTrack) (*AudioLibraryTrack, error) {
	err := scanAudioLibraryTrack(
		s.db.QueryRow(ctx, `
			INSERT INTO audio_library (title, artist, duration_ms, waveform_data, cover_url, audio_url,
			    source, source_post_id, source_user_id, is_licensed, language, genre, mood)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			RETURNING id, title, artist, duration_ms, waveform_data, cover_url, audio_url,
			    source, source_post_id, source_user_id, usage_count, is_trending, is_licensed,
			    COALESCE(language, ''), genre, mood, is_active, NULL::uuid, created_at`,
			t.Title, t.Artist, t.DurationMs, t.WaveformData, t.CoverURL, t.AudioURL,
			t.Source, t.SourcePostID, t.SourceUserID, t.IsLicensed, t.Language, t.Genre, t.Mood,
		),
		t,
	)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// GetTrendingAudioLibrary returns tracks ordered by use, optionally filtered by genre.
func (s *MediaAssetStore) GetTrendingAudioLibrary(ctx context.Context, genre *string, limit, offset int) ([]AudioLibraryTrack, error) {
	var rows pgx.Rows
	var err error
	if genre != nil && *genre != "" {
		rows, err = s.db.Query(ctx, `
			SELECT `+audioLibrarySelectCols+audioLibraryFrom+`
			WHERE l.is_active = TRUE AND `+audioLibraryLicensed+` AND l.genre = $3
			ORDER BY `+audioLibraryUses+` DESC, l.created_at DESC, l.id LIMIT $1 OFFSET $2`,
			limit, offset, *genre)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT `+audioLibrarySelectCols+audioLibraryFrom+`
			WHERE l.is_active = TRUE AND `+audioLibraryLicensed+`
			ORDER BY `+audioLibraryUses+` DESC, l.created_at DESC, l.id LIMIT $1 OFFSET $2`,
			limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAudioLibraryRows(rows)
}

// SearchAudioLibrary searches audio_library by title or artist ILIKE.
func (s *MediaAssetStore) SearchAudioLibrary(ctx context.Context, query string, limit, offset int) ([]AudioLibraryTrack, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+audioLibrarySelectCols+audioLibraryFrom+`
		WHERE l.is_active = TRUE AND `+audioLibraryLicensed+` AND (l.title ILIKE $3 OR l.artist ILIKE $3)
		ORDER BY `+audioLibraryUses+` DESC, l.id LIMIT $1 OFFSET $2`,
		limit, offset, "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAudioLibraryRows(rows)
}

// EnsureLibrarySound returns the audio_tracks row of library sound
// libraryID, materialising it on the first call. audioKey is the row's
// audio (a storage key, or an https URL), already checked by the caller.
//
// The insert copies the library row's metadata, and only while the row is
// active. The unique index of migration 028 is the arbiter, so concurrent
// first uses leave one row and every caller re-reads it. A row that already
// exists is answered as it stands, never rewritten.
//
// pgx.ErrNoRows: the library row is not active and was never materialised.
func (s *MediaAssetStore) EnsureLibrarySound(ctx context.Context, libraryID uuid.UUID, audioKey string) (*AudioTrack, error) {
	// genre: post-service's shape declares it NOT NULL with an empty
	// default, so a library row with no genre writes ''.
	if _, err := s.db.Exec(ctx, `
		INSERT INTO audio_tracks (id, library_id, source_media_id, source_reel_id, creator_user_id,
		    title, artist, genre, audio_key, duration_ms, status, is_original, is_public,
		    license_type, created_at, updated_at)
		SELECT $1, l.id, NULL, NULL, NULL,
		       l.title, l.artist, COALESCE(l.genre, ''), $3, l.duration_ms, 'ready', FALSE, TRUE,
		       CASE WHEN l.is_licensed THEN 'licensed' ELSE 'standard' END, NOW(), NOW()
		  FROM audio_library l
		 WHERE l.id = $2 AND l.is_active = TRUE AND l.withdrawn_at IS NULL
		   AND (l.license_expires_at IS NULL OR l.license_expires_at > NOW())
		ON CONFLICT (library_id) WHERE library_id IS NOT NULL DO NOTHING
	`, uuid.New(), libraryID, audioKey); err != nil {
		return nil, err
	}
	var a AudioTrack
	if err := scanSound(s.db.QueryRow(ctx, `
		SELECT `+audioTrackCols+`
		FROM audio_tracks WHERE library_id = $1
	`, libraryID), &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// IncrementAudioLibraryUsage bumps usage_count by 1 for a library track.
func (s *MediaAssetStore) IncrementAudioLibraryUsage(ctx context.Context, audioID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE audio_library SET usage_count = usage_count + 1 WHERE id = $1`, audioID)
	return err
}

// AddPostAudioRef records that a post uses a library audio track (idempotent).
func (s *MediaAssetStore) AddPostAudioRef(ctx context.Context, audioID, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO post_audio_refs (audio_id, post_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		audioID, postID)
	return err
}
