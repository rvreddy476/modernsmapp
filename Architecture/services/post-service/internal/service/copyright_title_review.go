package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/titlematch"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Copyright title review (founder quick wins, 2026-10-24; migration 073).
//
// A LONG VIDEO longer than TitleReviewMinDuration whose title, description
// or hashtags name a title on the staff catalogue (titlematch), uploaded by
// someone who is not one of that title's verified owner channels, is
// published but held: the hold is a post_restrictions row (source
// title_review) written in the publishing transaction, so every viewer path
// already hides it, and the publication's PostCreated is withheld, so
// nothing fans out, indexes or notifies. A reviewer clears it (the withheld
// PostCreated goes out then, once) or blocks it (it stays hidden; the
// author is told; trust-safety records a copyright strike).
//
// Reels, flicks and photo posts are never held by this rule. A video still
// transcoding at publication has no length yet: a match then holds it
// (fail closed) without queueing or telling anyone until the transcode
// measures it (ResolveTitleReviewMeasurement).

// TitleReviewMinDuration: only a video LONGER than this is held.
const TitleReviewMinDuration = 20 * time.Minute

// titleCatalogueTTL bounds how stale this replica's catalogue snapshot may
// be; a write on this replica refreshes it at once.
const titleCatalogueTTL = time.Minute

var (
	// ErrCopyrightReviewTier: a members-only tier may not be set or kept on
	// a post that is held or blocked (409 COPYRIGHT_REVIEW).
	ErrCopyrightReviewTier = postgres.ErrPostCopyrightHeld
	// ErrUploadsPaused is the errors.Is target for every *UploadsPausedError.
	ErrUploadsPaused = errors.New("long-video uploads are paused")
	// ErrInvalidCopyrightTitle: a catalogue write failed validation (400).
	ErrInvalidCopyrightTitle = errors.New("invalid copyright title")
	// ErrCopyrightReviewUnavailable: no review store is wired (503).
	ErrCopyrightReviewUnavailable = errors.New("copyright review is unavailable")
)

// UploadsPausedError: trust-safety reports the author's long-video uploads
// paused (three upheld copyright blocks in 90 days) — 403 UPLOADS_PAUSED.
type UploadsPausedError struct{ Until time.Time }

func (e *UploadsPausedError) Error() string {
	return "long-video uploads are paused until " + e.Until.UTC().Format(time.RFC3339)
}

// Is makes errors.Is(err, ErrUploadsPaused) hold.
func (e *UploadsPausedError) Is(target error) bool { return target == ErrUploadsPaused }

// BlockReason is what a parked draft or scheduled post carries.
func (e *UploadsPausedError) BlockReason() string {
	return BlockReasonUploadsPaused + " until " + e.Until.UTC().Format(time.RFC3339)
}

// Block reasons for parked background publications.
const (
	BlockReasonUploadsPaused   = "uploads_paused"
	BlockReasonCopyrightReview = "copyright_review"
)

// IsTerminalCopyrightError reports a refusal a background publication must
// park on rather than retry: uploads paused, or a tier on a held post.
func IsTerminalCopyrightError(err error) bool {
	return errors.Is(err, ErrUploadsPaused) || errors.Is(err, ErrCopyrightReviewTier)
}

// copyrightBlockReason renders the parked reason for a terminal copyright
// refusal ("" for anything else).
func copyrightBlockReason(err error) string {
	var paused *UploadsPausedError
	switch {
	case errors.As(err, &paused):
		return paused.BlockReason()
	case errors.Is(err, ErrCopyrightReviewTier):
		return BlockReasonCopyrightReview
	}
	return ""
}

// titleReviewStore is what this file needs from the Postgres store.
type titleReviewStore interface {
	ListActiveCopyrightTitles(ctx context.Context) ([]postgres.CopyrightTitle, error)
	ListCopyrightTitles(ctx context.Context, f postgres.CopyrightTitleFilter) ([]postgres.CopyrightTitle, string, error)
	GetCopyrightTitle(ctx context.Context, id uuid.UUID) (*postgres.CopyrightTitle, error)
	CreateCopyrightTitle(ctx context.Context, id uuid.UUID, w postgres.CopyrightTitleWrite, actor uuid.UUID) (*postgres.CopyrightTitle, error)
	UpdateCopyrightTitle(ctx context.Context, id uuid.UUID, w postgres.CopyrightTitleWrite, actor uuid.UUID, note string) (*postgres.CopyrightTitle, error)
	ListTitleReviews(ctx context.Context, state, cursor string, limit int) ([]postgres.TitleReviewQueueItem, string, error)
	GetTitleReview(ctx context.Context, id uuid.UUID) (*postgres.TitleReview, error)
	DecideTitleReview(ctx context.Context, in postgres.DecideTitleReviewInput, announce postgres.TitleReviewAnnounce) (*postgres.TitleReviewOutcome, error)
	ResolveTitleReviewDuration(ctx context.Context, postID uuid.UUID, durationSec int, hold, autoBlock bool, reason string, announce postgres.TitleReviewAnnounce) (postgres.TitleReviewDurationOutcome, error)
	RequestTitleReview(ctx context.Context, postID, authorID uuid.UUID, note string) (*postgres.TitleReview, error)
	TitleReviewsByPost(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]*postgres.TitleReview, error)
	CopyrightMatchPostAuthor(ctx context.Context, postID uuid.UUID) (uuid.UUID, error)
	ApplyOwnerMatch(ctx context.Context, in postgres.OwnerMatchInput) (*postgres.OwnerMatchResult, error)
}

// titleCatalogue is one replica's snapshot: the matcher, each entry's
// owner channels and each entry's owner request (auto_block, rights_owner).
type titleCatalogue struct {
	matcher *titlematch.Matcher
	owners  map[uuid.UUID]map[uuid.UUID]bool
	entries map[uuid.UUID]titleEntryMeta
	loaded  time.Time
}

// titleEntryMeta is what a hold copies from its matched entry.
type titleEntryMeta struct {
	autoBlock   bool
	rightsOwner *string
}

// SetCopyrightAutoBlockEnabled is the owner-registered auto-block kill
// switch (COPYRIGHT_AUTO_BLOCK_ENABLED, default true). Off, an upload that
// matches an auto_block entry — or an owner match asking for a block — is
// held for a reviewer instead of blocked by the system.
func (s *Service) SetCopyrightAutoBlockEnabled(v bool) { s.autoBlockDisabled = !v }

type titleCatalogueCache struct {
	mu  sync.Mutex
	cur *titleCatalogue
}

// SetCopyrightTitleHoldEnabled is the kill switch
// (COPYRIGHT_TITLE_HOLD_ENABLED, default true). Off, no new hold is
// placed; existing holds stay until a reviewer decides them.
func (s *Service) SetCopyrightTitleHoldEnabled(v bool) { s.titleHoldDisabled = !v }

func (s *Service) titleReviewStore() titleReviewStore {
	if s.titleReviews != nil {
		return s.titleReviews
	}
	if s.pgStore != nil {
		return s.pgStore
	}
	return nil
}

// titleCatalogueSnapshot returns this replica's catalogue, reloading it
// when older than titleCatalogueTTL. A load failure is an error: the
// publication it serves fails rather than skipping the rule.
func (s *Service) titleCatalogueSnapshot(ctx context.Context) (*titleCatalogue, error) {
	store := s.titleReviewStore()
	if store == nil {
		return nil, ErrCopyrightReviewUnavailable
	}
	now := s.clock()
	s.titleCatalogue.mu.Lock()
	cur := s.titleCatalogue.cur
	s.titleCatalogue.mu.Unlock()
	if cur != nil && now.Sub(cur.loaded) < titleCatalogueTTL {
		return cur, nil
	}
	rows, err := store.ListActiveCopyrightTitles(ctx)
	if err != nil {
		return nil, fmt.Errorf("copyright title catalogue: %w", err)
	}
	next := buildTitleCatalogue(rows, now)
	s.titleCatalogue.mu.Lock()
	s.titleCatalogue.cur = next
	s.titleCatalogue.mu.Unlock()
	return next, nil
}

func buildTitleCatalogue(rows []postgres.CopyrightTitle, now time.Time) *titleCatalogue {
	entries := make([]titlematch.Entry, 0, len(rows))
	owners := make(map[uuid.UUID]map[uuid.UUID]bool, len(rows))
	meta := make(map[uuid.UUID]titleEntryMeta, len(rows))
	for _, r := range rows {
		if !r.Active {
			continue
		}
		entries = append(entries, titlematch.Entry{ID: r.ID, Title: r.Title, Aliases: r.Aliases})
		set := make(map[uuid.UUID]bool, len(r.OwnerChannelIDs))
		for _, id := range r.OwnerChannelIDs {
			set[id] = true
		}
		owners[r.ID] = set
		meta[r.ID] = titleEntryMeta{autoBlock: r.AutoBlock, rightsOwner: r.RightsOwner}
	}
	return &titleCatalogue{matcher: titlematch.New(entries), owners: owners, entries: meta, loaded: now}
}

// dropTitleCatalogue forces the next publication on this replica to reload.
func (s *Service) dropTitleCatalogue() {
	s.titleCatalogue.mu.Lock()
	s.titleCatalogue.cur = nil
	s.titleCatalogue.mu.Unlock()
}

// titleHoldSubject is what the rule judges about one publication.
type titleHoldSubject struct {
	AuthorID    uuid.UUID
	ContentType string
	// DurationSec is the longest attached video in seconds; 0 = not
	// measured yet.
	DurationSec int
	Title       string
	Description string
	Hashtags    []string
}

// decideTitleHold applies the rule. nil, nil: publish normally.
func (s *Service) decideTitleHold(ctx context.Context, sub titleHoldSubject) (*postgres.TitleReviewHold, error) {
	if s.titleHoldDisabled || !isLongVideoContentType(sub.ContentType) {
		return nil, nil
	}
	if sub.DurationSec > 0 && time.Duration(sub.DurationSec)*time.Second <= TitleReviewMinDuration {
		return nil, nil
	}
	cat, err := s.titleCatalogueSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	matches := cat.matcher.MatchAll(sub.Title, sub.Description, sub.Hashtags)
	if len(matches) == 0 {
		return nil, nil
	}
	// The uploader's channel decides ownership. A lookup that fails holds
	// the video (fail closed); an author with no channel owns nothing.
	var channelID uuid.UUID
	if s.channels != nil {
		ch, err := s.channels.GetChannelByUserID(ctx, sub.AuthorID)
		if err != nil {
			return nil, fmt.Errorf("copyright title review: author channel: %w", err)
		}
		if ch != nil {
			channelID = ch.ID
		}
	}
	// The first title the uploader does not own decides — except that a
	// title whose owner asked for blocking wins over one that only goes to a
	// reviewer: the video is blocked for it whichever the text names first.
	var pick *titlematch.Match
	for i := range matches {
		m := &matches[i]
		if channelID != uuid.Nil && cat.owners[m.EntryID][channelID] {
			continue // a verified owner of this title
		}
		if pick == nil {
			pick = m
		}
		if s.entryAutoBlocks(cat.entries[m.EntryID]) {
			pick = m
			break
		}
	}
	if pick == nil {
		return nil, nil
	}
	meta := cat.entries[pick.EntryID]
	hold := &postgres.TitleReviewHold{
		ReviewID: uuid.New(), TitleID: pick.EntryID, MatchedTitle: pick.Title, MatchedTerm: pick.Term,
		MatchedField: pick.Field, AnnounceOnClear: true, RightsOwner: meta.rightsOwner,
		AutoBlock: s.entryAutoBlocks(meta),
	}
	if sub.DurationSec > 0 {
		d := sub.DurationSec
		hold.DurationSeconds = &d
	} else {
		hold.AwaitingDuration = true
	}
	return hold, nil
}

// entryAutoBlocks: the entry's owner asked for blocking, names themself,
// and the auto-block kill switch is on.
func (s *Service) entryAutoBlocks(m titleEntryMeta) bool {
	return !s.autoBlockDisabled && m.autoBlock && m.rightsOwner != nil && strings.TrimSpace(*m.rightsOwner) != ""
}

// requireLongVideoUploads refuses a long-video publication while
// trust-safety reports the author's long-video uploads paused. Called on
// every long-video publication after requirePublishStanding; the answer is
// the standing client's cached one, so it costs no second call.
func (s *Service) requireLongVideoUploads(ctx context.Context, authorID uuid.UUID, contentType string) error {
	if !isLongVideoContentType(contentType) {
		return nil
	}
	res, err := s.publishStandingResult(ctx, authorID)
	if err != nil || res == nil {
		return err
	}
	if res.LongVideoPausedUntil != nil && res.LongVideoPausedUntil.After(s.clock()) {
		return &UploadsPausedError{Until: res.LongVideoPausedUntil.UTC()}
	}
	return nil
}

// applyCreateTitleHold is CreatePost's half of the rule, for a publication
// that goes live now: it sets p.TitleReviewWrite when the post must be
// held, and refuses a members-only tier on a held post.
func (s *Service) applyCreateTitleHold(ctx context.Context, p *postgres.Post, maxDuration int, hasVideo bool) error {
	if !hasVideo {
		return nil // a "long video" with no video has nothing to review
	}
	hold, err := s.decideTitleHold(ctx, titleHoldSubject{
		AuthorID: p.AuthorID, ContentType: p.ContentType, DurationSec: maxDuration,
		Title: p.Title, Description: p.Text, Hashtags: p.Hashtags,
	})
	if err != nil || hold == nil {
		return err
	}
	if p.TierRequiredID != nil {
		return ErrCopyrightReviewTier
	}
	p.TitleReviewWrite = hold
	return nil
}

// createdEventFor is the PostCreated a fresh post writes with its row:
// none without a producer, none for a scheduled post (the schedule worker
// emits it at publication) and none for a post held for copyright title
// review (a reviewer's clear emits it).
func (s *Service) createdEventFor(ctx context.Context, p *postgres.Post, policy *DistributionPolicy, maxDuration, maxHeight int) (string, interface{}) {
	if s.producer == nil || p.PublishAt != nil || p.TitleReviewWrite != nil {
		return "", nil
	}
	return events.PostCreated, s.buildPostCreatedPayload(ctx, p, policy, maxDuration, maxHeight, 1)
}

// postHasVideo reports whether any attached media is a video.
func postHasVideo(p *postgres.Post) bool {
	for _, m := range p.Media {
		if m.Kind == "video" {
			return true
		}
	}
	return false
}

// scheduledTitleHold is the scheduled flip's half of the rule: the pause,
// then the hold (nil = publish normally), then the members-only refusal.
func (s *Service) scheduledTitleHold(ctx context.Context, p *postgres.Post, maxDuration int) (*postgres.TitleReviewHold, error) {
	if err := s.requireLongVideoUploads(ctx, p.AuthorID, p.ContentType); err != nil {
		return nil, err
	}
	if !postHasVideo(p) {
		return nil, nil
	}
	hold, err := s.decideTitleHold(ctx, titleHoldSubject{
		AuthorID: p.AuthorID, ContentType: p.ContentType, DurationSec: maxDuration,
		Title: p.Title, Description: p.Text, Hashtags: p.Hashtags,
	})
	if err != nil || hold == nil {
		return nil, err
	}
	if p.TierRequiredID != nil {
		return nil, ErrCopyrightReviewTier
	}
	return hold, nil
}

// ── The transcode measures a waiting video ──────────────────────────────

// ResolveTitleReviewMeasurement settles a hold that was waiting for the
// video's length, once the transcode has measured it: still a long video
// longer than the threshold → queued for a reviewer and the author told;
// otherwise the hold is lifted by the system and the withheld PostCreated
// goes out. A post with no waiting review is a no-op.
func (s *Service) ResolveTitleReviewMeasurement(ctx context.Context, postID uuid.UUID, durationSec int, contentType string) error {
	store := s.titleReviewStore()
	if store == nil || durationSec <= 0 {
		return nil
	}
	hold := isLongVideoContentType(contentType) && time.Duration(durationSec)*time.Second > TitleReviewMinDuration
	reason := ""
	if !isLongVideoContentType(contentType) {
		reason = postgres.TitleReviewReasonNotLongVideo
	}
	var post *postgres.Post
	if !hold && s.pgStore != nil {
		p, err := s.pgStore.GetPost(ctx, postID)
		if err != nil {
			return err
		}
		post = p
	}
	now := s.clock().UTC()
	outcome, err := store.ResolveTitleReviewDuration(ctx, postID, durationSec, hold, !s.autoBlockDisabled, reason, s.titleReviewAnnounce(ctx, post, now))
	if err != nil {
		return err
	}
	if outcome == postgres.TitleReviewDurationCleared {
		s.afterTitleReviewRelease(ctx, postID, post, true)
	}
	if outcome != postgres.TitleReviewDurationNone {
		s.InvalidatePostBodyCache(ctx, postID)
		slog.Info("copyright title review: duration measured", "post_id", postID, "duration_s", durationSec, "outcome", outcome)
	}
	return nil
}

// titleReviewAnnounce builds the withheld PostCreated for a cleared post:
// the same payload a fresh publication carries (buildPostCreatedPayload),
// stamped with the revision the clear produced and EligibleFrom = now, so
// no day before the clear earns. post is the live post with its media
// (GetPost, read before the transaction); nil — the post is gone or
// deleted — announces nothing.
func (s *Service) titleReviewAnnounce(ctx context.Context, post *postgres.Post, now time.Time) postgres.TitleReviewAnnounce {
	return func(rev int64) (string, interface{}, error) {
		// The same rule as a fresh publication: no producer, no PostCreated.
		if s.producer == nil || post == nil {
			return "", nil, nil
		}
		live := *post
		live.ActiveRestrictionCount = 0
		policy, perr := ParseDistributionPolicy(live.Distribution)
		if perr != nil {
			policy = nil
		}
		maxDuration, maxHeight := s.maxVideoDurationAndHeight(ctx, &live)
		pc := s.buildPostCreatedPayload(ctx, &live, policy, maxDuration, maxHeight, rev)
		from := now
		pc.EligibleFrom = &from
		return events.PostCreated, pc, nil
	}
}

// afterTitleReviewRelease is the post-commit half of a clear: the body
// cache, the author's counts and — when the withheld PostCreated went out
// — the rest of what a publication does at birth (mentions, trending,
// hashtag counters, the live pub/sub).
func (s *Service) afterTitleReviewRelease(ctx context.Context, postID uuid.UUID, post *postgres.Post, announced bool) {
	s.InvalidatePostBodyCache(ctx, postID)
	if post == nil {
		return
	}
	if s.rdb != nil {
		s.rdb.Del(ctx, fmt.Sprintf("post:author-counts:%s", post.AuthorID))
	}
	if announced {
		s.NudgeOutbox()
		s.announcePublishedPost(post, post.MentionUsernames)
	}
}

// ── Reviewer decisions ──────────────────────────────────────────────────

// DecideCopyrightReview applies a reviewer's clear or block. A clear lifts
// the hold and publishes the withheld PostCreated once; a block keeps the
// hold (and trust-safety records a copyright strike from the event).
func (s *Service) DecideCopyrightReview(ctx context.Context, reviewID, actorID uuid.UUID, decision, note string) (*postgres.TitleReviewOutcome, error) {
	store := s.titleReviewStore()
	if store == nil {
		return nil, ErrCopyrightReviewUnavailable
	}
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != postgres.TitleReviewDecisionClear && decision != postgres.TitleReviewDecisionBlock {
		return nil, fmt.Errorf("%w: decision must be clear or block", postgres.ErrTitleReviewInvalid)
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 2000 {
		return nil, fmt.Errorf("%w: note is longer than 2000 characters", postgres.ErrTitleReviewInvalid)
	}
	r, err := store.GetTitleReview(ctx, reviewID)
	if err != nil {
		return nil, err
	}
	var post *postgres.Post
	if decision == postgres.TitleReviewDecisionClear && s.pgStore != nil {
		if post, err = s.pgStore.GetPost(ctx, r.PostID); err != nil {
			return nil, err
		}
	}
	out, err := store.DecideTitleReview(ctx, postgres.DecideTitleReviewInput{
		ReviewID: reviewID, Decision: decision, ActorID: actorID, Note: note,
	}, s.titleReviewAnnounce(ctx, post, s.clock().UTC()))
	if err != nil {
		return nil, err
	}
	if decision == postgres.TitleReviewDecisionClear {
		s.afterTitleReviewRelease(ctx, r.PostID, post, out.Announced)
	} else {
		s.InvalidatePostBodyCache(ctx, r.PostID)
		s.NudgeOutbox()
	}
	return out, nil
}

// ListCopyrightReviews is the reviewer queue (oldest first).
func (s *Service) ListCopyrightReviews(ctx context.Context, state, cursor string, limit int) ([]postgres.TitleReviewQueueItem, string, error) {
	store := s.titleReviewStore()
	if store == nil {
		return nil, "", ErrCopyrightReviewUnavailable
	}
	if state == "" {
		state = postgres.TitleReviewPending
	}
	return store.ListTitleReviews(ctx, state, cursor, limit)
}

// ── The catalogue ───────────────────────────────────────────────────────

// CopyrightTitleInput is a create, or a partial update (nil = unchanged).
type CopyrightTitleInput struct {
	Title           *string
	Aliases         *[]string
	Language        *string
	ClearLanguage   bool
	OwnerChannelIDs *[]uuid.UUID
	Active          *bool
	// AutoBlock: the rights owner registered the title and asked for copies
	// to be blocked (requires RightsOwner).
	AutoBlock        *bool
	RightsOwner      *string
	ClearRightsOwner bool
	Note             string
}

var titleLanguageRE = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// Catalogue limits (the table's CHECKs, judged here first).
const (
	maxTitleRunes    = 200
	maxTitleAliases  = 50
	maxTitleOwners   = 100
	maxLanguageRunes = 16
)

func validateTitleTerm(field, term string) (string, error) {
	term = strings.TrimSpace(term)
	if term == "" || utf8.RuneCountInString(term) > maxTitleRunes {
		return "", fmt.Errorf("%w: %s must be 1-%d characters", ErrInvalidCopyrightTitle, field, maxTitleRunes)
	}
	if !titlematch.ValidTerm(term) {
		return "", fmt.Errorf("%w: %s %q has fewer than %d letters or digits", ErrInvalidCopyrightTitle, field, term, titlematch.MinTermRunes)
	}
	return term, nil
}

// normalizeTitleWrite validates a full write.
func normalizeTitleWrite(w postgres.CopyrightTitleWrite) (postgres.CopyrightTitleWrite, error) {
	title, err := validateTitleTerm("title", w.Title)
	if err != nil {
		return w, err
	}
	w.Title = title
	if len(w.Aliases) > maxTitleAliases {
		return w, fmt.Errorf("%w: at most %d aliases", ErrInvalidCopyrightTitle, maxTitleAliases)
	}
	seen := map[string]bool{strings.Join(titlematch.Tokens(title), " "): true}
	aliases := make([]string, 0, len(w.Aliases))
	for _, a := range w.Aliases {
		a, err := validateTitleTerm("alias", a)
		if err != nil {
			return w, err
		}
		key := strings.Join(titlematch.Tokens(a), " ")
		if seen[key] {
			continue
		}
		seen[key] = true
		aliases = append(aliases, a)
	}
	w.Aliases = aliases
	if w.Language != nil {
		lang := strings.TrimSpace(*w.Language)
		switch {
		case lang == "":
			w.Language = nil
		case len(lang) > maxLanguageRunes || !titleLanguageRE.MatchString(lang):
			return w, fmt.Errorf("%w: language must be a language tag such as ta or hi-IN", ErrInvalidCopyrightTitle)
		default:
			w.Language = &lang
		}
	}
	if len(w.OwnerChannelIDs) > maxTitleOwners {
		return w, fmt.Errorf("%w: at most %d owner channels", ErrInvalidCopyrightTitle, maxTitleOwners)
	}
	owners := make([]uuid.UUID, 0, len(w.OwnerChannelIDs))
	ownerSeen := map[uuid.UUID]bool{}
	for _, id := range w.OwnerChannelIDs {
		if id == uuid.Nil {
			return w, fmt.Errorf("%w: owner channel id is empty", ErrInvalidCopyrightTitle)
		}
		if !ownerSeen[id] {
			ownerSeen[id] = true
			owners = append(owners, id)
		}
	}
	w.OwnerChannelIDs = owners
	if w.RightsOwner != nil {
		owner := strings.Join(strings.Fields(*w.RightsOwner), " ")
		switch {
		case owner == "":
			w.RightsOwner = nil
		case utf8.RuneCountInString(owner) > maxTitleRunes || strings.IndexFunc(owner, unicode.IsControl) >= 0:
			return w, fmt.Errorf("%w: rights_owner must be 1-%d printable characters", ErrInvalidCopyrightTitle, maxTitleRunes)
		default:
			w.RightsOwner = &owner
		}
	}
	// An automatic block is the OWNER's request: the entry says whose.
	if w.AutoBlock && w.RightsOwner == nil {
		return w, fmt.Errorf("%w: auto_block needs rights_owner (who registered the title and asked for blocking)", ErrInvalidCopyrightTitle)
	}
	return w, nil
}

// CreateCopyrightTitle adds a catalogue entry (active unless said).
func (s *Service) CreateCopyrightTitle(ctx context.Context, in CopyrightTitleInput, actorID uuid.UUID) (*postgres.CopyrightTitle, error) {
	store := s.titleReviewStore()
	if store == nil {
		return nil, ErrCopyrightReviewUnavailable
	}
	if in.Title == nil {
		return nil, fmt.Errorf("%w: title is required", ErrInvalidCopyrightTitle)
	}
	w := postgres.CopyrightTitleWrite{Title: *in.Title, Language: in.Language, Active: true, RightsOwner: in.RightsOwner}
	if in.Aliases != nil {
		w.Aliases = *in.Aliases
	}
	if in.OwnerChannelIDs != nil {
		w.OwnerChannelIDs = *in.OwnerChannelIDs
	}
	if in.Active != nil {
		w.Active = *in.Active
	}
	if in.AutoBlock != nil {
		w.AutoBlock = *in.AutoBlock
	}
	w, err := normalizeTitleWrite(w)
	if err != nil {
		return nil, err
	}
	t, err := store.CreateCopyrightTitle(ctx, uuid.New(), w, actorID)
	if err != nil {
		return nil, err
	}
	s.dropTitleCatalogue()
	return t, nil
}

// UpdateCopyrightTitle changes an entry; Active=false disables it.
func (s *Service) UpdateCopyrightTitle(ctx context.Context, id uuid.UUID, in CopyrightTitleInput, actorID uuid.UUID) (*postgres.CopyrightTitle, error) {
	store := s.titleReviewStore()
	if store == nil {
		return nil, ErrCopyrightReviewUnavailable
	}
	cur, err := store.GetCopyrightTitle(ctx, id)
	if err != nil {
		return nil, err
	}
	w := postgres.CopyrightTitleWrite{
		Title: cur.Title, Aliases: cur.Aliases, Language: cur.Language,
		OwnerChannelIDs: cur.OwnerChannelIDs, Active: cur.Active,
		AutoBlock: cur.AutoBlock, RightsOwner: cur.RightsOwner,
	}
	if in.AutoBlock != nil {
		w.AutoBlock = *in.AutoBlock
	}
	if in.ClearRightsOwner {
		w.RightsOwner = nil
	} else if in.RightsOwner != nil {
		w.RightsOwner = in.RightsOwner
	}
	if in.Title != nil {
		w.Title = *in.Title
	}
	if in.Aliases != nil {
		w.Aliases = *in.Aliases
	}
	if in.ClearLanguage {
		w.Language = nil
	} else if in.Language != nil {
		w.Language = in.Language
	}
	if in.OwnerChannelIDs != nil {
		w.OwnerChannelIDs = *in.OwnerChannelIDs
	}
	if in.Active != nil {
		w.Active = *in.Active
	}
	w, err = normalizeTitleWrite(w)
	if err != nil {
		return nil, err
	}
	note := strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(note) > 2000 {
		return nil, fmt.Errorf("%w: note is longer than 2000 characters", ErrInvalidCopyrightTitle)
	}
	t, err := store.UpdateCopyrightTitle(ctx, id, w, actorID, note)
	if err != nil {
		return nil, err
	}
	s.dropTitleCatalogue()
	return t, nil
}

// ListCopyrightTitles pages the catalogue.
func (s *Service) ListCopyrightTitles(ctx context.Context, f postgres.CopyrightTitleFilter) ([]postgres.CopyrightTitle, string, error) {
	store := s.titleReviewStore()
	if store == nil {
		return nil, "", ErrCopyrightReviewUnavailable
	}
	return store.ListCopyrightTitles(ctx, f)
}

// ── The author ──────────────────────────────────────────────────────────

// CopyrightReviewView is the author's view of their video's review:
// copyright_review on their own post detail and uploads rows. Absent for
// every other viewer, and for a video that was never flagged (or whose
// provisional hold lifted itself before anyone was told).
type CopyrightReviewView struct {
	State        string     `json:"state"`
	MatchedTitle string     `json:"matched_title"`
	CreatedAt    time.Time  `json:"created_at"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
	// ReviewRequestedAt is when the author sent "I own the rights".
	ReviewRequestedAt *time.Time `json:"review_requested_at,omitempty"`
	// RightsOwner names the registered owner the video matched, when the
	// entry names one (migration 075).
	RightsOwner string `json:"rights_owner,omitempty"`
	// Reason: "owner_registered" (blocked at once for the registered
	// owner) or "mute_requested" (the owner asked for a mute; in review).
	Reason string `json:"reason,omitempty"`
}

func copyrightReviewView(r *postgres.TitleReview) *CopyrightReviewView {
	if r == nil {
		return nil
	}
	if r.State == postgres.TitleReviewCleared && !r.AuthorNotified {
		return nil // lifted by the duration rule before the author was told
	}
	v := &CopyrightReviewView{
		State: r.State, MatchedTitle: r.MatchedTitle, CreatedAt: r.CreatedAt,
		DecidedAt: r.DecidedAt, ReviewRequestedAt: r.AuthorRequestedAt,
	}
	if r.RightsOwner != nil {
		v.RightsOwner = *r.RightsOwner
	}
	if r.Reason != nil {
		v.Reason = *r.Reason
	}
	return v
}

// attachCopyrightReviews fills CopyrightReview on the author's own long
// videos. Best effort: a read failure leaves the field off.
func (s *Service) attachCopyrightReviews(ctx context.Context, ownerID uuid.UUID, details []*PostDetail) {
	store := s.titleReviewStore()
	if store == nil {
		return
	}
	var ids []uuid.UUID
	for _, d := range details {
		if d != nil && d.Post != nil && d.Post.AuthorID == ownerID && isLongVideoContentType(d.Post.ContentType) {
			ids = append(ids, d.Post.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	reviews, err := store.TitleReviewsByPost(ctx, ids)
	if err != nil {
		slog.Warn("copyright review overlay failed", "error", err)
		return
	}
	for _, d := range details {
		if d != nil && d.Post != nil && d.Post.AuthorID == ownerID {
			d.CopyrightReview = copyrightReviewView(reviews[d.Post.ID])
		}
	}
}

// RequestCopyrightReview records the author's "I own the rights" note on
// their pending review.
func (s *Service) RequestCopyrightReview(ctx context.Context, postID, authorID uuid.UUID, note string) (*CopyrightReviewView, error) {
	store := s.titleReviewStore()
	if store == nil {
		return nil, ErrCopyrightReviewUnavailable
	}
	note = strings.TrimSpace(note)
	if note == "" || utf8.RuneCountInString(note) > 1000 {
		return nil, fmt.Errorf("%w: note must be 1-1000 characters", postgres.ErrTitleReviewInvalid)
	}
	r, err := store.RequestTitleReview(ctx, postID, authorID, note)
	if err != nil {
		return nil, err
	}
	return copyrightReviewView(r), nil
}
