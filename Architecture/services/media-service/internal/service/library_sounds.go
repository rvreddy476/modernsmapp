package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Library sounds on posts (2026-10-15).
//
// The curated library (audio_library) could be browsed but not used: a post
// links a sound by an audio_tracks id (post-service posts.audio_track_id,
// written by its create, draft and publish paths), and a library id is not
// one. Rather than teach every attach path a second kind of id, a library
// sound becomes an ordinary audio_tracks row on its first use:
//
//	POST /v1/audio-library/:id/use   signed in
//	  200 {data: {"audio_track_id": "<uuid>", "sound": Sound}}
//	  404 NOT_FOUND         no such library row, or it is inactive / withdrawn
//	  422 NOT_STORED        the row's audio is neither a storage key nor https
//	  503 SOUND_UNAVAILABLE the store failed; retry
//
// The client then passes audio_track_id wherever it already passes one.
//
// THE ROW
//
//   - library_id names the library row; source_media_id, source_reel_id and
//     creator_user_id are NULL (the platform owns it); is_public TRUE;
//     status 'ready'; is_original FALSE; license_type 'licensed' for a
//     licensed row; title, artist, genre and duration copied.
//   - audio_key is the library row's audio_url: a storage key in this
//     service's bucket, served exactly like an extracted sound's (a
//     presigned GET behind /v1/audio/:id/serve), or an https URL, which the
//     serve route redirects to. No bytes pass through this process.
//   - One row per library id (uq_audio_tracks_library, migration 028):
//     INSERT … ON CONFLICT DO NOTHING, then re-read, so concurrent first
//     uses converge. An existing row is answered as it stands.
//
// THE AUDIENCE
//
// A library sound has no source video to answer for it. It is the
// platform's, public, and heard by everyone, the signed-out viewer
// included (audio_reads.go admitToTrack). Only an ACTIVE library row is
// materialised or re-answered by /use; a track whose status is no longer
// 'ready' is withdrawn and refused everywhere.
//
// /use counts nothing: a use is a post linking the sound, which
// post-service counts on attach (audio_tracks.use_count / usage_count). The
// library list reads that counter back (GetTrendingAudioLibrary), so the
// library's trending order follows real uses.

var (
	// ErrLibrarySoundNotFound: no such library row, it is inactive, or its
	// sound has been withdrawn.
	ErrLibrarySoundNotFound = errors.New("library sound not found")
	// ErrLibrarySoundNotStored: the row's audio_url is not something this
	// service can deliver (not a storage key, not an https URL).
	ErrLibrarySoundNotStored = errors.New("library sound has no deliverable audio")
	// ErrLibrarySoundUnavailable: the store failed. Retryable.
	ErrLibrarySoundUnavailable = errors.New("library sound is unavailable right now")
)

// librarySoundStore is the store slice /use needs.
type librarySoundStore interface {
	GetAudioLibraryTrack(ctx context.Context, id uuid.UUID) (*postgres.AudioLibraryTrack, error)
	EnsureLibrarySound(ctx context.Context, libraryID uuid.UUID, audioKey string) (*postgres.AudioTrack, error)
}

// LibrarySounds materialises library sounds and signs their previews.
type LibrarySounds struct {
	store librarySoundStore
	blobs recordPresigner
}

// NewLibrarySounds builds the library routes over an explicit store and
// signer; tests pass fakes.
func NewLibrarySounds(store librarySoundStore, blobs recordPresigner) *LibrarySounds {
	return &LibrarySounds{store: store, blobs: blobs}
}

// LibrarySounds is the library routes over the service's own store and
// blob store.
func (s *Service) LibrarySounds() *LibrarySounds {
	return NewLibrarySounds(s.pgStore, s.blobStore)
}

// Use returns the sound of library row libraryID for a signed-in caller,
// materialising it on first use.
func (l *LibrarySounds) Use(ctx context.Context, userID, libraryID uuid.UUID) (*postgres.AudioTrack, error) {
	if userID == uuid.Nil {
		return nil, ErrLibrarySoundNotFound
	}
	if l == nil || l.store == nil {
		return nil, fmt.Errorf("%w: store not configured", ErrLibrarySoundUnavailable)
	}
	lib, err := l.store.GetAudioLibraryTrack(ctx, libraryID)
	if err != nil {
		return nil, fmt.Errorf("%w: load library row: %v", ErrLibrarySoundUnavailable, err)
	}
	if lib == nil || !lib.IsActive {
		return nil, ErrLibrarySoundNotFound
	}
	audio, ok := LibraryAudioSource(lib.AudioURL)
	if !ok {
		return nil, ErrLibrarySoundNotStored
	}
	track, err := l.store.EnsureLibrarySound(ctx, libraryID, audio)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Deactivated between the read and the insert.
			return nil, ErrLibrarySoundNotFound
		}
		return nil, fmt.Errorf("%w: materialise: %v", ErrLibrarySoundUnavailable, err)
	}
	if track == nil || track.Status != "ready" || track.AudioKey == "" {
		return nil, ErrLibrarySoundNotFound
	}
	return track, nil
}

// LibraryAudioSource is what a library row's audio_url is delivered from:
// a storage key in this service's bucket (leading slashes trimmed), or an
// absolute https URL as written. Anything else (another scheme, a relative
// path that climbs, a query or fragment on a key) is not deliverable.
func LibraryAudioSource(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	if strings.Contains(s, "://") || strings.HasPrefix(s, "//") {
		return httpsURL(s)
	}
	key := strings.TrimLeft(s, "/")
	if key == "" || strings.ContainsAny(key, "?#\\") || strings.Contains(key, ":") {
		return "", false
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", false
		}
	}
	return key, true
}

// isLibraryHTTPS reports whether a library sound's audio_key is an https
// URL rather than a storage key.
func isLibraryHTTPS(audioKey string) bool {
	_, ok := httpsURL(audioKey)
	return ok && strings.Contains(audioKey, "://")
}

func httpsURL(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.User != nil {
		return "", false
	}
	return u.String(), true
}

// LibraryServePath is the route that plays a library row's audio before it
// is used: GET /v1/audio-library/:id/serve.
func LibraryServePath(id uuid.UUID) string {
	return "/v1/audio-library/" + id.String() + "/serve"
}

// PresentLibraryTrack is a library row as the read routes answer it: a
// storage key in audio_url is replaced by the serve path, so a key never
// leaves the service and a browser can play the preview; an https URL is
// kept; anything else is answered as "" (nothing to play).
func PresentLibraryTrack(t postgres.AudioLibraryTrack) postgres.AudioLibraryTrack {
	src, ok := LibraryAudioSource(t.AudioURL)
	switch {
	case !ok:
		t.AudioURL = ""
	case strings.Contains(src, "://"):
		t.AudioURL = src
	default:
		t.AudioURL = LibraryServePath(t.ID)
	}
	return t
}

// PreviewURL is GET /v1/audio-library/:id/serve: where an active library
// row's audio is, so a client can preview it before using it. A storage key
// is presigned exactly like every other sound's; an https URL is answered as
// it is. Public: the library is the same for every caller.
// delivery.ErrDeliveryDenied for a missing, inactive or undeliverable row.
func (l *LibrarySounds) PreviewURL(ctx context.Context, libraryID uuid.UUID) (string, error) {
	if l == nil || l.store == nil {
		return "", fmt.Errorf("%w: store not configured", delivery.ErrDeliveryUnresolved)
	}
	lib, err := l.store.GetAudioLibraryTrack(ctx, libraryID)
	if err != nil {
		return "", fmt.Errorf("%w: load library row: %v", delivery.ErrDeliveryUnresolved, err)
	}
	if lib == nil || !lib.IsActive {
		return "", delivery.ErrDeliveryDenied
	}
	src, ok := LibraryAudioSource(lib.AudioURL)
	if !ok {
		return "", delivery.ErrDeliveryDenied
	}
	return signSoundAudio(ctx, l.blobs, src, true)
}

// signSoundAudio turns a sound's audio_key into the URL a client is sent
// to: a storage key presigned for defaultURLExpiry, or, for a library sound
// only (library), an https URL as it is. An extracted sound's key is never
// taken for a URL.
func signSoundAudio(ctx context.Context, blobs recordPresigner, audioKey string, library bool) (string, error) {
	if library && isLibraryHTTPS(audioKey) {
		return audioKey, nil
	}
	if blobs == nil {
		return "", fmt.Errorf("%w: blob store not configured", delivery.ErrDeliveryUnresolved)
	}
	u, err := blobs.GeneratePresignedGetURL(ctx, audioKey, defaultURLExpiry)
	if err != nil {
		return "", fmt.Errorf("%w: sign audio url: %v", delivery.ErrDeliveryUnresolved, err)
	}
	return u.String(), nil
}
