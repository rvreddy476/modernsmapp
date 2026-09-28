package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

/*
	End screens and cards that viewers see (MTube, 2026-09-29; migration 054).

	  POST /v1/posts/:postId/end-screens {screens:[...]}   owner; replaces all
	  GET  /v1/posts/:postId/end-screens                   gated like the detail
	  POST /v1/posts/:postId/end-screens/:elementId/impression|click   204
	  POST /v1/posts/:postId/cards {cards:[...]}           owner; replaces all
	  GET  /v1/posts/:postId/cards                         gated like the detail
	  POST /v1/posts/:postId/cards/:cardId/impression|click            204

	Saves: 401 / 404 / 403 as every authoring write, then the 422 rules in
	the contract's order (validateEndScreens, validateVideoCards). An empty
	list always saves: clearing is allowed on any post.

	Reads: the post detail's gate first (singlePostRead: visibility incl.
	private shares, then age), then each element RESOLVED for this viewer.
	An element whose target the viewer may not open — private, deleted,
	scheduled or processing, an author who blocked them or went private,
	age-restricted for them — is DROPPED, never sent half-empty. The owner
	gets every element (a dead target resolves to null so the editor can
	show it) plus the raw fields and 28 days of stats. Made-for-kids posts
	answer [] to everyone else.

	latest / popular: the channel's public long videos other than this one,
	newest first (ChannelPublicVideoIDs); latest is the first the viewer may
	open, popular the most viewed of the newest 200 by the same analytics
	view count the watch page shows (ties: the newer), again the first the
	viewer may open.
*/

// The limits the save rules enforce.
const (
	MaxEndScreenElements    = 4
	MinEndScreenVideoMs     = 25000
	EndScreenEarliestMs     = 20000 // start_ms >= duration - 20 s
	EndScreenLatestMs       = 5000  // start_ms <= duration - 5 s
	EndScreenEndToleranceMs = 500
	EndScreenMinWidth       = 0.12
	EndScreenMaxWidth       = 0.5
	EndScreenMaxOverlap     = 0.10 // share of the smaller box
	MaxEndScreenTitle       = 60
	MaxVideoCards           = 5
	MaxTargetURL            = 2048
	EndScreenStatsDays      = 28

	// latest / popular candidates.
	endScreenLatestCandidates  = 5
	endScreenPopularCandidates = 200
	endScreenResolveTries      = 5
)

// The 422 codes, in the order the save checks them.
const (
	CodeEndScreenTooMany     = "END_SCREEN_TOO_MANY"
	CodeEndScreenSubscribe   = "END_SCREEN_SUBSCRIBE"
	CodeEndScreenNotEligible = "END_SCREEN_NOT_ELIGIBLE"
	CodeEndScreenKids        = "END_SCREEN_KIDS"
	CodeEndScreenTiming      = "END_SCREEN_TIMING"
	CodeEndScreenPosition    = "END_SCREEN_POSITION"
	CodeEndScreenOverlap     = "END_SCREEN_OVERLAP"
	CodeEndScreenTarget      = "END_SCREEN_TARGET"
	CodeCardTooMany          = "CARD_TOO_MANY"
	CodeCardKids             = "CARD_KIDS"
	CodeCardTiming           = "CARD_TIMING"
	CodeCardTarget           = "CARD_TARGET"
)

// AuthoringRuleError is a save refused by one of the rules above: a 422
// carrying Code. The message names the element.
type AuthoringRuleError struct {
	Code    string
	Message string
}

func (e *AuthoringRuleError) Error() string { return e.Message }

func ruleErr(code, format string, args ...any) error {
	return &AuthoringRuleError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ErrEndScreenElementNotFound: the impression / click names an element (or
// card) that is not one of this post's, or the post shows none.
var ErrEndScreenElementNotFound = errors.New("element not found")

// End-screen types and video modes (mirror the CHECKs of 012 / 054).
var (
	EndScreenTypes = []string{"video", "playlist", "channel_subscribe", "channel", "external_link"}
	VideoCardTypes = []string{"video", "playlist", "poll", "external_link"}
	EndScreenModes = []string{"specific", "latest", "popular"}
)

// endScreenStore is the slice of the store the end-screen and card flows
// need; an interface so every rule is testable without a database.
type endScreenStore interface {
	GetEndScreenSubject(ctx context.Context, postID uuid.UUID) (*postgres.EndScreenSubject, error)
	GetPostTargetFacts(ctx context.Context, postID uuid.UUID) (*postgres.PostTargetFacts, error)
	GetEndScreens(ctx context.Context, postID uuid.UUID) ([]postgres.EndScreen, error)
	SaveEndScreens(ctx context.Context, postID uuid.UUID, screens []postgres.EndScreen) error
	GetVideoCards(ctx context.Context, postID uuid.UUID) ([]postgres.VideoCard, error)
	SaveVideoCards(ctx context.Context, postID uuid.UUID, cards []postgres.VideoCard) error
	ChannelPublicVideoIDs(ctx context.Context, ownerID, excludeID uuid.UUID, limit int) ([]uuid.UUID, error)
	EndScreenStatsSince(ctx context.Context, postID uuid.UUID, since time.Time) (map[uuid.UUID]postgres.ElementStats, error)
	CardStatsSince(ctx context.Context, postID uuid.UUID, since time.Time) (map[uuid.UUID]postgres.ElementStats, error)
	EndScreenOnPost(ctx context.Context, postID, elementID uuid.UUID) (bool, error)
	CardOnPost(ctx context.Context, postID, cardID uuid.UUID) (bool, error)
	BumpEndScreenStat(ctx context.Context, postID, elementID uuid.UUID, day time.Time, click bool) error
	BumpCardStat(ctx context.Context, postID, cardID uuid.UUID, day time.Time, click bool) error
}

// ── position ───────────────────────────────────────────────────────────────

// EndScreenPosition is an element's box: fractions of the 16:9 frame, the
// top-left corner and the width. The height follows from the kind.
type EndScreenPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
}

// The legacy {slot:n} places, as agreed with the web editor (2026-09-29):
// inset x 0.05 / y 0.10 from the frame's edges; a tile is w 0.30, a circle
// w 0.20; slot 0 top-left, 1 top-right, 2 bottom-left, 3 bottom-right.
const (
	endScreenSlotInsetX = 0.05
	endScreenSlotInsetY = 0.10
	endScreenSlotTileW  = 0.30
	endScreenSlotCircle = 0.20
)

// EndScreenSlotPlace is where slot (0..3; other values wrap) puts an
// element of kind.
func EndScreenSlotPlace(kind string, slot int) EndScreenPosition {
	slot = ((slot % 4) + 4) % 4
	w := endScreenSlotTileW
	if endScreenIsCircle(kind) {
		w = endScreenSlotCircle
	}
	p := EndScreenPosition{X: endScreenSlotInsetX, Y: endScreenSlotInsetY, W: w}
	if slot == 1 || slot == 3 {
		p.X = 1 - endScreenSlotInsetX - w
	}
	if slot >= 2 {
		p.Y = 1 - endScreenSlotInsetY - endScreenBoxHeight(kind, w)
	}
	return p
}

// ParseEndScreenPosition reads {x, y, w}. A pre-054 {slot: n} (n in 0..3) is
// that slot's place for kind; any other value missing x, y or w falls back
// to the slot of the element's index. Used for stored rows and for saves
// alike, so an old row is rewritten in the new shape on its next save.
func ParseEndScreenPosition(raw json.RawMessage, kind string, index int) EndScreenPosition {
	var p struct {
		X    *float64 `json:"x"`
		Y    *float64 `json:"y"`
		W    *float64 `json:"w"`
		Slot *int     `json:"slot"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &p) == nil {
		if p.X != nil && p.Y != nil && p.W != nil {
			return EndScreenPosition{X: *p.X, Y: *p.Y, W: *p.W}
		}
		if p.Slot != nil && *p.Slot >= 0 && *p.Slot < 4 {
			return EndScreenSlotPlace(kind, *p.Slot)
		}
	}
	return EndScreenSlotPlace(kind, index)
}

// endScreenIsCircle: subscribe and channel elements are drawn as a circle
// of diameter w; the rest are 16:9 tiles of width w.
func endScreenIsCircle(kind string) bool {
	return kind == "channel_subscribe" || kind == "channel"
}

// endScreenBoxHeight is the box height as a fraction of the frame height. A
// 16:9 tile of width w (of a 16:9 frame) is w tall; a circle of diameter w
// frame-widths is w*16/9 frame-heights.
func endScreenBoxHeight(kind string, w float64) float64 {
	if endScreenIsCircle(kind) {
		return w * 16 / 9
	}
	return w
}

const positionEpsilon = 1e-9

func positionInFrame(kind string, p EndScreenPosition) bool {
	in01 := func(v float64) bool { return !math.IsNaN(v) && v >= 0 && v <= 1 }
	if !in01(p.X) || !in01(p.Y) || !in01(p.W) {
		return false
	}
	if p.W < EndScreenMinWidth-positionEpsilon || p.W > EndScreenMaxWidth+positionEpsilon {
		return false
	}
	if p.X+p.W > 1+positionEpsilon {
		return false
	}
	return p.Y+endScreenBoxHeight(kind, p.W) <= 1+positionEpsilon
}

// boxesOverlapTooMuch: the intersection is more than EndScreenMaxOverlap of
// the smaller box's area.
func boxesOverlapTooMuch(kindA string, a EndScreenPosition, kindB string, b EndScreenPosition) bool {
	ah, bh := endScreenBoxHeight(kindA, a.W), endScreenBoxHeight(kindB, b.W)
	ix := math.Min(a.X+a.W, b.X+b.W) - math.Max(a.X, b.X)
	iy := math.Min(a.Y+ah, b.Y+bh) - math.Max(a.Y, b.Y)
	if ix <= 0 || iy <= 0 {
		return false
	}
	smaller := math.Min(a.W*ah, b.W*bh)
	return ix*iy > EndScreenMaxOverlap*smaller+positionEpsilon
}

// ── save ───────────────────────────────────────────────────────────────────

// EndScreenInput is one element of a save, already shape-checked by the
// handler (type and mode enums, uuids, title length).
type EndScreenInput struct {
	// ID, when it is one of this post's elements, keeps that element (and
	// its stats) instead of replacing it. Optional; the contract's request
	// does not need it.
	ID        *uuid.UUID
	Type      string
	VideoMode string
	TargetID  *uuid.UUID
	TargetURL *string
	Title     *string
	Position  EndScreenPosition
	StartMs   int
	EndMs     int
}

// VideoCardInput is one card of a save, shape-checked by the handler.
type VideoCardInput struct {
	ID         *uuid.UUID
	Type       string
	TargetID   *uuid.UUID
	TargetURL  *string
	Title      string
	TeaserText *string
	AppearAtMs int
}

// validateEndScreenLayout is the timing, position and overlap rules, in that
// order, for a video of durationMs. Pure.
func validateEndScreenLayout(durationMs int, in []EndScreenInput) error {
	for i, el := range in {
		switch {
		case el.StartMs < durationMs-EndScreenEarliestMs:
			return ruleErr(CodeEndScreenTiming, "screens[%d].start_ms must be within the last %d s of the video", i, EndScreenEarliestMs/1000)
		case el.StartMs > durationMs-EndScreenLatestMs:
			return ruleErr(CodeEndScreenTiming, "screens[%d].start_ms must be at least %d s before the end of the video", i, EndScreenLatestMs/1000)
		case el.EndMs <= el.StartMs:
			return ruleErr(CodeEndScreenTiming, "screens[%d].end_ms must be greater than start_ms", i)
		case el.EndMs > durationMs+EndScreenEndToleranceMs:
			return ruleErr(CodeEndScreenTiming, "screens[%d].end_ms must not pass the end of the video", i)
		}
	}
	for i, el := range in {
		if !positionInFrame(el.Type, el.Position) {
			return ruleErr(CodeEndScreenPosition, "screens[%d].position must be {x, y, w} inside the frame with w between %.2f and %.2f", i, EndScreenMinWidth, EndScreenMaxWidth)
		}
	}
	for i := range in {
		for j := i + 1; j < len(in); j++ {
			if boxesOverlapTooMuch(in[i].Type, in[i].Position, in[j].Type, in[j].Position) {
				return ruleErr(CodeEndScreenOverlap, "screens[%d] and screens[%d] overlap", i, j)
			}
		}
	}
	return nil
}

// endScreenEligible: a long video whose duration is known and at least 25 s.
func endScreenEligible(sub *postgres.EndScreenSubject) bool {
	return sub != nil && isLongVideoContentType(sub.ContentType) && sub.DurationMs >= MinEndScreenVideoMs
}

// checkTargetURL: an absolute https URL with a host, at most 2048 bytes.
// There is no platform blocked-link list to consult (none exists in
// post-service or trust-safety as of 2026-09-29); when one does, it is
// checked here.
func checkTargetURL(raw *string) (string, bool) {
	if raw == nil {
		return "", false
	}
	v := strings.TrimSpace(*raw)
	if v == "" || len(v) > MaxTargetURL {
		return "", false
	}
	u, err := url.Parse(v)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" {
		return "", false
	}
	return v, true
}

// linkDomain is the host a link card shows: lower-cased, "www." dropped.
func linkDomain(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// ownPostTarget checks a post target: the caller's own, public or unlisted,
// not the post itself; needPoll additionally requires a poll on it.
func (s *Service) ownPostTarget(ctx context.Context, callerID, postID uuid.UUID, target *uuid.UUID, needPoll bool) (bool, error) {
	if target == nil || *target == postID {
		return false, nil
	}
	facts, err := s.endScreens.GetPostTargetFacts(ctx, *target)
	if err != nil {
		return false, fmt.Errorf("load target post: %w", err)
	}
	if facts == nil || facts.AuthorID != callerID {
		return false, nil
	}
	switch strings.ToLower(facts.Visibility) {
	case "public", "unlisted":
	default:
		return false, nil
	}
	if needPoll && !facts.HasPoll {
		return false, nil
	}
	return true, nil
}

// ownPublicCollection: the caller's own public, creator-made collection.
func (s *Service) ownPublicCollection(ctx context.Context, callerID uuid.UUID, target *uuid.UUID) (bool, error) {
	if target == nil {
		return false, nil
	}
	if s.authoringOwners == nil {
		return false, ErrAuthoringStoreUnavailable
	}
	p, err := s.authoringOwners.GetPlaylist(ctx, *target)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("load target collection: %w", err)
	}
	return p != nil && p.CreatorID == callerID && p.Visibility == playlistVisibilityPublic && isUserPlaylist(p), nil
}

// isUserPlaylist: a creator-made collection, never a system one (Queue,
// Loved), which is private to its owner by construction.
func isUserPlaylist(p *postgres.Playlist) bool {
	return p.Kind == "" || p.Kind == "user"
}

// otherChannel: an existing channel that is not the caller's own.
func (s *Service) otherChannel(ctx context.Context, callerID uuid.UUID, target *uuid.UUID) (bool, error) {
	if target == nil || *target == callerID {
		return false, nil
	}
	if s.channels == nil {
		return false, errors.New("channel store not configured")
	}
	ch, err := s.channels.GetChannelByUserID(ctx, *target)
	if err != nil {
		return false, fmt.Errorf("load target channel: %w", err)
	}
	return ch != nil, nil
}

// SaveEndScreens is the owner's replace-all save. Returns the saved rows.
func (s *Service) SaveEndScreens(ctx context.Context, callerID, postID uuid.UUID, in []EndScreenInput) ([]postgres.EndScreen, error) {
	if err := s.requirePostAuthor(ctx, callerID, postID); err != nil {
		return nil, err
	}
	if s.endScreens == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	if len(in) > 0 {
		if err := s.validateEndScreens(ctx, callerID, postID, in); err != nil {
			return nil, err
		}
	}
	rows := make([]postgres.EndScreen, len(in))
	for i, el := range in {
		pos, _ := json.Marshal(el.Position)
		row := postgres.EndScreen{PostID: postID, Type: el.Type, VideoMode: "specific", Position: pos, StartMs: el.StartMs, EndMs: el.EndMs}
		if el.ID != nil {
			row.ID = *el.ID
		}
		switch el.Type {
		case "video":
			row.VideoMode = el.VideoMode
			if row.VideoMode == "" {
				row.VideoMode = "specific"
			}
			if row.VideoMode == "specific" {
				row.TargetID = el.TargetID
			}
		case "playlist", "channel":
			row.TargetID = el.TargetID
		case "external_link":
			u, _ := checkTargetURL(el.TargetURL)
			row.TargetURL = &u
		}
		row.Title = trimmedTitle(el.Title)
		rows[i] = row
	}
	if err := s.endScreens.SaveEndScreens(ctx, postID, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func trimmedTitle(t *string) *string {
	if t == nil {
		return nil
	}
	v := strings.TrimSpace(*t)
	if v == "" {
		return nil
	}
	return &v
}

// validateEndScreens is the 422 rules in the contract's order.
func (s *Service) validateEndScreens(ctx context.Context, callerID, postID uuid.UUID, in []EndScreenInput) error {
	if len(in) > MaxEndScreenElements {
		return ruleErr(CodeEndScreenTooMany, "at most %d end screen elements", MaxEndScreenElements)
	}
	subscribes := 0
	for _, el := range in {
		if el.Type == "channel_subscribe" {
			subscribes++
		}
	}
	if subscribes > 1 {
		return ruleErr(CodeEndScreenSubscribe, "at most one channel_subscribe element")
	}
	sub, err := s.endScreens.GetEndScreenSubject(ctx, postID)
	if err != nil {
		return fmt.Errorf("load end screen subject: %w", err)
	}
	if sub == nil {
		return ErrPostNotFound
	}
	if !endScreenEligible(sub) {
		return ruleErr(CodeEndScreenNotEligible, "end screens need a long video of at least %d s whose length is known", MinEndScreenVideoMs/1000)
	}
	if sub.MadeForKids {
		return ruleErr(CodeEndScreenKids, "end screens are not available on videos made for kids")
	}
	if err := validateEndScreenLayout(sub.DurationMs, in); err != nil {
		return err
	}
	for i, el := range in {
		ok, err := s.endScreenTargetOK(ctx, callerID, postID, el)
		if err != nil {
			return err
		}
		if !ok {
			return ruleErr(CodeEndScreenTarget, "screens[%d] does not point at something it can show", i)
		}
	}
	return nil
}

func (s *Service) endScreenTargetOK(ctx context.Context, callerID, postID uuid.UUID, el EndScreenInput) (bool, error) {
	switch el.Type {
	case "video":
		switch el.VideoMode {
		case "", "specific":
			return s.ownPostTarget(ctx, callerID, postID, el.TargetID, false)
		case "latest", "popular":
			return el.TargetID == nil, nil
		}
		return false, nil
	case "playlist":
		return s.ownPublicCollection(ctx, callerID, el.TargetID)
	case "channel":
		return s.otherChannel(ctx, callerID, el.TargetID)
	case "channel_subscribe":
		return true, nil
	case "external_link":
		_, ok := checkTargetURL(el.TargetURL)
		return ok, nil
	}
	return false, nil
}

// SaveVideoCards is the owner's replace-all save of cards.
func (s *Service) SaveVideoCards(ctx context.Context, callerID, postID uuid.UUID, in []VideoCardInput) ([]postgres.VideoCard, error) {
	if err := s.requirePostAuthor(ctx, callerID, postID); err != nil {
		return nil, err
	}
	if s.endScreens == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	if len(in) > 0 {
		if err := s.validateVideoCards(ctx, callerID, postID, in); err != nil {
			return nil, err
		}
	}
	rows := make([]postgres.VideoCard, len(in))
	for i, card := range in {
		row := postgres.VideoCard{PostID: postID, Type: card.Type, Title: strings.TrimSpace(card.Title),
			TeaserText: trimmedTitle(card.TeaserText), AppearAtMs: card.AppearAtMs}
		if card.ID != nil {
			row.ID = *card.ID
		}
		if card.Type == "external_link" {
			u, _ := checkTargetURL(card.TargetURL)
			row.TargetURL = &u
		} else {
			row.TargetID = card.TargetID
		}
		rows[i] = row
	}
	if err := s.endScreens.SaveVideoCards(ctx, postID, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Service) validateVideoCards(ctx context.Context, callerID, postID uuid.UUID, in []VideoCardInput) error {
	if len(in) > MaxVideoCards {
		return ruleErr(CodeCardTooMany, "at most %d cards", MaxVideoCards)
	}
	sub, err := s.endScreens.GetEndScreenSubject(ctx, postID)
	if err != nil {
		return fmt.Errorf("load card subject: %w", err)
	}
	if sub == nil {
		return ErrPostNotFound
	}
	if sub.MadeForKids {
		return ruleErr(CodeCardKids, "cards are not available on videos made for kids")
	}
	for i, card := range in {
		if sub.DurationMs <= 0 {
			return ruleErr(CodeCardTiming, "cards[%d].appear_at_ms cannot be checked until the video's length is known", i)
		}
		if card.AppearAtMs < 0 || card.AppearAtMs > sub.DurationMs {
			return ruleErr(CodeCardTiming, "cards[%d].appear_at_ms must be within the video", i)
		}
	}
	for i, card := range in {
		var ok bool
		var err error
		switch card.Type {
		case "video":
			ok, err = s.ownPostTarget(ctx, callerID, postID, card.TargetID, false)
		case "poll":
			ok, err = s.ownPostTarget(ctx, callerID, postID, card.TargetID, true)
		case "playlist":
			ok, err = s.ownPublicCollection(ctx, callerID, card.TargetID)
		case "external_link":
			_, ok = checkTargetURL(card.TargetURL)
		}
		if err != nil {
			return err
		}
		if !ok {
			return ruleErr(CodeCardTarget, "cards[%d] does not point at something it can show", i)
		}
	}
	return nil
}

// ── read ───────────────────────────────────────────────────────────────────

// EndScreenVideo is a resolved video target.
type EndScreenVideo struct {
	ID              uuid.UUID `json:"id"`
	Title           string    `json:"title"`
	ThumbnailURL    string    `json:"thumbnail_url"`
	DurationSeconds int       `json:"duration_seconds"`
	ChannelName     string    `json:"channel_name"`
	ViewCount       int64     `json:"view_count"`
}

// EndScreenPlaylist is a resolved collection target.
type EndScreenPlaylist struct {
	ID           uuid.UUID `json:"id"`
	Title        string    `json:"title"`
	ThumbnailURL string    `json:"thumbnail_url"`
	ItemCount    int       `json:"item_count"`
}

// EndScreenChannel is a resolved channel (subscribe: the video's own).
type EndScreenChannel struct {
	UserID          uuid.UUID `json:"user_id"`
	Handle          string    `json:"handle"`
	Name            string    `json:"name"`
	AvatarURL       *string   `json:"avatar_url"`
	SubscriberCount int       `json:"subscriber_count"`
	IsSubscribed    bool      `json:"is_subscribed"`
}

// EndScreenLink is a resolved external link.
type EndScreenLink struct {
	URL    string  `json:"url"`
	Title  *string `json:"title"`
	Domain string  `json:"domain"`
}

// ElementStatsView is the owner's 28-day counters for one element or card.
// click_rate is clicks / impressions (0 with no impressions), 4 decimals.
type ElementStatsView struct {
	Impressions int64   `json:"impressions"`
	Clicks      int64   `json:"clicks"`
	ClickRate   float64 `json:"click_rate"`
}

func statsView(st postgres.ElementStats) ElementStatsView {
	v := ElementStatsView{Impressions: st.Impressions, Clicks: st.Clicks}
	if st.Impressions > 0 {
		v.ClickRate = math.Round(float64(st.Clicks)/float64(st.Impressions)*10000) / 10000
	}
	return v
}

// EndScreenElement is one element as a viewer receives it. Exactly one of
// the four blocks is set, by type; the others are null.
type EndScreenElement struct {
	ID       uuid.UUID          `json:"id"`
	Type     string             `json:"type"`
	Position EndScreenPosition  `json:"position"`
	StartMs  int                `json:"start_ms"`
	EndMs    int                `json:"end_ms"`
	Video    *EndScreenVideo    `json:"video"`
	Playlist *EndScreenPlaylist `json:"playlist"`
	Channel  *EndScreenChannel  `json:"channel"`
	Link     *EndScreenLink     `json:"link"`
}

// EndScreenOwnerElement is the owner's element: the viewer shape plus the
// raw fields the editor edits and the stats.
type EndScreenOwnerElement struct {
	EndScreenElement
	VideoMode string           `json:"video_mode"`
	TargetID  *uuid.UUID       `json:"target_id"`
	TargetURL *string          `json:"target_url"`
	Title     *string          `json:"title"`
	Stats     ElementStatsView `json:"stats"`
}

// CardPoll is a resolved poll card: the poll's post and the same body
// GET /v1/posts/:postId/poll answers.
type CardPoll struct {
	PostID uuid.UUID `json:"post_id"`
	*postgres.PollData
}

// VideoCardView is one card as a viewer receives it.
type VideoCardView struct {
	ID         uuid.UUID          `json:"id"`
	Type       string             `json:"type"`
	AppearAtMs int                `json:"appear_at_ms"`
	Title      string             `json:"title"`
	TeaserText *string            `json:"teaser_text"`
	Video      *EndScreenVideo    `json:"video"`
	Playlist   *EndScreenPlaylist `json:"playlist"`
	Link       *EndScreenLink     `json:"link"`
	Poll       *CardPoll          `json:"poll"`
}

// VideoCardOwnerView is the owner's card.
type VideoCardOwnerView struct {
	VideoCardView
	TargetID  *uuid.UUID       `json:"target_id"`
	TargetURL *string          `json:"target_url"`
	Stats     ElementStatsView `json:"stats"`
}

// endScreenReadSubject runs the detail's gate and loads the subject.
func (s *Service) endScreenReadSubject(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) (*postgres.EndScreenSubject, bool, error) {
	if err := s.singlePostRead(ctx, postID, viewerID); err != nil {
		return nil, false, err
	}
	if s.endScreens == nil {
		return nil, false, ErrAuthoringStoreUnavailable
	}
	sub, err := s.endScreens.GetEndScreenSubject(ctx, postID)
	if err != nil {
		return nil, false, err
	}
	if sub == nil {
		return nil, false, ErrPostNotFound
	}
	owner := viewerID != nil && *viewerID == sub.AuthorID
	return sub, owner, nil
}

func (s *Service) statsSince() time.Time {
	return s.clock().UTC().AddDate(0, 0, -(EndScreenStatsDays - 1))
}

// GetEndScreensFor answers GET /v1/posts/:postId/end-screens: a
// []EndScreenElement for a viewer, a []EndScreenOwnerElement for the owner.
func (s *Service) GetEndScreensFor(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) (any, error) {
	sub, owner, err := s.endScreenReadSubject(ctx, postID, viewerID)
	if err != nil {
		return nil, err
	}
	if sub.MadeForKids && !owner {
		return []EndScreenElement{}, nil
	}
	rows, err := s.endScreens.GetEndScreens(ctx, postID)
	if err != nil {
		return nil, err
	}
	r := &endScreenResolver{s: s, sub: sub, viewer: viewerID, owner: owner}
	if !owner {
		out := make([]EndScreenElement, 0, len(rows))
		for i, row := range rows {
			if el, ok := r.element(ctx, row, i); ok {
				out = append(out, el)
			}
		}
		return out, nil
	}
	stats, err := s.endScreens.EndScreenStatsSince(ctx, postID, s.statsSince())
	if err != nil {
		return nil, fmt.Errorf("load end screen stats: %w", err)
	}
	out := make([]EndScreenOwnerElement, 0, len(rows))
	for i, row := range rows {
		el, _ := r.element(ctx, row, i)
		mode := row.VideoMode
		if mode == "" {
			mode = "specific"
		}
		out = append(out, EndScreenOwnerElement{EndScreenElement: el, VideoMode: mode, TargetID: row.TargetID,
			TargetURL: row.TargetURL, Title: row.Title, Stats: statsView(stats[row.ID])})
	}
	return out, nil
}

// GetVideoCardsFor answers GET /v1/posts/:postId/cards, the same way.
func (s *Service) GetVideoCardsFor(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) (any, error) {
	sub, owner, err := s.endScreenReadSubject(ctx, postID, viewerID)
	if err != nil {
		return nil, err
	}
	if sub.MadeForKids && !owner {
		return []VideoCardView{}, nil
	}
	rows, err := s.endScreens.GetVideoCards(ctx, postID)
	if err != nil {
		return nil, err
	}
	r := &endScreenResolver{s: s, sub: sub, viewer: viewerID, owner: owner}
	if !owner {
		out := make([]VideoCardView, 0, len(rows))
		for _, row := range rows {
			if card, ok := r.card(ctx, row); ok {
				out = append(out, card)
			}
		}
		return out, nil
	}
	stats, err := s.endScreens.CardStatsSince(ctx, postID, s.statsSince())
	if err != nil {
		return nil, fmt.Errorf("load card stats: %w", err)
	}
	out := make([]VideoCardOwnerView, 0, len(rows))
	for _, row := range rows {
		card, _ := r.card(ctx, row)
		out = append(out, VideoCardOwnerView{VideoCardView: card, TargetID: row.TargetID, TargetURL: row.TargetURL,
			Stats: statsView(stats[row.ID])})
	}
	return out, nil
}

// endScreenResolver resolves one post's elements for one viewer; the
// channel-subscribe block and the latest / popular picks are computed once.
type endScreenResolver struct {
	s      *Service
	sub    *postgres.EndScreenSubject
	viewer *uuid.UUID
	owner  bool

	ownChannel        *EndScreenChannel
	ownChannelLoaded  bool
	latest, popular   *EndScreenVideo
	latestLoaded      bool
	popularLoaded     bool
	candidates        []uuid.UUID
	candidatesLoaded  bool
	candidateLoadFail bool
}

// element resolves one row; ok is false when its target is not the
// viewer's to open (the element is then dropped for a viewer).
func (r *endScreenResolver) element(ctx context.Context, row postgres.EndScreen, index int) (EndScreenElement, bool) {
	el := EndScreenElement{ID: row.ID, Type: row.Type, Position: ParseEndScreenPosition(row.Position, row.Type, index), StartMs: row.StartMs, EndMs: row.EndMs}
	switch row.Type {
	case "video":
		switch row.VideoMode {
		case "latest":
			el.Video = r.latestVideo(ctx)
		case "popular":
			el.Video = r.popularVideo(ctx)
		default:
			if row.TargetID != nil {
				el.Video = r.video(ctx, *row.TargetID)
			}
		}
		return el, el.Video != nil
	case "playlist":
		if row.TargetID != nil {
			el.Playlist = r.playlist(ctx, *row.TargetID)
		}
		return el, el.Playlist != nil
	case "channel_subscribe":
		el.Channel = r.subscribeChannel(ctx)
		return el, el.Channel != nil
	case "channel":
		if row.TargetID != nil {
			el.Channel = r.channel(ctx, *row.TargetID)
		}
		return el, el.Channel != nil
	case "external_link":
		el.Link = linkBlock(row.TargetURL, row.Title)
		return el, el.Link != nil
	}
	return el, false
}

// card resolves one card row, the same way.
func (r *endScreenResolver) card(ctx context.Context, row postgres.VideoCard) (VideoCardView, bool) {
	c := VideoCardView{ID: row.ID, Type: row.Type, AppearAtMs: row.AppearAtMs, Title: row.Title, TeaserText: row.TeaserText}
	switch row.Type {
	case "video":
		if row.TargetID != nil {
			c.Video = r.video(ctx, *row.TargetID)
		}
		return c, c.Video != nil
	case "playlist":
		if row.TargetID != nil {
			c.Playlist = r.playlist(ctx, *row.TargetID)
		}
		return c, c.Playlist != nil
	case "poll":
		// The watch page draws no poll card, so a viewer's read leaves poll
		// cards out (the contract allows it); the owner's editor still gets
		// them, resolved against the poll read.
		if !r.owner {
			return c, false
		}
		if row.TargetID != nil {
			c.Poll = r.poll(ctx, *row.TargetID)
		}
		return c, c.Poll != nil
	case "external_link":
		title := row.Title
		c.Link = linkBlock(row.TargetURL, &title)
		return c, c.Link != nil
	}
	return c, false
}

func linkBlock(raw, title *string) *EndScreenLink {
	u, ok := checkTargetURL(raw)
	if !ok {
		return nil
	}
	return &EndScreenLink{URL: u, Title: trimmedTitle(title), Domain: linkDomain(u)}
}

// postCard is the target-post gate every resolved video goes through: the
// direct read's gates (review, processing / scheduled, visibility incl.
// private shares, the author's account, age) — viewablePostCard, the same
// helper the related-post card uses. nil = not this viewer's to open.
func (s *Service) postCard(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) *RelatedPostCard {
	if s.postCardSeam != nil {
		return s.postCardSeam(ctx, postID, viewerID)
	}
	return s.viewablePostCard(ctx, postID, viewerID)
}

func (r *endScreenResolver) video(ctx context.Context, postID uuid.UUID) *EndScreenVideo {
	card := r.s.postCard(ctx, postID, r.viewer)
	if card == nil {
		return nil
	}
	return &EndScreenVideo{ID: card.ID, Title: card.Title, ThumbnailURL: card.ThumbnailURL,
		DurationSeconds: card.DurationSeconds, ChannelName: card.ChannelName, ViewCount: r.s.getViewCount(ctx, card.ID)}
}

func (r *endScreenResolver) loadCandidates(ctx context.Context) []uuid.UUID {
	if !r.candidatesLoaded {
		r.candidatesLoaded = true
		ids, err := r.s.endScreens.ChannelPublicVideoIDs(ctx, r.sub.AuthorID, r.sub.PostID, endScreenPopularCandidates)
		if err != nil {
			slog.WarnContext(ctx, "end screen: channel videos unavailable; latest/popular dropped", "post_id", r.sub.PostID, "err", err)
		}
		r.candidates = ids
	}
	return r.candidates
}

// firstViewable is the first of ids (at most endScreenResolveTries tried)
// the viewer may open.
func (r *endScreenResolver) firstViewable(ctx context.Context, ids []uuid.UUID) *EndScreenVideo {
	for i, id := range ids {
		if i >= endScreenResolveTries {
			break
		}
		if v := r.video(ctx, id); v != nil {
			return v
		}
	}
	return nil
}

func (r *endScreenResolver) latestVideo(ctx context.Context) *EndScreenVideo {
	if !r.latestLoaded {
		r.latestLoaded = true
		ids := r.loadCandidates(ctx)
		if len(ids) > endScreenLatestCandidates {
			ids = ids[:endScreenLatestCandidates]
		}
		r.latest = r.firstViewable(ctx, ids)
	}
	return r.latest
}

// popularOrder sorts candidates (newest first) by views, most first; ties
// keep the newer. Pure.
func popularOrder(ids []uuid.UUID, views map[uuid.UUID]int64) []uuid.UUID {
	out := append([]uuid.UUID(nil), ids...)
	sort.SliceStable(out, func(i, j int) bool { return views[out[i]] > views[out[j]] })
	return out
}

func (r *endScreenResolver) popularVideo(ctx context.Context) *EndScreenVideo {
	if !r.popularLoaded {
		r.popularLoaded = true
		ids := r.loadCandidates(ctx)
		r.popular = r.firstViewable(ctx, popularOrder(ids, r.s.fetchViewCounts(ctx, ids)))
	}
	return r.popular
}

func (r *endScreenResolver) playlist(ctx context.Context, id uuid.UUID) *EndScreenPlaylist {
	if r.s.authoringOwners == nil {
		return nil
	}
	p, err := r.s.authoringOwners.GetPlaylist(ctx, id)
	if err != nil || p == nil || !isUserPlaylist(p) || !playlistReadableBy(p, r.viewer) {
		return nil
	}
	if !r.s.canViewAuthor(ctx, r.viewer, p.CreatorID) {
		return nil
	}
	out := &EndScreenPlaylist{ID: p.ID, Title: p.Title, ItemCount: p.ItemCount}
	if p.CoverURL != nil && strings.TrimSpace(*p.CoverURL) != "" {
		out.ThumbnailURL = *p.CoverURL
		return out
	}
	// No cover: the first item this viewer may open lends its thumbnail, so
	// a private item's frame is never shown to someone who cannot open it.
	items, err := r.s.authoringOwners.GetPlaylistItems(ctx, p.ID)
	if err == nil {
		for i, it := range items {
			if i >= endScreenResolveTries {
				break
			}
			if card := r.s.postCard(ctx, it.PostID, r.viewer); card != nil {
				out.ThumbnailURL = card.ThumbnailURL
				break
			}
		}
	}
	return out
}

func (r *endScreenResolver) subscribeChannel(ctx context.Context) *EndScreenChannel {
	if !r.ownChannelLoaded {
		r.ownChannelLoaded = true
		r.ownChannel = r.channelBlock(ctx, r.sub.AuthorID)
	}
	return r.ownChannel
}

// channel is another channel: dropped when the viewer may not see its
// owner's posts (a block, a private account they do not follow, a
// deactivated owner — canViewAuthor).
func (r *endScreenResolver) channel(ctx context.Context, ownerID uuid.UUID) *EndScreenChannel {
	if !r.s.canViewAuthor(ctx, r.viewer, ownerID) {
		return nil
	}
	return r.channelBlock(ctx, ownerID)
}

func (r *endScreenResolver) channelBlock(ctx context.Context, ownerID uuid.UUID) *EndScreenChannel {
	if r.s.channels == nil {
		return nil
	}
	ch, err := r.s.channels.GetChannelByUserID(ctx, ownerID)
	if err != nil || ch == nil {
		return nil
	}
	out := &EndScreenChannel{UserID: ch.UserID, Handle: ch.Handle, Name: ch.Name, SubscriberCount: ch.SubscriberCount}
	viewer := uuid.Nil
	if r.viewer != nil {
		viewer = *r.viewer
	}
	if viewer != uuid.Nil {
		if sub, err := r.s.channels.GetSubscription(ctx, ch.ID, viewer); err == nil && sub != nil {
			out.IsSubscribed = true
		}
	}
	if ch.AvatarMediaID != nil {
		if u := r.s.resolveAvatarURLs(ctx, viewer, []uuid.UUID{*ch.AvatarMediaID})[*ch.AvatarMediaID]; u != "" {
			out.AvatarURL = &u
		}
	}
	return out
}

func (r *endScreenResolver) poll(ctx context.Context, postID uuid.UUID) *CardPoll {
	if r.s.postCard(ctx, postID, r.viewer) == nil || r.s.pgStore == nil {
		return nil
	}
	poll, err := r.s.GetPoll(ctx, postID, r.viewer)
	if err != nil || poll == nil {
		return nil
	}
	return &CardPoll{PostID: postID, PollData: poll}
}

// ── impressions and clicks ─────────────────────────────────────────────────

// statDeduper answers "is this the first time today" for one key.
type statDeduper interface {
	FirstToday(ctx context.Context, key string) (bool, error)
}

// StatViewer is who an impression or click is from: the signed-in user, or
// for an anonymous viewer the hashed client IP the gateway forwards (the
// gateway's X-Device-Id is only stamped for signed-in tokens). Both empty:
// counted without dedupe; the gateway's per-IP limiter is the rate limit.
type StatViewer struct {
	UserID *uuid.UUID
	AnonID string
}

func (v StatViewer) key() string {
	if v.UserID != nil && *v.UserID != uuid.Nil {
		return "u:" + v.UserID.String()
	}
	if v.AnonID != "" {
		return "a:" + v.AnonID
	}
	return ""
}

// RecordEndScreenEvent is POST .../end-screens/:elementId/impression|click.
func (s *Service) RecordEndScreenEvent(ctx context.Context, postID, elementID uuid.UUID, viewer StatViewer, click bool) error {
	return s.recordStat(ctx, "es", postID, elementID, viewer, click)
}

// RecordCardEvent is POST .../cards/:cardId/impression|click.
func (s *Service) RecordCardEvent(ctx context.Context, postID, cardID uuid.UUID, viewer StatViewer, click bool) error {
	return s.recordStat(ctx, "card", postID, cardID, viewer, click)
}

// recordStat: the detail's gate, the element must be the post's (and the
// post one that shows elements to this viewer), the owner's own views are
// not counted, one impression and one click per viewer per element per UTC
// day, then the upsert.
func (s *Service) recordStat(ctx context.Context, kind string, postID, elementID uuid.UUID, viewer StatViewer, click bool) error {
	sub, owner, err := s.endScreenReadSubject(ctx, postID, viewer.UserID)
	if err != nil {
		return err
	}
	if sub.MadeForKids && !owner {
		return ErrEndScreenElementNotFound
	}
	var onPost bool
	if kind == "es" {
		onPost, err = s.endScreens.EndScreenOnPost(ctx, postID, elementID)
	} else {
		onPost, err = s.endScreens.CardOnPost(ctx, postID, elementID)
	}
	if err != nil {
		return err
	}
	if !onPost {
		return ErrEndScreenElementNotFound
	}
	if owner {
		return nil
	}
	day := s.clock().UTC()
	if key := viewer.key(); key != "" && s.statDedupe != nil {
		action := "imp"
		if click {
			action = "click"
		}
		first, err := s.statDedupe.FirstToday(ctx, fmt.Sprintf("%s_stat:%s:%s:%s:%s", kind, action, elementID, key, day.Format("2006-01-02")))
		if err != nil {
			// Fail open, as the product-tag counters do: a Redis blip
			// over-counts a little rather than dropping real views.
			slog.WarnContext(ctx, "end screen stat dedupe unavailable; counting", "kind", kind, "element_id", elementID, "err", err)
		} else if !first {
			return nil
		}
	}
	if kind == "es" {
		return s.endScreens.BumpEndScreenStat(ctx, postID, elementID, day, click)
	}
	return s.endScreens.BumpCardStat(ctx, postID, elementID, day, click)
}

// ValidEndScreenTitle: at most MaxEndScreenTitle runes once trimmed.
func ValidEndScreenTitle(t *string) bool {
	return t == nil || utf8.RuneCountInString(strings.TrimSpace(*t)) <= MaxEndScreenTitle
}

// endScreenStatDedupeTTL outlives the UTC day in the key, so a key set just
// before midnight still stops a repeat until its day is over.
const endScreenStatDedupeTTL = 26 * time.Hour

// redisStatDeduper is statDeduper over Redis SETNX.
type redisStatDeduper struct{ rdb *redis.Client }

func (d redisStatDeduper) FirstToday(ctx context.Context, key string) (bool, error) {
	return d.rdb.SetNX(ctx, key, "1", endScreenStatDedupeTTL).Result()
}
