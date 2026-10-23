package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/atpost/post-service/internal/actionlinks"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

// The Action button (pinned contract, 23 Oct 2026).
//
// A post of any kind, and a profile, may carry one labelled external link.
// Every write — create, PATCH, both kinds of draft at publication, PATCH
// /v1/channels/me — goes through prepareAction, in this order (cheapest
// refusal first, nothing written on any refusal):
//
//  1. shape: the label is one of actionlinks.Labels (INVALID_ACTION_LABEL)
//     and the URL passes actionlinks.CheckURL (INVALID_ACTION_URL,
//     ACTION_LINK_SHORTENER, ACTION_LINK_GROUP_INVITE);
//  2. eligibility: account >= 7 days old, email verified, no active
//     strikes (403 ACTION_NOT_ELIGIBLE, details.missing names each failed
//     item; an item that cannot be established counts as missing);
//  3. safety: Google Web Risk (ACTION_LINK_UNSAFE). Without a checker a
//     link is stored "unchecked" outside production and refused in
//     production (503 ACTION_LINK_CHECK_UNAVAILABLE); a checker that
//     fails is 503 everywhere. Our own domains skip the lookup.
//
// The read side is attachActions, run inside redactForViewer — the one
// per-viewer pass every surface that answers post rows already makes — so
// no list can forget it: viewers get the action only while it is active,
// and never its raw URL; the author gets url, status and status_reason;
// a members-only row redacted for this viewer has none.

var (
	ErrInvalidActionLabel = errors.New("action.label must be one of the labels in GET /v1/posts/action-labels")
	// ErrInvalidActionURL wraps actionlinks.ErrInvalidURL (errors.Is both).
	ErrInvalidActionURL           = fmt.Errorf("action.url: %w", actionlinks.ErrInvalidURL)
	ErrActionLinkUnsafe           = errors.New("this link was flagged as unsafe by our safety check")
	ErrActionLinkCheckUnavailable = errors.New("the link safety check is unavailable right now; nothing was saved, try again")
	ErrActionNotEligible          = errors.New("this account cannot add an action button yet")
	ErrActionLinkNotFound         = errors.New("action link not found")
)

// Eligibility items, exactly as the wire names them in details.missing.
const (
	ActionMissingAccountAge     = "account_age"
	ActionMissingEmailVerified  = "email_verified"
	ActionMissingNoActiveStrike = "no_active_strikes"
)

// DefaultActionMinAccountAge is the contract's 7 days. Only a non-production
// deployment may lower it (ACTION_LINK_MIN_ACCOUNT_AGE_HOURS, main.go).
const DefaultActionMinAccountAge = 7 * 24 * time.Hour

// Report threshold: this many distinct scam_link reporters within the
// window put a live link under review.
const (
	ActionReportThreshold = 3
	ActionReportWindow    = 7 * 24 * time.Hour
)

// ActionTapDedupeWindow: one viewer (or one IP + user agent) counts once
// per link per hour.
const ActionTapDedupeWindow = time.Hour

const actionEligibilityTimeout = 2500 * time.Millisecond

// ActionNotEligibleError is ErrActionNotEligible with the failed items.
// EligibleAt is when the account turns old enough (set only when
// account_age is missing and the account's age is known).
type ActionNotEligibleError struct {
	Missing    []string
	EligibleAt *time.Time
}

func (e *ActionNotEligibleError) Error() string { return ErrActionNotEligible.Error() }

// Is matches ErrActionNotEligible.
func (e *ActionNotEligibleError) Is(target error) bool { return target == ErrActionNotEligible }

// ActionInput is the wire {"label","url"} on every write.
type ActionInput struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// actionLinkStore is the slice of the store the Action button needs.
type actionLinkStore interface {
	ReplacePostActionLink(ctx context.Context, postID, authorID uuid.UUID, w *postgres.ActionLinkWrite) (*postgres.ActionLink, error)
	ReplaceProfileActionLink(ctx context.Context, ownerID uuid.UUID, w *postgres.ActionLinkWrite) (*postgres.ActionLink, error)
	ActionLinksForPosts(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]*postgres.ActionLink, error)
	ActionLinkForProfile(ctx context.Context, ownerID uuid.UUID) (*postgres.ActionLink, error)
	GetResolvableActionLink(ctx context.Context, id uuid.UUID) (*postgres.ResolvableActionLink, error)
	RecordActionLinkClick(ctx context.Context, id uuid.UUID, day time.Time) error
	ActionLinkStats(ctx context.Context, postID, ownerID *uuid.UUID, since time.Time) (int64, int64, error)
	ApplyActionLinkReport(ctx context.Context, in postgres.ActionLinkReportInput) (postgres.ActionLinkReportOutcome, error)
	ReviewActionLink(ctx context.Context, id, actor uuid.UUID, decision, note string) (*postgres.ActionLink, error)
	ListActionLinksByStatus(ctx context.Context, status string, before time.Time, limit int) ([]*postgres.ActionLink, error)
	ClaimActionLinksForRecheck(ctx context.Context, now, staleBefore, claimUntil time.Time, limit int) ([]*postgres.ActionLink, error)
	RecordActionLinkVerdict(ctx context.Context, id uuid.UUID, safety string, checkedAt time.Time) (*postgres.ActionLink, error)
}

// ActionTapDeduper answers whether key is the first tap in its window.
type ActionTapDeduper interface {
	FirstTap(ctx context.Context, key string, window time.Duration) (bool, error)
}

// redisTapDeduper is ActionTapDeduper over Redis SET NX.
type redisTapDeduper struct{ rdb *redis.Client }

func (d redisTapDeduper) FirstTap(ctx context.Context, key string, window time.Duration) (bool, error) {
	return d.rdb.SetNX(ctx, key, "1", window).Result()
}

// ActionLinkConfig wires the Action button (main.go; tests).
type ActionLinkConfig struct {
	// Checker is the safety lookup; nil = none (see the file comment).
	Checker actionlinks.Checker
	// Production refuses unchecked links.
	Production bool
	// OwnDomains are allowed without a lookup (in-app links).
	OwnDomains []string
	// Accounts and Emails are the eligibility facts; nil = unknown, which
	// counts as missing.
	Accounts actionlinks.AccountSource
	Emails   actionlinks.EmailSource
	// MinAccountAge is DefaultActionMinAccountAge when zero.
	MinAccountAge time.Duration
	// TapDedupe overrides the Redis deduper (tests).
	TapDedupe ActionTapDeduper
	// Store overrides the Postgres store (tests).
	Store actionLinkStore
}

// actionConfig is the wired configuration.
type actionConfig struct {
	ActionLinkConfig
}

// SetActionLinks wires the Action button. The store defaults to Postgres,
// the deduper to Redis.
func (s *Service) SetActionLinks(cfg ActionLinkConfig) {
	if cfg.MinAccountAge <= 0 {
		cfg.MinAccountAge = DefaultActionMinAccountAge
	}
	if cfg.Store != nil {
		s.actionLinks = cfg.Store
	}
	if cfg.TapDedupe == nil && s.rdb != nil {
		cfg.TapDedupe = redisTapDeduper{rdb: s.rdb}
	}
	s.actionCfg = &actionConfig{ActionLinkConfig: cfg}
}

// ActionCheckerName is the configured checker's name, "" for none (logs).
func (s *Service) ActionCheckerName() string {
	if s.actionCfg == nil || s.actionCfg.Checker == nil {
		return ""
	}
	return s.actionCfg.Checker.Name()
}

func (s *Service) actionConf() *actionConfig {
	if s.actionCfg == nil {
		return &actionConfig{ActionLinkConfig: ActionLinkConfig{MinAccountAge: DefaultActionMinAccountAge}}
	}
	return s.actionCfg
}

// ValidateActionShape is step 1 alone: the label and the URL rules. Drafts
// run it when they are saved (no network); prepareAction runs it first.
func ValidateActionShape(in *ActionInput) (string, actionlinks.Checked, error) {
	if in == nil {
		return "", actionlinks.Checked{}, ErrInvalidActionLabel
	}
	if _, ok := actionlinks.LabelText(in.Label); !ok {
		return "", actionlinks.Checked{}, ErrInvalidActionLabel
	}
	checked, err := actionlinks.CheckURL(in.URL)
	if err != nil {
		if errors.Is(err, actionlinks.ErrInvalidURL) {
			return "", actionlinks.Checked{}, ErrInvalidActionURL
		}
		return "", actionlinks.Checked{}, err
	}
	return in.Label, checked, nil
}

// ErrInvalidActionShape: an action that is neither an object nor null
// (400 INVALID_REQUEST).
var ErrInvalidActionShape = errors.New(`action must be an object {"label","url"} or null`)

// BlockReasonActionRefused parks a scheduled draft whose action can never
// publish as saved (not eligible, unsafe, a rule its URL now fails).
const BlockReasonActionRefused = "action_refused"

// ParseActionPatch reads a raw `action` of a PATCH-style write: absent
// (present false), null (clear), or an object (in). Anything else is
// ErrInvalidActionShape. Unknown keys inside the object are refused too.
func ParseActionPatch(raw json.RawMessage) (in *ActionInput, clear, present bool, err error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, false, false, nil
	}
	if bytes.Equal(raw, []byte("null")) {
		return nil, true, true, nil
	}
	var a ActionInput
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, false, true, ErrInvalidActionShape
	}
	return &a, false, true, nil
}

// IsTerminalActionError is an action refusal retrying cannot fix (every
// one but the safety check being unavailable).
func IsTerminalActionError(err error) bool {
	return errors.Is(err, ErrInvalidActionLabel) || errors.Is(err, actionlinks.ErrInvalidURL) ||
		errors.Is(err, actionlinks.ErrShortener) || errors.Is(err, actionlinks.ErrGroupInvite) ||
		errors.Is(err, ErrActionLinkUnsafe) || errors.Is(err, ErrActionNotEligible) ||
		errors.Is(err, ErrInvalidActionShape)
}

// prepareAction runs the three steps for authorID and returns the write.
func (s *Service) prepareAction(ctx context.Context, authorID uuid.UUID, in *ActionInput) (*postgres.ActionLinkWrite, error) {
	label, checked, err := ValidateActionShape(in)
	if err != nil {
		return nil, err
	}
	if err := s.checkActionEligibility(ctx, authorID); err != nil {
		return nil, err
	}
	safety, at, err := s.checkActionSafety(ctx, checked)
	if err != nil {
		return nil, err
	}
	return &postgres.ActionLinkWrite{Label: label, URL: checked.URL, Domain: checked.Domain, SafetyStatus: safety, SafetyCheckedAt: at}, nil
}

// checkActionEligibility asks the three facts at once, bounded, and lists
// every one that is not established (fail closed per item).
func (s *Service) checkActionEligibility(ctx context.Context, userID uuid.UUID) error {
	cfg := s.actionConf()
	ctx, cancel := context.WithTimeout(ctx, actionEligibilityTimeout)
	defer cancel()
	now := s.clock()

	var (
		wg                        sync.WaitGroup
		ageOK, emailOK, strikesOK bool
		eligibleAt                *time.Time
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		if cfg.Accounts == nil {
			return
		}
		info, err := cfg.Accounts.Account(ctx, userID)
		if err != nil {
			slog.WarnContext(ctx, "action eligibility: account read failed", "user_id", userID, "err", err)
			return
		}
		if !info.Found || !info.Active {
			return
		}
		at := info.CreatedAt.Add(cfg.MinAccountAge)
		if now.Before(at) {
			eligibleAt = &at
			return
		}
		ageOK = true
	}()
	go func() {
		defer wg.Done()
		if cfg.Emails == nil {
			return
		}
		ok, err := cfg.Emails.EmailVerified(ctx, userID)
		if err != nil {
			slog.WarnContext(ctx, "action eligibility: email read failed", "user_id", userID, "err", err)
			return
		}
		emailOK = ok
	}()
	go func() {
		defer wg.Done()
		if s.standing == nil {
			// The same dev-only opt-out publication honours
			// (PUBLISH_REQUIRE_STANDING_CHECK=false): no checker, not
			// required. Production always has a checker.
			strikesOK = !s.requireStandingCheck
			return
		}
		res, err := s.standing.Check(ctx, userID)
		if err != nil || res == nil {
			return
		}
		strikesOK = len(res.ActiveStrikes) == 0
	}()
	wg.Wait()

	var missing []string
	if !ageOK {
		missing = append(missing, ActionMissingAccountAge)
	}
	if !emailOK {
		missing = append(missing, ActionMissingEmailVerified)
	}
	if !strikesOK {
		missing = append(missing, ActionMissingNoActiveStrike)
	}
	if len(missing) == 0 {
		return nil
	}
	e := &ActionNotEligibleError{Missing: missing}
	if !ageOK && eligibleAt != nil {
		e.EligibleAt = eligibleAt
	}
	return e
}

// checkActionSafety is step 3: the stored safety_status and when it was
// established.
func (s *Service) checkActionSafety(ctx context.Context, c actionlinks.Checked) (string, *time.Time, error) {
	cfg := s.actionConf()
	now := s.clock().UTC()
	if actionlinks.IsOwnDomain(c.Domain, cfg.OwnDomains) {
		return postgres.ActionSafetySafe, &now, nil
	}
	if cfg.Checker == nil {
		if cfg.Production {
			return "", nil, ErrActionLinkCheckUnavailable
		}
		return postgres.ActionSafetyUnchecked, nil, nil
	}
	verdict, err := cfg.Checker.Check(ctx, c.URL)
	if err != nil {
		slog.WarnContext(ctx, "action link safety check failed", "checker", cfg.Checker.Name(), "domain", c.Domain, "err", err)
		return "", nil, ErrActionLinkCheckUnavailable
	}
	if verdict.Unsafe {
		slog.InfoContext(ctx, "action link refused: unsafe", "domain", c.Domain, "threats", verdict.ThreatTypes)
		return "", nil, ErrActionLinkUnsafe
	}
	return postgres.ActionSafetySafe, &now, nil
}

// actionHref is the redirect path a client opens (web /l/<id>, Android the
// in-app interstitial).
func actionHref(id uuid.UUID) string { return "/l/" + id.String() }

// ActionViewFor is the read shape of l: nil for a removed link, and for a
// viewer (author false) anything but an active one.
func ActionViewFor(l *postgres.ActionLink, author bool) *postgres.ActionView {
	if l == nil || l.Status == postgres.ActionStatusRemoved {
		return nil
	}
	if !author && l.Status != postgres.ActionStatusActive {
		return nil
	}
	text, _ := actionlinks.LabelText(l.Label)
	v := &postgres.ActionView{ID: l.ID, Label: l.Label, LabelText: text, Domain: l.Domain, Href: actionHref(l.ID)}
	if author {
		v.URL = l.URL
		v.Status = l.Status
		if l.Status != postgres.ActionStatusActive && l.StatusReason != nil {
			v.StatusReason = *l.StatusReason
		}
	}
	return v
}

// attachActions sets each row's Action for viewer: one store read per page.
// A row redacted for this viewer (members-only) gets none; a failed read
// gets none on every row (a button that cannot be checked is not shown).
// Without a store (unit rigs) rows are left as they are.
func (s *Service) attachActions(ctx context.Context, viewer uuid.UUID, posts []*postgres.Post) {
	if s.actionLinks == nil {
		return
	}
	ids := make([]uuid.UUID, 0, len(posts))
	for _, p := range posts {
		if p == nil {
			continue
		}
		p.Action = nil
		if !p.BodyRedacted {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	links, err := s.actionLinks.ActionLinksForPosts(ctx, ids)
	if err != nil {
		slog.WarnContext(ctx, "action buttons not attached: lookup failed", "rows", len(ids), "err", err)
		return
	}
	for _, p := range posts {
		if p == nil || p.BodyRedacted {
			continue
		}
		p.Action = ActionViewFor(links[p.ID], viewer != uuid.Nil && viewer == p.AuthorID)
	}
}

// AttachAuthorActions is attachActions for the author's own rows (Creator
// Hub libraries, create / edit answers), which skip the members-only pass.
func (s *Service) AttachAuthorActions(ctx context.Context, authorID uuid.UUID, posts []*postgres.Post) {
	s.attachActions(ctx, authorID, posts)
}

// SetPostAction sets (in) or removes (clear) the action on the caller's
// post, after prepareAction. Answers the author's view (nil when removed).
func (s *Service) SetPostAction(ctx context.Context, callerID, postID uuid.UUID, in *ActionInput, clear bool) (*postgres.ActionView, error) {
	if s.actionLinks == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	var w *postgres.ActionLinkWrite
	if !clear {
		var err error
		if w, err = s.prepareAction(ctx, callerID, in); err != nil {
			return nil, err
		}
	}
	return s.writePostAction(ctx, callerID, postID, w)
}

// writePostAction stores an already prepared write (nil removes).
func (s *Service) writePostAction(ctx context.Context, callerID, postID uuid.UUID, w *postgres.ActionLinkWrite) (*postgres.ActionView, error) {
	if s.actionLinks == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	link, err := s.actionLinks.ReplacePostActionLink(ctx, postID, callerID, w)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, ErrPostNotFound
	case errors.Is(err, postgres.ErrPostEditNotOwned):
		return nil, ErrNotPostAuthor
	case err != nil:
		return nil, err
	}
	return ActionViewFor(link, true), nil
}

// SetProfileAction sets (in) or removes (clear) the caller's profile
// action.
func (s *Service) SetProfileAction(ctx context.Context, userID uuid.UUID, in *ActionInput, clear bool) (*postgres.ActionView, error) {
	if s.actionLinks == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	var w *postgres.ActionLinkWrite
	if !clear {
		var err error
		if w, err = s.prepareAction(ctx, userID, in); err != nil {
			return nil, err
		}
	}
	link, err := s.actionLinks.ReplaceProfileActionLink(ctx, userID, w)
	if err != nil {
		return nil, err
	}
	return ActionViewFor(link, true), nil
}

// ProfileActionView is the profile button as viewer sees it (nil = none).
// A failed read answers none.
func (s *Service) ProfileActionView(ctx context.Context, ownerID, viewer uuid.UUID) *postgres.ActionView {
	if s.actionLinks == nil || ownerID == uuid.Nil {
		return nil
	}
	l, err := s.actionLinks.ActionLinkForProfile(ctx, ownerID)
	if err != nil {
		slog.WarnContext(ctx, "profile action not attached: lookup failed", "owner_id", ownerID, "err", err)
		return nil
	}
	return ActionViewFor(l, viewer == ownerID)
}

// ResolvedActionLink is GET /v1/links/:id.
type ResolvedActionLink struct {
	ID     uuid.UUID `json:"id"`
	URL    string    `json:"url"`
	Domain string    `json:"domain"`
	Label  string    `json:"label"`
	Status string    `json:"status"`
}

// ResolveActionLink answers an active link and counts the tap, once per
// tapKey (the viewer, or a hash of IP + user agent) per link per hour; the
// author's own taps are not counted. Not active, on a deleted or
// unapproved post, or on a members-only post the viewer is not entitled
// to: ErrActionLinkNotFound (one answer for all). A count that fails is
// logged, never an error: the viewer still gets the link.
func (s *Service) ResolveActionLink(ctx context.Context, id uuid.UUID, viewer uuid.UUID, tapKey string) (*ResolvedActionLink, error) {
	if s.actionLinks == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	r, err := s.actionLinks.GetResolvableActionLink(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil || r.Link == nil || r.Link.Status != postgres.ActionStatusActive {
		return nil, ErrActionLinkNotFound
	}
	l := r.Link
	if r.PostTierID != nil && l.PostID != nil {
		gate := &postgres.Post{ID: *l.PostID, AuthorID: r.PostAuthorID, TierRequiredID: r.PostTierID}
		if !s.viewerIsMember(ctx, viewer, gate) {
			return nil, ErrActionLinkNotFound
		}
	}
	if viewer != l.AuthorID {
		s.countActionTap(ctx, l.ID, tapKey)
	}
	return &ResolvedActionLink{ID: l.ID, URL: l.URL, Domain: l.Domain, Label: l.Label, Status: l.Status}, nil
}

// countActionTap counts one tap unless tapKey already tapped this link in
// the window. Without a deduper (a Redis-less dev loop) every tap counts;
// a deduper that errors counts nothing (stats must not be inflatable by
// knocking Redis over).
func (s *Service) countActionTap(ctx context.Context, linkID uuid.UUID, tapKey string) {
	cfg := s.actionConf()
	if cfg.TapDedupe != nil {
		first, err := cfg.TapDedupe.FirstTap(ctx, "action_tap:"+linkID.String()+":"+tapKey, ActionTapDedupeWindow)
		if err != nil {
			slog.WarnContext(ctx, "action tap not counted: dedupe unavailable", "link_id", linkID, "err", err)
			return
		}
		if !first {
			return
		}
	}
	if err := s.actionLinks.RecordActionLinkClick(ctx, linkID, s.clock()); err != nil {
		slog.WarnContext(ctx, "action tap not counted", "link_id", linkID, "err", err)
	}
}

// ActionStats is GET /v1/posts/:id/action/stats.
type ActionStats struct {
	ClicksTotal int64 `json:"clicks_total"`
	Clicks7d    int64 `json:"clicks_7d"`
}

func (s *Service) actionStatsSince() time.Time {
	return s.clock().UTC().AddDate(0, 0, -6)
}

// PostActionStats is the author's tap counts across every action the post
// has had. ErrPostNotFound, ErrNotPostAuthor.
func (s *Service) PostActionStats(ctx context.Context, callerID, postID uuid.UUID) (ActionStats, error) {
	if s.actionLinks == nil {
		return ActionStats{}, ErrAuthoringStoreUnavailable
	}
	author, _, err := s.postGateOf(ctx, postID)
	if err != nil {
		return ActionStats{}, err
	}
	if callerID == uuid.Nil || author != callerID {
		return ActionStats{}, ErrNotPostAuthor
	}
	total, week, err := s.actionLinks.ActionLinkStats(ctx, &postID, nil, s.actionStatsSince())
	return ActionStats{ClicksTotal: total, Clicks7d: week}, err
}

// ProfileActionStats is the caller's own profile-button tap counts.
func (s *Service) ProfileActionStats(ctx context.Context, userID uuid.UUID) (ActionStats, error) {
	if s.actionLinks == nil {
		return ActionStats{}, ErrAuthoringStoreUnavailable
	}
	total, week, err := s.actionLinks.ActionLinkStats(ctx, nil, &userID, s.actionStatsSince())
	return ActionStats{ClicksTotal: total, Clicks7d: week}, err
}

// ApplyScamLinkReport counts one scam_link report from trust-safety
// (consumers/action_link_report.go) against the target's live link.
// targetType is trust-safety's entity_type: "post" or "user" (a profile).
func (s *Service) ApplyScamLinkReport(ctx context.Context, reportID, reporterID uuid.UUID, targetType string, targetID uuid.UUID, reportedAt time.Time) (postgres.ActionLinkReportOutcome, error) {
	if s.actionLinks == nil {
		return postgres.ActionLinkReportOutcome{}, ErrAuthoringStoreUnavailable
	}
	now := s.clock().UTC()
	if reportedAt.IsZero() || reportedAt.After(now) {
		reportedAt = now
	}
	return s.actionLinks.ApplyActionLinkReport(ctx, postgres.ActionLinkReportInput{
		TargetType: targetType, TargetID: targetID, ReporterID: reporterID, ReportID: reportID,
		ReportedAt: reportedAt, Now: now, Window: ActionReportWindow, Threshold: ActionReportThreshold,
	})
}

// ActionLinkAdminView is one link in the moderation queue: the full row,
// raw URL included (moderators decide on the destination).
type ActionLinkAdminView struct {
	ID              uuid.UUID  `json:"id"`
	PostID          *uuid.UUID `json:"post_id"`
	ProfileOwnerID  *uuid.UUID `json:"profile_owner_id"`
	AuthorID        uuid.UUID  `json:"author_id"`
	URL             string     `json:"url"`
	Domain          string     `json:"domain"`
	Label           string     `json:"label"`
	LabelText       string     `json:"label_text"`
	Status          string     `json:"status"`
	StatusReason    *string    `json:"status_reason"`
	SafetyStatus    string     `json:"safety_status"`
	SafetyCheckedAt *time.Time `json:"safety_checked_at"`
	ModeratedAt     *time.Time `json:"moderated_at"`
	ClicksTotal     int64      `json:"clicks_total"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func actionLinkAdminView(l *postgres.ActionLink) ActionLinkAdminView {
	text, _ := actionlinks.LabelText(l.Label)
	return ActionLinkAdminView{
		ID: l.ID, PostID: l.PostID, ProfileOwnerID: l.ProfileOwnerID, AuthorID: l.AuthorID,
		URL: l.URL, Domain: l.Domain, Label: l.Label, LabelText: text,
		Status: l.Status, StatusReason: l.StatusReason, SafetyStatus: l.SafetyStatus,
		SafetyCheckedAt: l.SafetyCheckedAt, ModeratedAt: l.ModeratedAt, ClicksTotal: l.ClicksTotal,
		CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt,
	}
}

// ListActionLinksForReview is the queue (under_review or blocked).
func (s *Service) ListActionLinksForReview(ctx context.Context, status string, before time.Time, limit int) ([]ActionLinkAdminView, error) {
	if s.actionLinks == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	links, err := s.actionLinks.ListActionLinksByStatus(ctx, status, before, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ActionLinkAdminView, 0, len(links))
	for _, l := range links {
		out = append(out, actionLinkAdminView(l))
	}
	return out, nil
}

// ReviewActionLink is a moderator's restore / block (audited by the store).
func (s *Service) ReviewActionLink(ctx context.Context, actor, id uuid.UUID, decision, note string) (*ActionLinkAdminView, error) {
	if s.actionLinks == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	l, err := s.actionLinks.ReviewActionLink(ctx, id, actor, decision, note)
	if err != nil {
		return nil, err
	}
	v := actionLinkAdminView(l)
	return &v, nil
}

// ActionRecheckReport is what one recheck pass did.
type ActionRecheckReport struct {
	Checked     int
	Blocked     int
	Unavailable int
}

// ActionRecheckStaleAfter: a verdict older than this is checked again.
const ActionRecheckStaleAfter = 24 * time.Hour

// ActionRecheckClaim: how long a claimed link is kept from other workers
// (and, after a failed lookup, the retry delay).
const ActionRecheckClaim = time.Hour

// RecheckActionLinks runs one bounded batch of the daily safety recheck: a
// link whose verdict turns unsafe is blocked (status_reason unsafe) — the
// button disappears for viewers and its /l/ link stops resolving at once.
// Own-domain links are marked safe without a lookup. No checker: nothing
// to do. A failed lookup leaves the link as it is and keeps the claim,
// which delays its retry by ActionRecheckClaim.
func (s *Service) RecheckActionLinks(ctx context.Context, batch int) (ActionRecheckReport, error) {
	var rep ActionRecheckReport
	cfg := s.actionConf()
	if s.actionLinks == nil || cfg.Checker == nil {
		return rep, nil
	}
	now := s.clock().UTC()
	links, err := s.actionLinks.ClaimActionLinksForRecheck(ctx, now, now.Add(-ActionRecheckStaleAfter), now.Add(ActionRecheckClaim), batch)
	if err != nil {
		return rep, err
	}
	for _, l := range links {
		if ctx.Err() != nil {
			break
		}
		safety := postgres.ActionSafetySafe
		if !actionlinks.IsOwnDomain(l.Domain, cfg.OwnDomains) {
			verdict, err := cfg.Checker.Check(ctx, l.URL)
			if err != nil {
				rep.Unavailable++
				continue
			}
			if verdict.Unsafe {
				safety = postgres.ActionSafetyUnsafe
			}
		}
		updated, err := s.actionLinks.RecordActionLinkVerdict(ctx, l.ID, safety, s.clock().UTC())
		if err != nil {
			if !errors.Is(err, postgres.ErrActionLinkNotFound) {
				slog.WarnContext(ctx, "action recheck: verdict not stored", "link_id", l.ID, "err", err)
			}
			continue
		}
		rep.Checked++
		if safety == postgres.ActionSafetyUnsafe && updated.Status == postgres.ActionStatusBlocked {
			rep.Blocked++
			slog.InfoContext(ctx, "action link blocked: unsafe on recheck", "link_id", l.ID, "domain", l.Domain)
		}
	}
	return rep, nil
}
