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
	"unicode"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Video credits — cast & crew on a long video (2026-10-23, migration 070).

	  POST  /v1/posts, PATCH /v1/posts/:id, both draft kinds
	        credits [{name, role, user_id?}] (max 30, order kept; PATCH
	        replaces the list, [] clears, omitted leaves it), genres (max 3
	        slugs from GET /v1/posts/genres), release_year (1900 … next
	        year), production_house (<= 100). Long videos only: anything else
	        is 400 CREDITS_NOT_ALLOWED.
	  GET   /v1/posts/genres                    [{slug, label}]
	  GET   /v1/credits/requests?cursor=&limit= my pending tags
	  POST  /v1/credits/:id/accept | /decline   {status}
	  DELETE /v1/credits/:id                    "Remove me from this video"
	  GET   /v1/creators/:id/appearances        "Appears in"

	A tag (user_id) starts pending and asks that person (post.credit.requested
	→ notification credit_request), except:

	  * the uploader crediting themselves — accepted at once;
	  * an account that does not exist, that the uploader cannot see, or with
	    a block either way — stored as plain text, silently: the response is
	    the same as for any plain credit, so nothing tells the uploader why;
	  * a tag over the uploader's limit (20 new pending tags per 24 h) —
	    plain text, counted in meta.credit_tags_skipped.

	If the checks cannot be made (identity-profile or graph unreachable),
	every tag is stored as plain text: never a link that was not checked.

	Who sees what (GET /v1/posts/:id): every credit's name and role for
	everyone; the account (`user`) only once that person accepted; `status`
	and `user_id` only for the post's owner, so the edit form can send the
	tags back and keep them.
*/

var (
	// ErrCreditsNotAllowed: credits, genres, release year or production
	// house on anything but a long video.
	ErrCreditsNotAllowed = errors.New("credits, genres, release_year and production_house are for long videos only")
	// ErrTooManyCredits: more than MaxCredits.
	ErrTooManyCredits = fmt.Errorf("a video may carry at most %d credits", MaxCredits)
	// ErrInvalidCredit: a name or role empty, too long or with control
	// characters, or a nil user_id.
	ErrInvalidCredit = errors.New("each credit needs a name (1-80 characters) and a role (1-40 characters) without control characters")
	// ErrInvalidGenre: a genre outside GET /v1/posts/genres, or more than
	// MaxGenres of them.
	ErrInvalidGenre = fmt.Errorf("genres must be at most %d of the slugs GET /v1/posts/genres lists", MaxGenres)
	// ErrInvalidReleaseYear: outside 1900 … next year.
	ErrInvalidReleaseYear = errors.New("release_year must be between 1900 and next year")
	// ErrInvalidProductionHouse: longer than 100 characters, or control
	// characters.
	ErrInvalidProductionHouse = fmt.Errorf("production_house may be at most %d characters without control characters", MaxProductionHouseRunes)
	// ErrCreditNotFound: the consent routes' one refusal (404).
	ErrCreditNotFound = postgres.ErrCreditNotFound
)

// Limits.
const (
	MaxCredits              = 30
	MaxCreditNameRunes      = 80
	MaxCreditRoleRunes      = 40
	MaxGenres               = 3
	MaxProductionHouseRunes = 100
	MinReleaseYear          = 1900
)

// Genre is one entry of GET /v1/posts/genres.
type Genre struct {
	Slug  string `json:"slug"`
	Label string `json:"label"`
}

// videoGenres is the closed genre list, in the order the picker shows it.
var videoGenres = []Genre{
	{"action", "Action"}, {"comedy", "Comedy"}, {"drama", "Drama"}, {"thriller", "Thriller"},
	{"romance", "Romance"}, {"horror", "Horror"}, {"sci-fi", "Sci-fi"}, {"fantasy", "Fantasy"},
	{"animation", "Animation"}, {"documentary", "Documentary"}, {"family", "Family"}, {"music", "Music"},
	{"crime", "Crime"}, {"mystery", "Mystery"}, {"biography", "Biography"}, {"history", "History"},
	{"sports", "Sports"}, {"reality", "Reality"}, {"talk", "Talk"}, {"news", "News"},
}

// VideoGenres returns a copy of the genre list.
func VideoGenres() []Genre {
	return append([]Genre(nil), videoGenres...)
}

func isVideoGenre(slug string) bool {
	for _, g := range videoGenres {
		if g.Slug == slug {
			return true
		}
	}
	return false
}

// hasUnsafeRune reports a character no credit text may hold: control
// characters, line / paragraph separators and the bidi overrides that can
// make a name render as something else.
func hasUnsafeRune(s string) bool {
	for _, r := range s {
		switch {
		case unicode.IsControl(r), unicode.In(r, unicode.Zl, unicode.Zp):
			return true
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
			return true
		}
	}
	return false
}

func validCreditText(s string, max int) bool {
	n := utf8.RuneCountInString(s)
	return n >= 1 && n <= max && utf8.ValidString(s) && !hasUnsafeRune(s)
}

// ValidateCredits trims and checks a credit list (order kept). nil stays
// nil.
func ValidateCredits(raw []postgres.CreditInput) ([]postgres.CreditInput, error) {
	if raw == nil {
		return nil, nil
	}
	if len(raw) > MaxCredits {
		return nil, ErrTooManyCredits
	}
	out := make([]postgres.CreditInput, 0, len(raw))
	for _, c := range raw {
		name, role := strings.TrimSpace(c.Name), strings.TrimSpace(c.Role)
		if !validCreditText(name, MaxCreditNameRunes) || !validCreditText(role, MaxCreditRoleRunes) {
			return nil, ErrInvalidCredit
		}
		if c.UserID != nil && *c.UserID == uuid.Nil {
			return nil, ErrInvalidCredit
		}
		item := postgres.CreditInput{Name: name, Role: role}
		if c.UserID != nil {
			id := *c.UserID
			item.UserID = &id
		}
		out = append(out, item)
	}
	return out, nil
}

// NormalizeGenres lowercases, dedupes and checks genre slugs. nil stays
// nil; [] stays [] (clears on PATCH).
func NormalizeGenres(raw []string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, g := range raw {
		slug := strings.ToLower(strings.TrimSpace(g))
		if !isVideoGenre(slug) {
			return nil, ErrInvalidGenre
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	if len(out) > MaxGenres {
		return nil, ErrInvalidGenre
	}
	return out, nil
}

// ValidateReleaseYear: 1900 … next year (now's).
func ValidateReleaseYear(year int, now time.Time) error {
	if year < MinReleaseYear || year > now.Year()+1 {
		return ErrInvalidReleaseYear
	}
	return nil
}

// NormalizeProductionHouse trims; "" is none (nil).
func NormalizeProductionHouse(raw string) (*string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(v) > MaxProductionHouseRunes || hasUnsafeRune(v) {
		return nil, ErrInvalidProductionHouse
	}
	return &v, nil
}

// VideoCreditFields are the credit fields of one create (or draft publish).
type VideoCreditFields struct {
	Credits         []postgres.CreditInput
	Genres          []string
	ReleaseYear     *int
	ProductionHouse string
}

// empty reports a request that sets none of them ([] counts as none).
func (f VideoCreditFields) empty() bool {
	return len(f.Credits) == 0 && len(f.Genres) == 0 && f.ReleaseYear == nil && strings.TrimSpace(f.ProductionHouse) == ""
}

// validatedVideoCredits checks the create path's credit fields for a post of
// contentType (already normalized): every value first, then the kind, so a
// malformed list is refused for what it is.
func validatedVideoCredits(contentType string, f VideoCreditFields, now time.Time) (VideoCreditFields, *string, error) {
	var out VideoCreditFields
	credits, err := ValidateCredits(f.Credits)
	if err != nil {
		return out, nil, err
	}
	genres, err := NormalizeGenres(f.Genres)
	if err != nil {
		return out, nil, err
	}
	if f.ReleaseYear != nil {
		if err := ValidateReleaseYear(*f.ReleaseYear, now); err != nil {
			return out, nil, err
		}
	}
	house, err := NormalizeProductionHouse(f.ProductionHouse)
	if err != nil {
		return out, nil, err
	}
	if !f.empty() && !isLongVideoContentType(contentType) {
		return out, nil, ErrCreditsNotAllowed
	}
	out.Credits, out.Genres, out.ReleaseYear = credits, genres, f.ReleaseYear
	if len(out.Genres) == 0 {
		out.Genres = nil
	}
	if len(out.Credits) == 0 {
		out.Credits = nil
	}
	return out, house, nil
}

// creditLinker decides which tagged accounts an uploader may link: those
// that exist (as the uploader sees them) with no block either way. Any
// failure is an error, and the caller then links nobody.
type creditLinker interface {
	Linkable(ctx context.Context, uploaderID uuid.UUID, userIDs []uuid.UUID) (linkable map[uuid.UUID]bool, uploaderName string, err error)
}

// creditStore is the storage slice the credit reads and the consent routes
// use (store/postgres/credits.go); an interface so the guards are testable
// without a database.
type creditStore interface {
	ListPostCredits(ctx context.Context, postID uuid.UUID) ([]postgres.PostCredit, error)
	RespondToCredit(ctx context.Context, creditID, callerID uuid.UUID, action string) (string, error)
	ListCreditRequests(ctx context.Context, userID uuid.UUID, cursor *postgres.CreditCursor, limit int) ([]postgres.CreditRequestRow, *postgres.CreditCursor, error)
	ListAppearancePostIDs(ctx context.Context, userID uuid.UUID, cursor *postgres.CreditCursor, limit int) ([]uuid.UUID, *postgres.CreditCursor, error)
}

// resolveCreditCandidates turns a validated list into what the store
// writes: a self-credit is Self, a tag the linker allows keeps its user_id,
// every other tag becomes plain text — with no trace of why.
func (s *Service) resolveCreditCandidates(ctx context.Context, uploaderID uuid.UUID, credits []postgres.CreditInput) *postgres.CreditsWrite {
	w := &postgres.CreditsWrite{Candidates: make([]postgres.CreditCandidate, 0, len(credits))}
	var others []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, c := range credits {
		if c.UserID != nil && *c.UserID != uploaderID && !seen[*c.UserID] {
			seen[*c.UserID] = true
			others = append(others, *c.UserID)
		}
	}
	linkable := map[uuid.UUID]bool{}
	if len(others) > 0 {
		var err error
		if s.creditLinks == nil {
			err = errors.New("credit link checks are not configured")
		} else {
			linkable, w.UploaderName, err = s.creditLinks.Linkable(ctx, uploaderID, others)
		}
		if err != nil {
			slog.WarnContext(ctx, "credits: link checks unresolved; storing every tag as plain text", "uploader_id", uploaderID, "err", err)
			linkable = map[uuid.UUID]bool{}
		}
	}
	for _, c := range credits {
		cand := postgres.CreditCandidate{Name: c.Name, Role: c.Role}
		switch {
		case c.UserID == nil:
		case *c.UserID == uploaderID:
			id := *c.UserID
			cand.UserID, cand.Self = &id, true
		case linkable[*c.UserID]:
			id := *c.UserID
			cand.UserID = &id
		}
		w.Candidates = append(w.Candidates, cand)
	}
	return w
}

// defaultCreditLinker asks identity-profile (as the uploader: an account the
// uploader cannot see does not resolve) and graph-service's either-direction
// block lookup. Both must answer.
type defaultCreditLinker struct{ s *Service }

func (l defaultCreditLinker) Linkable(ctx context.Context, uploaderID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]bool, string, error) {
	s := l.s
	if s.profileServiceURL == "" || s.graphServiceURL == "" {
		return nil, "", errors.New("identity-profile or graph-service is not configured")
	}
	ids := make([]string, 0, len(userIDs)+1)
	ids = append(ids, uploaderID.String())
	for _, id := range userIDs {
		ids = append(ids, id.String())
	}
	profiles, err := s.fetchCommentProfiles(ctx, &uploaderID, ids)
	if err != nil {
		return nil, "", fmt.Errorf("profile lookup: %w", err)
	}
	blocked, err := s.blockedAny(ctx, uploaderID, userIDs)
	if err != nil {
		return nil, "", err
	}
	out := make(map[uuid.UUID]bool, len(userIDs))
	for _, id := range userIDs {
		_, exists := profiles[id]
		out[id] = exists && !blocked[id]
	}
	name := ""
	if p, ok := profiles[uploaderID]; ok {
		name = strings.TrimSpace(p.DisplayName)
		if name == "" {
			name = p.Username
		}
	}
	return out, name, nil
}

// blockedAnyPath is graph-service's either-direction block lookup.
const blockedAnyPath = "/v1/internal/graph/blocked-any"

// blockedAny returns the candidates with a block either way with userID.
// A failure, a non-200 or a body without the list is an error, never "not
// blocked".
func (s *Service) blockedAny(ctx context.Context, userID uuid.UUID, candidates []uuid.UUID) (map[uuid.UUID]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	body, err := json.Marshal(map[string]any{"user_id": userID, "candidate_ids": candidates})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.graphServiceURL, "/")+blockedAnyPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graph blocked-any: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("graph blocked-any returned %d", resp.StatusCode)
	}
	var out struct {
		BlockedUserIDs *[]uuid.UUID `json:"blocked_user_ids"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return nil, fmt.Errorf("graph blocked-any body: %w", err)
	}
	if out.BlockedUserIDs == nil {
		return nil, errors.New("graph blocked-any answered without blocked_user_ids")
	}
	blocked := make(map[uuid.UUID]bool, len(*out.BlockedUserIDs))
	for _, id := range *out.BlockedUserIDs {
		blocked[id] = true
	}
	return blocked, nil
}

// CreditUser is the account on an accepted credit.
type CreditUser struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
}

// CreditView is one credit as a reader sees it. UserID and Status are the
// owner's alone; User is there for everyone once the person accepted.
type CreditView struct {
	ID     uuid.UUID   `json:"id"`
	Name   string      `json:"name"`
	Role   string      `json:"role"`
	UserID *uuid.UUID  `json:"user_id,omitempty"`
	User   *CreditUser `json:"user,omitempty"`
	Status string      `json:"status,omitempty"`
}

// creditProfilesFor resolves accepted accounts for the read (as the viewer,
// so an account the viewer cannot see stays plain text). Best-effort.
func (s *Service) creditProfilesFor(ctx context.Context, viewerID *uuid.UUID, ids []uuid.UUID) map[uuid.UUID]commentProfile {
	if len(ids) == 0 {
		return nil
	}
	if s.creditProfiles != nil {
		p, err := s.creditProfiles(ctx, viewerID, ids)
		if err != nil {
			return nil
		}
		return p
	}
	if s.profileServiceURL == "" {
		return nil
	}
	raw := make([]string, len(ids))
	for i, id := range ids {
		raw[i] = id.String()
	}
	p, err := s.fetchCommentProfiles(ctx, viewerID, raw)
	if err != nil {
		slog.DebugContext(ctx, "credits: account hydration skipped", "err", err)
		return nil
	}
	return p
}

// BuildCreditViews is the read shape (pure). isOwner decides status and
// user_id; profiles names the accepted accounts this viewer may see — an
// accepted credit whose account did not resolve reads as plain text.
func BuildCreditViews(credits []postgres.PostCredit, isOwner bool, profiles map[uuid.UUID]commentProfile) []CreditView {
	out := make([]CreditView, 0, len(credits))
	for _, c := range credits {
		v := CreditView{ID: c.ID, Name: c.Name, Role: c.Role}
		if isOwner {
			v.Status = c.Status
			if c.UserID != nil {
				id := *c.UserID
				v.UserID = &id
			}
		}
		if c.UserID != nil && c.Status == postgres.CreditAccepted {
			if p, ok := profiles[*c.UserID]; ok {
				u := &CreditUser{ID: *c.UserID, Username: p.Username, DisplayName: p.DisplayName}
				if p.AvatarMediaID != nil {
					u.AvatarURL = fmt.Sprintf("/v1/media/%s/serve/avatar", p.AvatarMediaID)
				}
				v.User = u
			}
		}
		out = append(out, v)
	}
	return out
}

// creditViewsFor builds the views for one post and viewer.
func (s *Service) creditViewsFor(ctx context.Context, credits []postgres.PostCredit, authorID uuid.UUID, viewerID *uuid.UUID) []CreditView {
	var accepted []uuid.UUID
	for _, c := range credits {
		if c.Status == postgres.CreditAccepted && c.UserID != nil {
			accepted = append(accepted, *c.UserID)
		}
	}
	isOwner := viewerID != nil && *viewerID == authorID
	return BuildCreditViews(credits, isOwner, s.creditProfilesFor(ctx, viewerID, accepted))
}

// attachCredits puts the credit list on a long video's detail (always an
// array there); other kinds get none. Best-effort: a failed read leaves it
// absent rather than failing the post.
func (s *Service) attachCredits(ctx context.Context, detail *PostDetail, viewerID *uuid.UUID) {
	if detail == nil || detail.Post == nil || !isLongVideoContentType(detail.ContentType) || s.credits == nil {
		return
	}
	credits, err := s.credits.ListPostCredits(ctx, detail.ID)
	if err != nil {
		slog.WarnContext(ctx, "credits: read failed", "post_id", detail.ID, "err", err)
		return
	}
	views := s.creditViewsFor(ctx, credits, detail.AuthorID, viewerID)
	detail.Credits = &views
}

// CreatedCredits is a create's credit answer.
type CreatedCredits struct {
	Credits *[]CreditView
}

// CreatedPostWithCredits is the owner's view of the credits a create just
// wrote (or, for an idempotent replay, the stored ones) and how many tags
// went over the daily limit. Nil credits for anything but a long video.
func (s *Service) CreatedPostWithCredits(ctx context.Context, p *postgres.Post) (CreatedCredits, int) {
	if p == nil || !isLongVideoContentType(p.ContentType) {
		return CreatedCredits{}, 0
	}
	owner := p.AuthorID
	if p.CreditsWrite != nil {
		views := s.creditViewsFor(ctx, p.CreditsWrite.Result.Credits, p.AuthorID, &owner)
		return CreatedCredits{Credits: &views}, p.CreditsWrite.Result.Skipped
	}
	detail := &PostDetail{Post: p}
	s.attachCredits(ctx, detail, &owner)
	return CreatedCredits{Credits: detail.Credits}, 0
}

// CreditAnswer is the body of the consent routes.
type CreditAnswer struct {
	Status string `json:"status"`
}

// RespondToCredit is accept / decline / remove by the tagged person.
func (s *Service) RespondToCredit(ctx context.Context, callerID, creditID uuid.UUID, action string) (*CreditAnswer, error) {
	if s.credits == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	if callerID == uuid.Nil {
		return nil, ErrCreditNotFound
	}
	status, err := s.credits.RespondToCredit(ctx, creditID, callerID, action)
	if err != nil {
		return nil, err
	}
	s.NudgeOutbox()
	return &CreditAnswer{Status: status}, nil
}

// CreditRequestPost is the video a request is on.
type CreditRequestPost struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	CoverURL    *string   `json:"cover_url"`
	ChannelName *string   `json:"channel_name"`
}

// CreditRequestFrom is the uploader who asked.
type CreditRequestFrom struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
}

// CreditRequest is one row of GET /v1/credits/requests.
type CreditRequest struct {
	ID        uuid.UUID         `json:"id"`
	Role      string            `json:"role"`
	Name      string            `json:"name"`
	Post      CreditRequestPost `json:"post"`
	From      CreditRequestFrom `json:"from"`
	CreatedAt time.Time         `json:"created_at"`
}

// Credit list page sizes.
const (
	DefaultCreditListLimit = 20
	MaxCreditListLimit     = 50
)

func clampCreditLimit(limit int) int {
	if limit <= 0 {
		return DefaultCreditListLimit
	}
	if limit > MaxCreditListLimit {
		return MaxCreditListLimit
	}
	return limit
}

// ListCreditRequests is the caller's pending tags, newest first.
func (s *Service) ListCreditRequests(ctx context.Context, callerID uuid.UUID, rawCursor string, limit int) ([]CreditRequest, string, error) {
	if s.credits == nil {
		return nil, "", ErrAuthoringStoreUnavailable
	}
	cursor, err := postgres.DecodeCreditCursor(rawCursor)
	if err != nil {
		return nil, "", err
	}
	rows, next, err := s.credits.ListCreditRequests(ctx, callerID, cursor, clampCreditLimit(limit))
	if err != nil {
		return nil, "", err
	}
	var uploaders []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, r := range rows {
		if !seen[r.UploaderID] {
			seen[r.UploaderID] = true
			uploaders = append(uploaders, r.UploaderID)
		}
	}
	profiles := s.creditProfilesFor(ctx, &callerID, uploaders)
	out := make([]CreditRequest, 0, len(rows))
	for _, r := range rows {
		from := CreditRequestFrom{ID: r.UploaderID}
		if p, ok := profiles[r.UploaderID]; ok {
			from.Username, from.DisplayName = p.Username, p.DisplayName
			if p.AvatarMediaID != nil {
				from.AvatarURL = fmt.Sprintf("/v1/media/%s/serve/avatar", p.AvatarMediaID)
			}
		}
		out = append(out, CreditRequest{
			ID: r.ID, Role: r.Role, Name: r.Name, CreatedAt: r.CreatedAt, From: from,
			Post: CreditRequestPost{ID: r.PostID, Title: r.PostTitle, CoverURL: r.CoverURL, ChannelName: r.ChannelName},
		})
	}
	nextCursor := ""
	if next != nil {
		nextCursor = postgres.EncodeCreditCursor(*next)
	}
	return out, nextCursor, nil
}

// ListAppearances is "Appears in": the public long videos on which userID's
// credit is accepted, newest first, as this viewer may see them. A profile
// the viewer may not see (private to them, blocked, hidden) has no
// appearances; every row then passes the ordinary batch read gate (blocks
// with the uploader, private and hidden uploaders, the age gate) and the
// list's own rules again (public, long video, not members-only), so a row
// the viewer could not open is never listed. Rows dropped by the gate make a
// short page; the cursor still advances.
func (s *Service) ListAppearances(ctx context.Context, userID uuid.UUID, viewerID *uuid.UUID, rawCursor string, limit int) ([]PostDetail, string, error) {
	if s.credits == nil {
		return nil, "", ErrAuthoringStoreUnavailable
	}
	cursor, err := postgres.DecodeCreditCursor(rawCursor)
	if err != nil {
		return nil, "", err
	}
	isSelf := viewerID != nil && *viewerID == userID
	if !isSelf && !s.appearanceProfileVisible(ctx, viewerID, userID) {
		return []PostDetail{}, "", nil
	}
	ids, next, err := s.credits.ListAppearancePostIDs(ctx, userID, cursor, clampCreditLimit(limit))
	if err != nil {
		return nil, "", err
	}
	nextCursor := ""
	if next != nil {
		nextCursor = postgres.EncodeCreditCursor(*next)
	}
	if len(ids) == 0 {
		return []PostDetail{}, nextCursor, nil
	}
	read := s.appearancePosts
	if read == nil {
		read = s.GetPostsByIDs
	}
	byID, err := read(ctx, ids, viewerID)
	if err != nil {
		return nil, "", err
	}
	out := make([]PostDetail, 0, len(ids))
	page := make([]*PostDetail, 0, len(ids))
	for _, id := range ids {
		d := byID[id]
		if d == nil || d.Post == nil || !appearanceListable(d.Post) {
			continue
		}
		page = append(page, d)
	}
	if s.channels != nil {
		viewer := uuid.Nil
		if viewerID != nil {
			viewer = *viewerID
		}
		s.attachChannelRefs(ctx, viewer, page)
	}
	for _, d := range page {
		out = append(out, *d)
	}
	return out, nextCursor, nil
}

// appearanceListable is the list's own rule, checked again on every row the
// gate returned: public, published, approved, a long video, not members-only.
func appearanceListable(p *postgres.Post) bool {
	return p.Visibility == "public" && isLongVideoContentType(p.ContentType) && p.TierRequiredID == nil &&
		p.DeletedAt == nil && p.PublishAt == nil && p.EffectiveReviewStatus() == "approved"
}

func (s *Service) appearanceProfileVisible(ctx context.Context, viewerID *uuid.UUID, userID uuid.UUID) bool {
	if s.appearanceProfileGate != nil {
		return s.appearanceProfileGate(ctx, viewerID, userID)
	}
	return s.canViewAuthor(ctx, viewerID, userID)
}

// postCreditSearchNames is the names / roles a create carries to search,
// from the list it is about to write.
func postCreditSearchNames(w *postgres.CreditsWrite) (names, roles []string) {
	if w == nil {
		return nil, nil
	}
	for _, c := range w.Candidates {
		names = append(names, c.Name)
		roles = append(roles, c.Role)
	}
	return names, roles
}
