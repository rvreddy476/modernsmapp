package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/google/uuid"
)

/*
	Original sounds on reels (2026-09-29).

	A reel can play the audio of another creator's reel. The video and the
	sound stay separate: the player plays both in sync, and nothing is mixed
	on the server. So a sound follows its source video's audience at every
	read: when the source goes private or is taken down, the sound stops.

	Three things live here.

	  * `sound` on a post (attachSounds). Every read that returns a post
	    carries audio_track_id and audio_start_ms from the row; the sound
	    itself is added per viewer, by this service's own media-access
	    decision on the sound's media, the signed-out viewer included. A
	    viewer who may not hear it gets no `sound`, and neither does anyone
	    when the decision cannot be made: the reel then plays with its own
	    audio, and the read still succeeds. It is never a reason to fail a
	    feed page. The cached post body holds no `sound`: it is attached
	    after the cache read, to the detail and not to the post.

	  * "Use this sound" (UseSound, POST /v1/posts/:postId/sound): the sound
	    a reel plays, made on first use by media-service from the reel's own
	    video.

	  * The reels that play a sound (PostsBySound,
	    GET /v1/posts/by-sound/:soundId), behind the same author gates as the
	    other public listings.

	No storage path and no presigned URL is ever part of a PostSound: the
	bytes are media-service's to serve, re-gated on every request.

	A platform library sound (library_id, 2026-10-15) is the exception to
	"follows its source video": it has none. media-service materialised it
	from the curated library on first use; it is public and every viewer,
	signed out included, may hear and reuse it while it is ready.

	Reuse needs the creator's consent (soundReuseAllowed, audio.go); hearing
	does not. A reel that already plays a sound keeps playing it after its
	source turns reuse off.
*/

const (
	// MaxSoundSourceMs is the longest video a sound is taken from.
	MaxSoundSourceMs = 600 * 1000 // ten minutes (founder, 2026-10-21; was five)
	// SoundUseLimitPerHour bounds how many sounds one account may ask
	// media-service to make in an hour.
	SoundUseLimitPerHour = 30

	// soundMakeTimeout bounds one call to media-service's sound route. It
	// runs ffmpeg over a rendition of up to ten minutes on first use, so
	// it is well above the 5 s of the shared client.
	soundMakeTimeout = 30 * time.Second

	soundTitlePrefix  = "Original sound"
	soundTitleMaxLen  = 120
	soundArtistMaxLen = 80
)

var (
	// ErrSoundNotFound: no such sound, or one this viewer may not hear. One
	// error for both, so the route cannot be used to test an id.
	ErrSoundNotFound = errors.New("sound not found")
	// ErrSoundUnavailable: an authority could not answer (the store, the
	// audience decision, media-service). Never read as "allowed".
	ErrSoundUnavailable = errors.New("sound is unavailable right now")
	// ErrSoundRateLimited: the account has made too many sounds this hour.
	ErrSoundRateLimited = errors.New("too many sounds requested")
	// ErrInvalidSoundCursor: the cursor is not one the listing issued.
	ErrInvalidSoundCursor = errors.New("cursor is not a value this endpoint issued")
)

// Refusal codes of "use this sound". The three 422s are media-service's own
// (its sound route answers the same ones), so a refusal reads the same
// whichever service made it.
const (
	SoundCodeNotAReel = "NOT_A_REEL"
	SoundCodeNotReady = "NOT_READY"
	SoundCodeTooLong  = "TOO_LONG"
)

// SoundRefusal is a 422 of "use this sound": the post is not a sound source.
type SoundRefusal struct {
	Code string
}

func (e *SoundRefusal) Error() string { return "sound refused: " + e.Code }

// Message is the text for the wire; it never carries another service's.
func (e *SoundRefusal) Message() string {
	switch e.Code {
	case SoundCodeNotAReel, "NOT_A_VIDEO":
		return "Only a short video has a sound to use"
	case SoundCodeNotReady:
		return "The video is not ready yet"
	case SoundCodeTooLong:
		return "The video is too long to take a sound from"
	}
	return "This video has no sound to use"
}

// PostSound is the sound a post plays, as a viewer who may hear it gets it.
type PostSound struct {
	ID         uuid.UUID `json:"id"`
	Title      string    `json:"title"`
	Artist     string    `json:"artist"`
	DurationMs int       `json:"duration_ms"`
	// StartMs is where in the sound this post starts playing it; 0 on a
	// sound that is not yet on a post.
	StartMs  int `json:"start_ms"`
	UseCount int `json:"use_count"`
	// SourcePostID is the reel the sound was taken from; CreatorUserID its
	// author. Both null on a sound that names neither.
	SourcePostID  *uuid.UUID `json:"source_post_id"`
	CreatorUserID *uuid.UUID `json:"creator_user_id"`
	// LibraryID is set on a platform library sound only (the curated
	// library row it was made from); absent on every other sound.
	LibraryID *uuid.UUID `json:"library_id,omitempty"`
}

// SoundTrending is the sound page's trending badge (trending sounds,
// 2026-10-24): the sound's place in media-service's stored ranking.
type SoundTrending struct {
	Rank   int  `json:"rank"`
	Rising bool `json:"rising"`
}

// SoundExtras is what media-service adds to the sound page's sound
// (GET /v1/media/internal/sounds/:id/extras).
type SoundExtras struct {
	Trending *SoundTrending `json:"trending"`
	CoverURL *string        `json:"cover_url"`
}

// PageSound is the sound at the top of the sound page: the PostSound, plus
// its cover and its trending badge (each null when it has none, or when
// media-service could not say).
type PageSound struct {
	*PostSound
	CoverURL *string        `json:"cover_url"`
	Trending *SoundTrending `json:"trending"`
}

// SoundPage is one page of GET /v1/posts/by-sound/:soundId.
type SoundPage struct {
	Sound *PageSound `json:"sound"`
	// Origin is the reel the sound was taken from: on the first page only,
	// and only when this viewer may open it. Never repeated in Items.
	Origin *PostDetail  `json:"origin"`
	Items  []PostDetail `json:"items"`
}

// soundStore is the storage slice the sound reads need.
type soundStore interface {
	GetAudioTracksByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.AudioTrack, error)
	SoundSourcePosts(ctx context.Context, sourcePostID, mediaID *uuid.UUID) ([]postgres.SoundSourcePost, error)
	GetPost(ctx context.Context, id uuid.UUID) (*postgres.Post, error)
	ListPostsBySound(ctx context.Context, soundID uuid.UUID, exclude *uuid.UUID, limit int, cursor string) ([]postgres.Post, string, error)
	libraryReuseReader
}

// sounds is the store behind the sound reads; nil when there is none, and
// every caller then answers as an authority that cannot.
func (s *Service) sounds() soundStore {
	if s.soundReads != nil {
		return s.soundReads
	}
	if s.pgStore != nil {
		return s.pgStore
	}
	return nil
}

// SoundRequest is the body of media-service's sound route.
type SoundRequest struct {
	Title         string    `json:"title"`
	Artist        string    `json:"artist"`
	SourcePostID  uuid.UUID `json:"source_post_id"`
	CreatorUserID uuid.UUID `json:"creator_user_id"`
}

// MediaSound is the part of media-service's Sound this service reads.
type MediaSound struct {
	ID            uuid.UUID  `json:"id"`
	Title         string     `json:"title"`
	Artist        string     `json:"artist"`
	DurationMs    int        `json:"duration_ms"`
	Status        string     `json:"status"`
	UsageCount    int        `json:"usage_count"`
	SourcePostID  *uuid.UUID `json:"source_post_id"`
	CreatorUserID *uuid.UUID `json:"creator_user_id"`
	// AvailableForNewPosts and UnavailableReason are media-service's
	// new-use verdict (reels audit 2026-10-30). Absent (an older
	// media-service) reads as available; Status still has to be ready.
	AvailableForNewPosts *bool  `json:"available_for_new_posts"`
	UnavailableReason    string `json:"unavailable_reason"`
}

// soundSource makes sure a video has its one sound and returns it.
// Production is the HTTP client below; tests substitute a fake. Its errors
// are a *SoundRefusal (media-service's 422) or wrap ErrSoundUnavailable.
type soundSource interface {
	EnsureSound(ctx context.Context, mediaID uuid.UUID, in SoundRequest) (*MediaSound, error)
}

func (s *Service) soundMakerOrHTTP() soundSource {
	if s.soundMaker != nil {
		return s.soundMaker
	}
	return httpSoundSource{svc: s}
}

// playableSound reports whether a sound row can be played at all: it
// exists, it is ready, and it names the media that answers for its
// audience, or it is a platform library sound, which needs none.
func playableSound(t *postgres.AudioTrack) bool {
	if t == nil || t.Status != audioTrackReady {
		return false
	}
	return t.IsLibrarySound() || (t.MediaID != nil && *t.MediaID != uuid.Nil)
}

// needsAudience reports whether a playable sound's hearing is decided on
// its media. A library sound's is not: everyone may hear it.
func needsAudience(t *postgres.AudioTrack) bool {
	return !t.IsLibrarySound()
}

// reusableSound adds the private-sound rule to playableSound: a private
// sound (is_public = false) is its creator's alone (M10).
func reusableSound(t *postgres.AudioTrack, viewerID *uuid.UUID) bool {
	if !playableSound(t) {
		return false
	}
	if t.IsPublic {
		return true
	}
	return viewerID != nil && t.CreatorUserID != nil && *t.CreatorUserID == *viewerID
}

func postSoundOf(t *postgres.AudioTrack, startMs int) *PostSound {
	return &PostSound{
		ID: t.ID, Title: t.Title, Artist: t.Artist, DurationMs: t.DurationMs,
		StartMs: clampSoundStart(startMs, t.DurationMs), UseCount: t.UseCount,
		SourcePostID: t.SourcePostID, CreatorUserID: t.CreatorUserID,
		LibraryID: t.LibraryID,
	}
}

// mayHear is the media-access decision for one viewer over the media of a
// page of sounds; a nil viewer is the signed-out one. soundAudience replaces
// it in tests. A media id absent from the answer is not allowed.
func (s *Service) mayHear(ctx context.Context, viewerID *uuid.UUID, mediaIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	viewer := uuid.Nil
	if viewerID != nil {
		viewer = *viewerID
	}
	if s.soundAudience != nil {
		return s.soundAudience(ctx, viewer, mediaIDs)
	}
	results, err := s.ViewerMayAccessMediaBatch(ctx, viewer, mediaIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(results))
	for id, res := range results {
		out[id] = res.Allowed
	}
	return out, nil
}

// attachSounds sets Sound on every detail whose post plays an added sound
// this viewer may hear, with one read of the sounds and one audience
// decision for the whole page. It returns nothing on purpose: a sound that
// cannot be read or decided is left off, and the page goes out.
func (s *Service) attachSounds(ctx context.Context, viewerID *uuid.UUID, details []*PostDetail) {
	var soundIDs []uuid.UUID
	seen := make(map[uuid.UUID]bool)
	for _, d := range details {
		if d == nil || d.Post == nil {
			continue
		}
		// Whatever arrived with the row is not a decision for this viewer.
		d.Sound = nil
		if d.Post.AudioTrackID == nil || seen[*d.Post.AudioTrackID] {
			continue
		}
		seen[*d.Post.AudioTrackID] = true
		soundIDs = append(soundIDs, *d.Post.AudioTrackID)
	}
	if len(soundIDs) == 0 {
		return
	}
	store := s.sounds()
	if store == nil {
		slog.WarnContext(ctx, "sounds omitted: no store to read them from", "sounds", len(soundIDs))
		return
	}
	tracks, err := store.GetAudioTracksByIDs(ctx, soundIDs)
	if err != nil {
		slog.WarnContext(ctx, "sounds omitted: read failed", "sounds", len(soundIDs), "err", err)
		return
	}
	var mediaIDs []uuid.UUID
	seenMedia := make(map[uuid.UUID]bool)
	for _, t := range tracks {
		if playableSound(t) && needsAudience(t) && !seenMedia[*t.MediaID] {
			seenMedia[*t.MediaID] = true
			mediaIDs = append(mediaIDs, *t.MediaID)
		}
	}
	// Library sounds need no decision; a page of only those asks nobody.
	allowed := map[uuid.UUID]bool{}
	if len(mediaIDs) > 0 {
		allowed, err = s.mayHear(ctx, viewerID, mediaIDs)
		if err != nil {
			// The library sounds on the page are still heard: their
			// hearing never depended on this answer.
			slog.WarnContext(ctx, "sounds omitted: audience unresolved", "sounds", len(soundIDs), "err", err)
			allowed = map[uuid.UUID]bool{}
		}
	}
	for _, d := range details {
		if d == nil || d.Post == nil || d.Post.AudioTrackID == nil {
			continue
		}
		t := tracks[*d.Post.AudioTrackID]
		if !playableSound(t) || (needsAudience(t) && !allowed[*t.MediaID]) {
			continue
		}
		startMs := 0
		if d.Post.AudioStartMs != nil {
			startMs = *d.Post.AudioStartMs
		}
		d.Sound = postSoundOf(t, startMs)
	}
}

// soundForViewer reads one sound for a viewer who wants to reuse it, or
// list what plays it: ErrSoundNotFound when there is none or it is not
// theirs to hear, ErrSoundUnavailable when that could not be established.
func (s *Service) soundForViewer(ctx context.Context, viewerID *uuid.UUID, soundID uuid.UUID) (*postgres.AudioTrack, error) {
	store := s.sounds()
	if store == nil {
		return nil, fmt.Errorf("%w: no store", ErrSoundUnavailable)
	}
	tracks, err := store.GetAudioTracksByIDs(ctx, []uuid.UUID{soundID})
	if err != nil {
		return nil, fmt.Errorf("%w: read: %v", ErrSoundUnavailable, err)
	}
	track := tracks[soundID]
	if !reusableSound(track, viewerID) {
		return nil, ErrSoundNotFound
	}
	if !needsAudience(track) {
		// A platform library sound: public, heard by everyone.
		return track, nil
	}
	allowed, err := s.mayHear(ctx, viewerID, []uuid.UUID{*track.MediaID})
	if err != nil {
		return nil, fmt.Errorf("%w: audience: %v", ErrSoundUnavailable, err)
	}
	if !allowed[*track.MediaID] {
		return nil, ErrSoundNotFound
	}
	return track, nil
}

// primaryVideo is the first video a post carries, in carousel order.
func primaryVideo(p *postgres.Post) *postgres.PostMedia {
	for i := range p.Media {
		if p.Media[i].Kind == "video" {
			return &p.Media[i]
		}
	}
	return nil
}

// isSoundSourceType: only short-form video is a sound source.
func isSoundSourceType(contentType string) bool {
	switch normalizeLegacyContentType(strings.ToLower(strings.TrimSpace(contentType))) {
	case "flick":
		return true
	}
	return false
}

// UseSound is "use this sound" on a reel: the sound the reel plays, made on
// first use.
//
//  1. The viewer must be able to open the post, by the direct read's gate;
//     a refusal is that gate's (ErrPostNotVisible, or an age refusal).
//  2. A reel that already plays an added sound the viewer may reuse answers
//     that sound. media-service is not called.
//  3. Otherwise the post must be a short video whose primary video is ready
//     and at most MaxSoundSourceMs long (*SoundRefusal).
//  4. Consent: a reel whose remix_setting is 'disallow' gives its sound to
//     its author only (ErrSoundReuseNotAllowed).
//  5. media-service makes the sound, or returns the one it already made.
func (s *Service) UseSound(ctx context.Context, viewerID, postID uuid.UUID) (*PostSound, error) {
	if err := s.singlePostRead(ctx, postID, &viewerID); err != nil {
		return nil, err
	}
	store := s.sounds()
	if store == nil {
		return nil, fmt.Errorf("%w: no store", ErrSoundUnavailable)
	}
	p, err := store.GetPost(ctx, postID)
	if err != nil {
		return nil, fmt.Errorf("%w: read post: %v", ErrSoundUnavailable, err)
	}
	if p == nil {
		return nil, ErrPostNotVisible
	}
	if err := s.attachMediaState(ctx, []*postgres.Post{p}); err != nil {
		return nil, fmt.Errorf("%w: media state: %v", ErrSoundUnavailable, err)
	}
	if hiddenFromViewer(p, &viewerID) {
		return nil, ErrPostNotVisible
	}

	if p.AudioTrackID != nil {
		track, err := s.soundForViewer(ctx, &viewerID, *p.AudioTrackID)
		switch {
		case err == nil:
			// A library sound withdrawn, switched off or out of licence is
			// refused, not swapped for the reel's own audio: the viewer
			// asked for this sound.
			if err := newSoundUseRefusal(ctx, store, track); err != nil {
				return nil, err
			}
			sources, err := store.SoundSourcePosts(ctx, track.SourcePostID, track.MediaID)
			if err != nil {
				return nil, fmt.Errorf("%w: consent: %v", ErrSoundUnavailable, err)
			}
			if !soundReuseAllowed(sources, viewerID) {
				return nil, ErrSoundReuseNotAllowed
			}
			return postSoundOf(track, 0), nil
		case errors.Is(err, ErrSoundNotFound):
			// Not theirs to hear: what they may use is the reel's own audio.
		default:
			return nil, err
		}
	}

	video := primaryVideo(p)
	if !isSoundSourceType(p.ContentType) || video == nil {
		return nil, &SoundRefusal{Code: SoundCodeNotAReel}
	}
	if video.ProcessingStatus != mediaReady || video.ModerationStatus == "rejected" {
		return nil, &SoundRefusal{Code: SoundCodeNotReady}
	}
	if video.DurationMs > MaxSoundSourceMs {
		return nil, &SoundRefusal{Code: SoundCodeTooLong}
	}
	if strings.EqualFold(strings.TrimSpace(p.RemixSetting), remixDisallow) && viewerID != p.AuthorID {
		return nil, ErrSoundReuseNotAllowed
	}
	if !s.soundUseAllowed(ctx, viewerID) {
		return nil, ErrSoundRateLimited
	}

	title, artist := soundNames(s.soundArtistName(ctx, viewerID, p.AuthorID))
	made, err := s.soundMakerOrHTTP().EnsureSound(ctx, video.MediaID, SoundRequest{
		Title: title, Artist: artist, SourcePostID: p.ID, CreatorUserID: p.AuthorID,
	})
	if err != nil {
		return nil, err
	}
	if made == nil || made.ID == uuid.Nil {
		return nil, fmt.Errorf("%w: media-service answered no sound", ErrSoundUnavailable)
	}
	if made.Status != "" && made.Status != audioTrackReady {
		return nil, &SoundRefusal{Code: SoundCodeNotReady}
	}
	if made.AvailableForNewPosts != nil && !*made.AvailableForNewPosts {
		reason := made.UnavailableReason
		if reason == "" {
			reason = "unavailable"
		}
		return nil, &SoundUnavailableError{Reason: reason}
	}
	return &PostSound{
		ID: made.ID, Title: made.Title, Artist: made.Artist, DurationMs: made.DurationMs,
		StartMs: 0, UseCount: made.UsageCount,
		SourcePostID: made.SourcePostID, CreatorUserID: made.CreatorUserID,
	}, nil
}

// soundUseAllowed is the per-user limit on making a sound; soundLimiter
// replaces it in tests. Without Redis there is no limiter to ask.
func (s *Service) soundUseAllowed(ctx context.Context, userID uuid.UUID) bool {
	if s.soundLimiter != nil {
		return s.soundLimiter(ctx, userID)
	}
	if s.rdb == nil || s.rateLimiter == nil {
		return true
	}
	return s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:sound_use:%s", userID), SoundUseLimitPerHour, time.Hour)
}

// soundArtistName is the name a reel's sound is credited to: the author's
// channel name, else their display name, else their username. Empty when
// none can be read; the sound is then plain "Original sound".
func (s *Service) soundArtistName(ctx context.Context, viewerID, authorID uuid.UUID) string {
	if s.channels != nil {
		if ch, err := s.channels.GetChannelByUserID(ctx, authorID); err == nil && ch != nil {
			if name := strings.TrimSpace(ch.Name); name != "" {
				return name
			}
		}
	}
	if s.profileServiceURL == "" {
		return ""
	}
	profiles, err := s.fetchCommentProfiles(ctx, &viewerID, []string{authorID.String()})
	if err != nil {
		slog.DebugContext(ctx, "sound artist: profile unavailable", "author_id", authorID, "err", err)
		return ""
	}
	if name := strings.TrimSpace(profiles[authorID].DisplayName); name != "" {
		return name
	}
	return strings.TrimSpace(profiles[authorID].Username)
}

// soundNames builds the title and artist of an original sound, inside
// media-service's limits (120 and 80 runes).
func soundNames(name string) (title, artist string) {
	artist = truncateRunes(strings.TrimSpace(name), soundArtistMaxLen)
	if artist == "" {
		return soundTitlePrefix, ""
	}
	return truncateRunes(soundTitlePrefix+" - "+artist, soundTitleMaxLen), artist
}

func truncateRunes(v string, max int) string {
	if utf8.RuneCountInString(v) <= max {
		return v
	}
	return strings.TrimSpace(string([]rune(v)[:max]))
}

// NormalizeSoundPostsLimit resolves ?limit=: absent, unreadable or below 1
// is the default, anything above the ceiling is the ceiling.
func NormalizeSoundPostsLimit(limit int) int {
	if limit <= 0 {
		return postgres.DefaultSoundPostsLimit
	}
	if limit > postgres.MaxSoundPostsLimit {
		return postgres.MaxSoundPostsLimit
	}
	return limit
}

// ValidSoundPostsCursor reports whether cursor is one the listing issued.
func ValidSoundPostsCursor(cursor string) bool {
	_, _, err := postgres.ParseSoundPostsCursor(cursor)
	return err == nil
}

// PostsBySound is the page of reels that play a sound, newest first.
//
// The sound must be one the viewer may hear; otherwise the answer is
// ErrSoundNotFound, the same as for a sound that does not exist. The rows
// are public, live, approved short videos (the store applies that), behind
// the same fail-closed author gate as the other public listings
// (canViewPosts: private accounts, blocks, hidden authors), hydrated like
// them.
func (s *Service) PostsBySound(ctx context.Context, viewerID *uuid.UUID, soundID uuid.UUID, limit int, cursor string) (*SoundPage, string, error) {
	if cursor != "" && !ValidSoundPostsCursor(cursor) {
		return nil, "", ErrInvalidSoundCursor
	}
	track, err := s.soundForViewer(ctx, viewerID, soundID)
	if err != nil {
		return nil, "", err
	}
	posts, nextCursor, err := s.sounds().ListPostsBySound(ctx, soundID, track.SourcePostID, NormalizeSoundPostsLimit(limit), cursor)
	if err != nil {
		return nil, "", fmt.Errorf("%w: list: %v", ErrSoundUnavailable, err)
	}
	items, err := s.hydrateSoundPosts(ctx, viewerID, posts)
	if err != nil {
		return nil, "", err
	}
	extras := s.soundPageExtras(ctx, soundID)
	page := &SoundPage{
		Sound: &PageSound{PostSound: postSoundOf(track, 0), CoverURL: extras.CoverURL, Trending: extras.Trending},
		Items: items,
	}
	if cursor == "" && track.SourcePostID != nil {
		page.Origin = s.soundOrigin(ctx, viewerID, *track.SourcePostID)
	}
	return page, nextCursor, nil
}

// soundExtrasTimeout bounds the badge-and-cover read: a decoration, so a
// slow media-service costs it, not the page.
const soundExtrasTimeout = 2 * time.Second

// soundPageExtras is the sound page's cover and trending badge from
// media-service. Best-effort: a fault is logged and the page carries both
// as null. The caller has already decided the viewer may hear the sound.
func (s *Service) soundPageExtras(ctx context.Context, soundID uuid.UUID) SoundExtras {
	var (
		extras *SoundExtras
		err    error
	)
	if s.soundExtrasSeam != nil {
		extras, err = s.soundExtrasSeam(ctx, soundID)
	} else {
		extras, err = s.fetchSoundExtras(ctx, soundID)
	}
	if err != nil || extras == nil {
		if err != nil {
			slog.Warn("sound page: cover and trending unavailable", "sound_id", soundID, "error", err)
		}
		return SoundExtras{}
	}
	return *extras
}

func (s *Service) fetchSoundExtras(ctx context.Context, soundID uuid.UUID) (*SoundExtras, error) {
	if s.mediaServiceURL == "" {
		return nil, errors.New("media-service not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, soundExtrasTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.mediaServiceURL, "/")+"/v1/media/internal/sounds/"+soundID.String()+"/extras", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("media-service sound extras returned %d", resp.StatusCode)
	}
	var envelope struct {
		Data *SoundExtras `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("media-service sound extras body: %w", err)
	}
	if envelope.Data == nil {
		return nil, errors.New("media-service sound extras answered without data")
	}
	return envelope.Data, nil
}

// hydrateSoundPosts turns listed rows into the details every listing
// answers: the author gate, the counts, the live media state (which drops
// what this viewer may not see yet, and attaches each row's sound), the
// view count and the channel.
func (s *Service) hydrateSoundPosts(ctx context.Context, viewerID *uuid.UUID, posts []postgres.Post) ([]PostDetail, error) {
	authorSet := make(map[uuid.UUID]struct{}, len(posts))
	authorIDs := make([]uuid.UUID, 0, len(posts))
	postIDs := make([]uuid.UUID, 0, len(posts))
	for _, p := range posts {
		postIDs = append(postIDs, p.ID)
		if viewerID != nil && *viewerID == p.AuthorID {
			continue
		}
		if _, ok := authorSet[p.AuthorID]; !ok {
			authorSet[p.AuthorID] = struct{}{}
			authorIDs = append(authorIDs, p.AuthorID)
		}
	}
	viewableAuthor := s.canViewPosts(ctx, viewerID, authorIDs)
	counts := s.countsForSoundPosts(ctx, postIDs)

	details := make([]PostDetail, 0, len(posts))
	for _, p := range posts {
		if (viewerID == nil || *viewerID != p.AuthorID) && !viewableAuthor[p.AuthorID] {
			continue
		}
		post := p
		c := counts[p.ID]
		if c == nil {
			c = &scylla.Counts{}
		}
		details = append(details, PostDetail{Post: &post, Counts: c})
	}
	details, err := s.attachMediaStateToDetails(ctx, details, viewerID)
	if err != nil {
		return nil, err
	}
	// Members-only rows: redacted for this viewer (members_read.go). The
	// sound page and the origin card both come through here.
	s.redactDetailsForViewer(ctx, viewerID, details)
	// The view counts in one batched call for the page (at most 51 ids, one
	// analytics request under viewCountTimeout), never one call per reel:
	// a cold page of 50 used to make 50 sequential calls of up to 1.5 s.
	ids := make([]uuid.UUID, len(details))
	for i := range details {
		ids[i] = details[i].Post.ID
	}
	views := s.fetchViewCounts(ctx, ids)
	ptrs := make([]*PostDetail, len(details))
	for i := range details {
		details[i].ViewCount = views[details[i].Post.ID]
		ptrs[i] = &details[i]
	}
	attachViewer := uuid.Nil
	if viewerID != nil {
		attachViewer = *viewerID
	}
	s.attachChannelRefs(ctx, attachViewer, ptrs)
	return details, nil
}

// countsForSoundPosts is the like and comment counts of a page in one round
// trip per store; soundCounts replaces it in tests. Best-effort like the
// other listings: a page whose counts cannot be read shows zeros.
func (s *Service) countsForSoundPosts(ctx context.Context, postIDs []uuid.UUID) map[uuid.UUID]*scylla.Counts {
	if len(postIDs) == 0 {
		return map[uuid.UUID]*scylla.Counts{}
	}
	if s.soundCounts != nil {
		counts, err := s.soundCounts(ctx, postIDs)
		if err != nil {
			slog.WarnContext(ctx, "sound page counts unavailable; serving 0", "posts", len(postIDs), "err", err)
			return map[uuid.UUID]*scylla.Counts{}
		}
		return counts
	}
	if s.scyllaStore == nil || s.pgStore == nil {
		slog.WarnContext(ctx, "sound page counts unavailable: no counts store; serving 0", "posts", len(postIDs))
		return map[uuid.UUID]*scylla.Counts{}
	}
	counts, err := s.scyllaStore.BatchGetCounts(ctx, postIDs)
	if err == nil {
		err = s.overlayCommentCounts(ctx, postIDs, counts)
	}
	if err != nil {
		slog.WarnContext(ctx, "sound page counts unavailable; serving 0", "posts", len(postIDs), "err", err)
		return map[uuid.UUID]*scylla.Counts{}
	}
	return counts
}

// soundOrigin is the reel a sound was taken from, for a viewer who may open
// it by the direct read's gate; nil for everyone else, and nil when that
// could not be decided.
func (s *Service) soundOrigin(ctx context.Context, viewerID *uuid.UUID, originID uuid.UUID) *PostDetail {
	if err := s.singlePostRead(ctx, originID, viewerID); err != nil {
		if !errors.Is(err, ErrPostNotVisible) && !errors.Is(err, ErrPostNotFound) &&
			!errors.Is(err, ErrAgeSignIn) && !errors.Is(err, ErrAgeRestricted) && !errors.Is(err, ErrAgeUnverified) {
			slog.WarnContext(ctx, "sound origin omitted: read gate unresolved", "post_id", originID, "err", err)
		}
		return nil
	}
	p, err := s.sounds().GetPost(ctx, originID)
	if err != nil || p == nil {
		if err != nil {
			slog.WarnContext(ctx, "sound origin omitted: read failed", "post_id", originID, "err", err)
		}
		return nil
	}
	details, err := s.hydrateSoundPosts(ctx, viewerID, []postgres.Post{*p})
	if err != nil || len(details) == 0 {
		if err != nil {
			slog.WarnContext(ctx, "sound origin omitted: hydration failed", "post_id", originID, "err", err)
		}
		return nil
	}
	return &details[0]
}

// httpSoundSource calls POST {mediaServiceURL}/v1/media/internal/:mediaId/sound
// as a trusted service caller: the internal key, and never a viewer id. Who
// may use the sound was decided here before the call.
type httpSoundSource struct{ svc *Service }

func (h httpSoundSource) EnsureSound(ctx context.Context, mediaID uuid.UUID, in SoundRequest) (*MediaSound, error) {
	s := h.svc
	if s == nil || s.mediaServiceURL == "" {
		return nil, fmt.Errorf("%w: media-service not configured", ErrSoundUnavailable)
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSoundUnavailable, err)
	}
	ctx, cancel := context.WithTimeout(ctx, soundMakeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.mediaServiceURL, "/")+"/v1/media/internal/"+mediaID.String()+"/sound", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSoundUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	// Its own client: the shared one gives up after 5 s, and the first use
	// of a sound runs an extraction.
	resp, err := (&http.Client{Timeout: soundMakeTimeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSoundUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read: %v", ErrSoundUnavailable, err)
	}
	var envelope struct {
		Data  *MediaSound `json:"data"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeErr := json.Unmarshal(raw, &envelope)
	switch {
	case resp.StatusCode == http.StatusOK:
		if decodeErr != nil || envelope.Data == nil {
			return nil, fmt.Errorf("%w: unreadable answer", ErrSoundUnavailable)
		}
		return envelope.Data, nil
	case resp.StatusCode == http.StatusUnprocessableEntity:
		// media-service's refusal passes through by its code alone.
		if decodeErr == nil && envelope.Error != nil {
			if code := refusalCode(envelope.Error.Code); code != "" {
				return nil, &SoundRefusal{Code: code}
			}
		}
		return nil, fmt.Errorf("%w: refusal without a code", ErrSoundUnavailable)
	default:
		return nil, fmt.Errorf("%w: status %d", ErrSoundUnavailable, resp.StatusCode)
	}
}

// refusalCode admits a code of upper-case letters, digits and underscores,
// at most 40 long; anything else is not a code and answers "".
func refusalCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 40 {
		return ""
	}
	for _, r := range code {
		if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' {
			return ""
		}
	}
	return code
}
