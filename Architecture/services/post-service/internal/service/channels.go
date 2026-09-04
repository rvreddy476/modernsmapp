package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Tube channels (2026-09-05).
//
// Founder rule: "Before submitting a video, a channel name should be created."
// One channel per account, owned here because post-service owns the videos.
// The rows live in the shared `channels` table (see migrations/041_channels.sql).

const (
	MinChannelNameRunes  = 3
	MaxChannelNameRunes  = 40
	MinChannelHandleLen  = 3
	MaxChannelHandleLen  = 30
	MaxChannelAboutRunes = 200
)

var (
	ErrInvalidChannelName  = errors.New("channel name must be 3-40 characters")
	ErrInvalidChannelAbout = errors.New("channel about must be at most 200 characters")
	ErrInvalidHandle       = errors.New("handle must be 3-30 characters: lowercase letters, digits, '.' and '_', starting and ending with a letter or digit, no '..'")
	// ErrChannelRequired: a long video was posted by an account with no channel.
	ErrChannelRequired = errors.New("Create your channel before posting a video")
	// ErrChannelNotFound mirrors the store error at the service boundary.
	ErrChannelNotFound = postgres.ErrChannelNotFound
)

// handlePattern is the contract's rule after lowercasing; ".." is checked
// separately because a regexp can't express it cleanly.
var handlePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.]*[a-z0-9]$`)

// NormalizeChannelName trims and checks the 3-40 rune ceiling.
func NormalizeChannelName(name string) (string, error) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	if n < MinChannelNameRunes || n > MaxChannelNameRunes {
		return "", ErrInvalidChannelName
	}
	return name, nil
}

// NormalizeChannelAbout trims and checks the 200 rune ceiling.
func NormalizeChannelAbout(about string) (string, error) {
	about = strings.TrimSpace(about)
	if utf8.RuneCountInString(about) > MaxChannelAboutRunes {
		return "", ErrInvalidChannelAbout
	}
	return about, nil
}

// NormalizeChannelHandle lowercases, strips a leading '@' and validates:
// 3-30 chars, ^[a-z0-9][a-z0-9_.]*[a-z0-9]$, no "..".
func NormalizeChannelHandle(handle string) (string, error) {
	handle = strings.ToLower(strings.TrimSpace(handle))
	handle = strings.TrimPrefix(handle, "@")
	if len(handle) < MinChannelHandleLen || len(handle) > MaxChannelHandleLen {
		return "", ErrInvalidHandle
	}
	if !handlePattern.MatchString(handle) || strings.Contains(handle, "..") {
		return "", ErrInvalidHandle
	}
	return handle, nil
}

// SlugifyHandle derives a valid handle base from free text (a username or a
// display name): lowercase, every run of invalid characters becomes one '.',
// leading/trailing separators dropped, padded when too short and cut to fit
// a numeric suffix. The result always passes NormalizeChannelHandle.
func SlugifyHandle(seed string) string {
	var b strings.Builder
	lastSep := true // treat start as a separator so leading junk is dropped
	for _, r := range strings.ToLower(strings.TrimSpace(seed)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastSep = false
		case r == '_':
			if !lastSep {
				b.WriteRune('_')
				lastSep = true
			}
		default:
			if !lastSep {
				b.WriteRune('.')
				lastSep = true
			}
		}
	}
	base := strings.TrimRight(b.String(), "._")
	if len(base) < MinChannelHandleLen {
		if base == "" {
			base = "creator"
		} else {
			base += ".channel"
		}
	}
	// Leave room for a 2-digit suffix.
	const room = MaxChannelHandleLen - 2
	if len(base) > room {
		base = strings.TrimRight(base[:room], "._")
	}
	return base
}

// channelStore is the slice of the Postgres store the channel flows need,
// an interface so the service tests can drive it with a fake.
type channelStore interface {
	CreateChannel(ctx context.Context, ch *postgres.Channel) error
	UpdateChannel(ctx context.Context, userID uuid.UUID, patch postgres.ChannelPatch) (*postgres.Channel, error)
	GetChannelByUserID(ctx context.Context, userID uuid.UUID) (*postgres.Channel, error)
	GetChannelByHandle(ctx context.Context, handle string) (*postgres.Channel, error)
	ChannelHandleExists(ctx context.Context, handle string) (bool, error)
	GetChannelsByUserIDs(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]*postgres.Channel, error)
	CountChannelVideos(ctx context.Context, userID uuid.UUID) (int, error)
	CountChannelVideosBatch(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]int, error)
}

// ChannelView is the channel JSON: what the owner and the public both see.
type ChannelView struct {
	UserID        uuid.UUID  `json:"user_id"`
	Name          string     `json:"name"`
	Handle        string     `json:"handle"`
	About         string     `json:"about"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
	AvatarURL     *string    `json:"avatar_url"`
	VideoCount    int        `json:"video_count"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ChannelRef is the card-sized channel attached to a long_video post.
type ChannelRef struct {
	UserID    uuid.UUID `json:"user_id"`
	Name      string    `json:"name"`
	Handle    string    `json:"handle"`
	AvatarURL *string   `json:"avatar_url"`
}

// CreateChannelInput is the POST /v1/channels body.
type CreateChannelInput struct {
	Name          string
	Handle        string
	About         string
	AvatarMediaID *uuid.UUID
}

// UpdateChannelInput is the PATCH /v1/channels/me body: nil = untouched.
type UpdateChannelInput struct {
	Name          *string
	Handle        *string
	About         *string
	AvatarMediaID *uuid.UUID
	ClearAvatar   bool
}

// SetMediaServiceURL configures the media-service base URL used to resolve
// channel avatar URLs through the delivery gate.
func (s *Service) SetMediaServiceURL(url string) {
	s.mediaServiceURL = url
}

// CreateChannel creates the caller's one channel.
func (s *Service) CreateChannel(ctx context.Context, userID uuid.UUID, in CreateChannelInput) (*ChannelView, error) {
	if s.channels == nil {
		return nil, errors.New("channel store not configured")
	}
	name, err := NormalizeChannelName(in.Name)
	if err != nil {
		return nil, err
	}
	handle, err := NormalizeChannelHandle(in.Handle)
	if err != nil {
		return nil, err
	}
	about, err := NormalizeChannelAbout(in.About)
	if err != nil {
		return nil, err
	}
	// A friendlier 409 than the unique index alone: the index is still the
	// authority for a concurrent double-create.
	if existing, err := s.channels.GetChannelByUserID(ctx, userID); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, postgres.ErrChannelExists
	}
	ch := &postgres.Channel{UserID: userID, Name: name, Handle: handle, About: about, AvatarMediaID: in.AvatarMediaID}
	if err := s.channels.CreateChannel(ctx, ch); err != nil {
		return nil, err
	}
	return s.channelView(ctx, userID, ch), nil
}

// GetMyChannel returns the caller's channel or ErrChannelNotFound.
func (s *Service) GetMyChannel(ctx context.Context, userID uuid.UUID) (*ChannelView, error) {
	if s.channels == nil {
		return nil, errors.New("channel store not configured")
	}
	ch, err := s.channels.GetChannelByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, ErrChannelNotFound
	}
	return s.channelView(ctx, userID, ch), nil
}

// UpdateMyChannel applies any subset of fields to the caller's channel.
func (s *Service) UpdateMyChannel(ctx context.Context, userID uuid.UUID, in UpdateChannelInput) (*ChannelView, error) {
	if s.channels == nil {
		return nil, errors.New("channel store not configured")
	}
	patch := postgres.ChannelPatch{AvatarMediaID: in.AvatarMediaID, ClearAvatar: in.ClearAvatar}
	if in.Name != nil {
		name, err := NormalizeChannelName(*in.Name)
		if err != nil {
			return nil, err
		}
		patch.Name = &name
	}
	if in.Handle != nil {
		handle, err := NormalizeChannelHandle(*in.Handle)
		if err != nil {
			return nil, err
		}
		patch.Handle = &handle
	}
	if in.About != nil {
		about, err := NormalizeChannelAbout(*in.About)
		if err != nil {
			return nil, err
		}
		patch.About = &about
	}
	ch, err := s.channels.UpdateChannel(ctx, userID, patch)
	if err != nil {
		return nil, err
	}
	return s.channelView(ctx, userID, ch), nil
}

// GetChannelByRef resolves a public channel by handle (with or without '@')
// or by owner user id. viewerID scopes the avatar URL resolution.
func (s *Service) GetChannelByRef(ctx context.Context, viewerID uuid.UUID, ref string) (*ChannelView, error) {
	if s.channels == nil {
		return nil, errors.New("channel store not configured")
	}
	var (
		ch  *postgres.Channel
		err error
	)
	if id, parseErr := uuid.Parse(strings.TrimSpace(ref)); parseErr == nil {
		ch, err = s.channels.GetChannelByUserID(ctx, id)
	} else {
		handle, normErr := NormalizeChannelHandle(ref)
		if normErr != nil {
			return nil, ErrChannelNotFound
		}
		ch, err = s.channels.GetChannelByHandle(ctx, handle)
	}
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, ErrChannelNotFound
	}
	return s.channelView(ctx, viewerID, ch), nil
}

// ChannelHandleAvailability answers GET /v1/channels/handle-available.
//
// With a handle: available = valid and unclaimed; the suggestion is the
// handle itself when available, else a free variant of it. Without one the
// suggestion is derived from the caller's username / display name (or the
// optional seed), so the composer can prefill the field.
func (s *Service) ChannelHandleAvailability(ctx context.Context, userID uuid.UUID, handle, seed string) (bool, string, error) {
	if s.channels == nil {
		return false, "", errors.New("channel store not configured")
	}
	if strings.TrimSpace(handle) != "" {
		normalized, err := NormalizeChannelHandle(handle)
		if err == nil {
			taken, err := s.channels.ChannelHandleExists(ctx, normalized)
			if err != nil {
				return false, "", err
			}
			if !taken {
				return true, normalized, nil
			}
			// Taken: the caller's own handle is theirs, not "taken".
			if own, err := s.channels.GetChannelByUserID(ctx, userID); err == nil && own != nil && own.Handle == normalized {
				return true, normalized, nil
			}
		}
		suggestion, err := s.freeHandle(ctx, SlugifyHandle(handle))
		return false, suggestion, err
	}
	if strings.TrimSpace(seed) == "" {
		seed = s.handleSeedFromProfile(ctx, userID)
	}
	suggestion, err := s.freeHandle(ctx, SlugifyHandle(seed))
	if err != nil {
		return false, "", err
	}
	return false, suggestion, nil
}

// SuggestChannelHandle returns a free handle derived from the user's profile.
func (s *Service) SuggestChannelHandle(ctx context.Context, userID uuid.UUID) (string, error) {
	return s.freeHandle(ctx, SlugifyHandle(s.handleSeedFromProfile(ctx, userID)))
}

// freeHandle returns base when unclaimed, else base + the first free numeric
// suffix (1..99), else base + 4 random digits.
func (s *Service) freeHandle(ctx context.Context, base string) (string, error) {
	taken, err := s.channels.ChannelHandleExists(ctx, base)
	if err != nil {
		return "", err
	}
	if !taken {
		return base, nil
	}
	for i := 1; i < 100; i++ {
		candidate := fmt.Sprintf("%s%d", base, i)
		taken, err := s.channels.ChannelHandleExists(ctx, candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	trimmed := base
	if len(trimmed) > MaxChannelHandleLen-4 {
		trimmed = strings.TrimRight(trimmed[:MaxChannelHandleLen-4], "._")
	}
	return fmt.Sprintf("%s%04d", trimmed, rand.Intn(10000)), nil //nolint:gosec
}

// handleSeedFromProfile asks identity-profile (the batch contract the feed
// and comments already use) for the user's username, falling back to the
// display name, then to a generic seed. Best-effort.
func (s *Service) handleSeedFromProfile(ctx context.Context, userID uuid.UUID) string {
	if s.profileServiceURL == "" || userID == uuid.Nil {
		return ""
	}
	profiles, err := s.fetchCommentProfiles(ctx, &userID, []string{userID.String()})
	if err != nil {
		slog.WarnContext(ctx, "channel handle suggestion: profile lookup skipped", "err", err)
		return ""
	}
	p, ok := profiles[userID]
	if !ok {
		return ""
	}
	if strings.TrimSpace(p.Username) != "" {
		return p.Username
	}
	return p.DisplayName
}

// RequireChannelForVideo is the founder's gate: a long video needs a channel.
// Fails closed — no store, no channel.
func (s *Service) RequireChannelForVideo(ctx context.Context, authorID uuid.UUID) error {
	if s.channels == nil {
		return ErrChannelRequired
	}
	ch, err := s.channels.GetChannelByUserID(ctx, authorID)
	if err != nil {
		return fmt.Errorf("channel lookup: %w", err)
	}
	if ch == nil {
		return ErrChannelRequired
	}
	return nil
}

// ChannelRefsForUsers resolves card-sized channels for a page of authors in
// one query plus one media batch — the feed's per-page call.
func (s *Service) ChannelRefsForUsers(ctx context.Context, viewerID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]*ChannelRef, error) {
	if s.channels == nil {
		return map[uuid.UUID]*ChannelRef{}, nil
	}
	chans, err := s.channels.GetChannelsByUserIDs(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	var avatarIDs []uuid.UUID
	for _, ch := range chans {
		if ch.AvatarMediaID != nil {
			avatarIDs = append(avatarIDs, *ch.AvatarMediaID)
		}
	}
	avatars := s.resolveAvatarURLs(ctx, viewerID, avatarIDs)
	out := make(map[uuid.UUID]*ChannelRef, len(chans))
	for id, ch := range chans {
		out[id] = channelRef(ch, avatars)
	}
	return out, nil
}

func channelRef(ch *postgres.Channel, avatars map[uuid.UUID]string) *ChannelRef {
	ref := &ChannelRef{UserID: ch.UserID, Name: ch.Name, Handle: ch.Handle}
	if ch.AvatarMediaID != nil {
		if u, ok := avatars[*ch.AvatarMediaID]; ok && u != "" {
			url := u
			ref.AvatarURL = &url
		}
	}
	return ref
}

// isLongVideoContentType: the post kinds a channel card belongs to.
func isLongVideoContentType(ct string) bool {
	return ct == "long_video" || ct == "video"
}

// attachChannelRefs fills PostDetail.Channel on every long_video post whose
// author has a channel. Best-effort: a post reads the same without it.
func (s *Service) attachChannelRefs(ctx context.Context, viewerID uuid.UUID, details []*PostDetail) {
	if s.channels == nil || len(details) == 0 {
		return
	}
	seen := make(map[uuid.UUID]bool)
	var authorIDs []uuid.UUID
	for _, d := range details {
		if d == nil || d.Post == nil || !isLongVideoContentType(d.ContentType) {
			continue
		}
		if !seen[d.AuthorID] {
			seen[d.AuthorID] = true
			authorIDs = append(authorIDs, d.AuthorID)
		}
	}
	if len(authorIDs) == 0 {
		return
	}
	refs, err := s.ChannelRefsForUsers(ctx, viewerID, authorIDs)
	if err != nil {
		slog.WarnContext(ctx, "channel attach skipped", "err", err)
		return
	}
	for _, d := range details {
		if d == nil || d.Post == nil || !isLongVideoContentType(d.ContentType) {
			continue
		}
		if ref, ok := refs[d.AuthorID]; ok {
			d.Channel = ref
		}
	}
}

// channelView builds the full channel JSON (avatar URL + video count).
func (s *Service) channelView(ctx context.Context, viewerID uuid.UUID, ch *postgres.Channel) *ChannelView {
	view := &ChannelView{
		UserID:        ch.UserID,
		Name:          ch.Name,
		Handle:        ch.Handle,
		About:         ch.About,
		AvatarMediaID: ch.AvatarMediaID,
		CreatedAt:     ch.CreatedAt,
		UpdatedAt:     ch.UpdatedAt,
	}
	if ch.AvatarMediaID != nil {
		if u, ok := s.resolveAvatarURLs(ctx, viewerID, []uuid.UUID{*ch.AvatarMediaID})[*ch.AvatarMediaID]; ok && u != "" {
			url := u
			view.AvatarURL = &url
		}
	}
	if n, err := s.channels.CountChannelVideos(ctx, ch.UserID); err == nil {
		view.VideoCount = n
	} else {
		slog.WarnContext(ctx, "channel video count skipped", "user_id", ch.UserID, "err", err)
	}
	return view
}

// avatarVariantPreference: the image rendition an avatar should use, best first.
var avatarVariantPreference = []string{"small_480", "thumb_150", "medium_1080", "original"}

const avatarResolveTimeout = 3 * time.Second

// resolveAvatarURLs turns avatar media ids into delivery URLs through
// media-service's batch endpoint, as the viewer (the delivery gate asks this
// service back whether the viewer may see the asset — see
// ViewerMayAccessMediaBatch, which answers yes for any channel avatar).
// Best-effort: an unresolved avatar is a null avatar_url, never an error.
func (s *Service) resolveAvatarURLs(ctx context.Context, viewerID uuid.UUID, mediaIDs []uuid.UUID) map[uuid.UUID]string {
	out := make(map[uuid.UUID]string, len(mediaIDs))
	if s.mediaServiceURL == "" || len(mediaIDs) == 0 {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, avatarResolveTimeout)
	defer cancel()

	for start := 0; start < len(mediaIDs); start += 50 {
		end := start + 50
		if end > len(mediaIDs) {
			end = len(mediaIDs)
		}
		ids := make([]string, 0, end-start)
		for _, id := range mediaIDs[start:end] {
			ids = append(ids, id.String())
		}
		body, err := json.Marshal(map[string]any{"ids": ids})
		if err != nil {
			return out
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			strings.TrimRight(s.mediaServiceURL, "/")+"/v1/media/batch", bytes.NewReader(body))
		if err != nil {
			return out
		}
		req.Header.Set("Content-Type", "application/json")
		if viewerID != uuid.Nil {
			req.Header.Set("X-User-Id", viewerID.String())
		}
		if s.internalServiceKey != "" {
			req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			slog.WarnContext(ctx, "channel avatar resolve skipped", "err", err)
			return out
		}
		var envelope struct {
			Data map[string]struct {
				Variants map[string]string `json:"variants"`
			} `json:"data"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || decodeErr != nil {
			slog.WarnContext(ctx, "channel avatar resolve skipped", "status", resp.StatusCode, "err", decodeErr)
			return out
		}
		for idStr, d := range envelope.Data {
			id, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			if u := pickAvatarVariant(d.Variants); u != "" {
				out[id] = u
			}
		}
	}
	return out
}

func pickAvatarVariant(variants map[string]string) string {
	for _, name := range avatarVariantPreference {
		if u := variants[name]; u != "" {
			return u
		}
	}
	return ""
}

// gateVideoBehindChannel applies the founder's rule to a normalized content
// type: long videos (and the legacy "video" spelling) need the author's
// channel; every other kind — reels/flicks included — passes untouched
// without a store round trip.
func (s *Service) gateVideoBehindChannel(ctx context.Context, authorID uuid.UUID, contentType string) error {
	if !isLongVideoContentType(contentType) {
		return nil
	}
	return s.RequireChannelForVideo(ctx, authorID)
}
