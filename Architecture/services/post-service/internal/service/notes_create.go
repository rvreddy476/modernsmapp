// notes_create.go holds the two Echo publication paths: CreateNote (a
// note, a reply or a quote; POST /v1/notes) and CreateNoteThread (POST
// /v1/notes/thread). Both are publication entry points
// (publish_standing_guard_test.go) and run, in order: the request rules,
// author standing, Premium (notes, threads, quotes) or account age
// (replies), the durable idempotency replay, the rate limit, the target
// (visible note, reply policy, blocks), media authority and the note media
// rule, mentions privacy, the spam detector, and then one transaction for
// the rows, the parent's reply_count, a quote's repost row and the outbox
// events (store/postgres CreateNotes).
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// CreateNoteInput is POST /v1/notes.
type CreateNoteInput struct {
	AuthorID       uuid.UUID
	Text           string
	MediaIDs       []uuid.UUID
	ReplyToID      *uuid.UUID
	QuoteOfID      *uuid.UUID
	ReplyPolicy    string
	IdempotencyKey string
}

// CreateNoteThreadInput is POST /v1/notes/thread.
type CreateNoteThreadInput struct {
	AuthorID       uuid.UUID
	Entries        []ThreadEntryInput
	ReplyPolicy    string
	IdempotencyKey string
}

// Idempotency keys share post_create_idempotency with POST /v1/posts, so
// each route claims its own namespace: one client key can never replay a
// post as a note or a note as a thread.
const (
	noteIdemPrefix       = "note:"
	noteThreadIdemPrefix = "note-thread:"
)

func noteFingerprint(v any) string {
	b, _ := json.Marshal(v)
	return FingerprintOf(b)
}

func normalizeReplyPolicy(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return ReplyPolicyEveryone, nil
	}
	if !ValidReplyPolicy(p) {
		return "", fmt.Errorf("%w: reply_policy must be everyone, following or mentioned", ErrInvalidNoteRequest)
	}
	return p, nil
}

// noteDistribution is every note's policy: {"version":1,"main_feed":false}.
func noteDistribution() (*DistributionPolicy, json.RawMessage, error) {
	mainFeed := false
	pol := &DistributionPolicy{Version: distributionPolicyVersion, MainFeed: &mainFeed}
	raw, err := MarshalPolicy(pol)
	return pol, raw, err
}

// newNoteRow is the posts row of one note.
func newNoteRow(id, author uuid.UUID, text string, media []postgres.PostMedia, hashtags, mentions []string, review string, dist json.RawMessage, at time.Time) *postgres.Post {
	postType := postTypeText
	if len(media) > 0 {
		postType = postTypeImage
	}
	return &postgres.Post{
		ID: id, AuthorID: author, Text: text, Visibility: "public",
		ContentType: NoteContentType, PostType: postType, AppOrigin: "echo",
		Hashtags: hashtags, MentionUsernames: mentions, Media: media,
		Language: "en", License: "standard", RemixSetting: "allow", CommentModeration: "none",
		CommentAccess: "everyone", OriginalAudioVol: 1, OverlayAudioVol: 1, AllowDownload: true,
		ContentTypeExplicit: true, ReviewStatus: review,
		Distribution: dist, DistributionRev: 1,
		CreatedAt: at,
	}
}

// noteMedia checks one note's attachments: every asset exists, the note
// media rule (images, at most 4, a GIF alone), then the authority rule
// every post attachment passes (owned, confirmed, not refused).
func (s *Service) noteMedia(ctx context.Context, store noteStore, author uuid.UUID, ids []uuid.UUID) ([]postgres.PostMedia, map[uuid.UUID]postgres.NoteMediaRow, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	rows, err := store.NoteMedia(ctx, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("verify note media: %w", err)
	}
	return noteMediaFrom(author, ids, rows)
}

func noteMediaFrom(author uuid.UUID, ids []uuid.UUID, rows map[uuid.UUID]postgres.NoteMediaRow) ([]postgres.PostMedia, map[uuid.UUID]postgres.NoteMediaRow, error) {
	list := make([]postgres.NoteMediaRow, 0, len(ids))
	for _, id := range ids {
		m, ok := rows[id]
		if !ok {
			return nil, nil, fmt.Errorf("%w: %s", ErrMediaNotFound, id)
		}
		list = append(list, m)
	}
	if err := NoteMediaRule(list); err != nil {
		return nil, nil, err
	}
	out := make([]postgres.PostMedia, 0, len(ids))
	for _, id := range ids {
		m := rows[id]
		own := postgres.MediaOwnership{UploaderID: m.UploaderID, Kind: m.Kind,
			ProcessingStatus: m.ProcessingStatus, ModerationStatus: m.ModerationStatus}
		if err := checkMediaAuthority(id, author, own, true, NoteContentType, postTypeImage); err != nil {
			return nil, nil, err
		}
		out = append(out, postgres.PostMedia{MediaID: id, Kind: m.Kind})
	}
	return out, rows, nil
}

// noteVisibleTo reports whether viewer may read note id (the batch read's
// gates: private accounts, blocks either way, deactivated authors, review,
// media state).
func (s *Service) noteVisibleTo(ctx context.Context, id, viewer uuid.UUID) (bool, error) {
	d, err := s.GetPostsByIDs(ctx, []uuid.UUID{id}, &viewer)
	if err != nil {
		return false, err
	}
	p := d[id]
	return p != nil && p.Post != nil && p.Post.ContentType == NoteContentType, nil
}

// loadNoteTarget is the reply / quote target: a live note author may see.
func (s *Service) loadNoteTarget(ctx context.Context, store noteStore, id, author uuid.UUID) (*postgres.Note, error) {
	rows, err := store.GetNotes(ctx, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	n := rows[id]
	if n == nil {
		return nil, ErrNoteNotFound
	}
	ok, err := s.noteVisibleTo(ctx, id, author)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoteNotFound
	}
	return n, nil
}

// checkReplyAllowed is the reply rule on a visible target: its
// reply_policy, and no block between the replier and the conversation's
// top author (the target's own author is covered by the visibility read).
func (s *Service) checkReplyAllowed(ctx context.Context, store noteStore, target *postgres.Note, replier uuid.UUID) error {
	if !s.replyPolicyAllows(ctx, target, replier) {
		return ErrReplyNotAllowed
	}
	if target.ConversationID == nil || *target.ConversationID == target.Post.ID {
		return nil
	}
	rows, err := store.GetNotes(ctx, []uuid.UUID{*target.ConversationID})
	if err != nil {
		return err
	}
	root := rows[*target.ConversationID]
	if root == nil || root.Post.AuthorID == replier || root.Post.AuthorID == target.Post.AuthorID {
		return nil
	}
	if !s.canViewAuthor(ctx, &replier, root.Post.AuthorID) {
		return ErrReplyNotAllowed
	}
	return nil
}

func (s *Service) noteSpam(ctx context.Context, author uuid.UUID, text string, media int) (string, error) {
	if s.spam == nil {
		return "approved", nil
	}
	res := s.spam.Check(ctx, author.String(), text, media)
	if res.Score > 0.95 {
		return "", fmt.Errorf("content rejected: %s", res.Reason)
	}
	if res.Score > 0.7 {
		if s.producer != nil {
			go s.producer.PublishSpamDetected(context.Background(), author, res.Reason, res.Score)
		}
		return "flagged", nil
	}
	return "approved", nil
}

func (s *Service) noteEventsOn() bool { return s.producer != nil || s.noteEmitEvents }

// notePostCreated is PostCreated for a note: the ordinary payload plus the
// conversation fields (omitted when empty).
func (s *Service) notePostCreated(ctx context.Context, p *postgres.Post, pol *DistributionPolicy, f postgres.NoteFields) events.PostCreatedPayload {
	pc := s.buildPostCreatedPayload(ctx, p, pol, 0, 0, 1)
	if f.ReplyToID != nil {
		pc.ReplyToID = f.ReplyToID.String()
	}
	if f.ConversationID != nil {
		pc.ConversationID = f.ConversationID.String()
	}
	if f.QuoteOfID != nil {
		pc.QuoteOfID = f.QuoteOfID.String()
	}
	return pc
}

// noteReplyEvents are a note's note.replied / note.quoted outbox events.
// Nothing is emitted when the actor is the recipient.
func noteReplyEvents(p *postgres.Post, f postgres.NoteFields, target *postgres.Note) []postgres.ThreadOutboxEvent {
	if target == nil || target.Post.AuthorID == p.AuthorID {
		return nil
	}
	switch {
	case f.ReplyToID != nil:
		conv := ""
		if f.ConversationID != nil {
			conv = f.ConversationID.String()
		}
		return []postgres.ThreadOutboxEvent{{EventType: events.EventNoteReplied, PostID: p.ID, Payload: events.NoteRepliedPayload{
			NoteID: p.ID.String(), ConversationID: conv, ReplyToID: f.ReplyToID.String(),
			ActorID: p.AuthorID.String(), RecipientID: target.Post.AuthorID.String(), CreatedAt: p.CreatedAt,
		}}}
	case f.QuoteOfID != nil:
		return []postgres.ThreadOutboxEvent{{EventType: events.EventNoteQuoted, PostID: p.ID, Payload: events.NoteQuotedPayload{
			NoteID: p.ID.String(), QuoteOfID: f.QuoteOfID.String(),
			ActorID: p.AuthorID.String(), RecipientID: target.Post.AuthorID.String(), CreatedAt: p.CreatedAt,
		}}}
	}
	return nil
}

// replayNoteCreate answers a retry of a create that already committed.
func (s *Service) replayNote(ctx context.Context, postID, author uuid.UUID) (*NoteView, error) {
	views, err := s.loadNoteViews(ctx, []uuid.UUID{postID}, &author, true)
	if err != nil {
		return nil, err
	}
	if v := views[postID]; v != nil {
		return v, nil
	}
	return nil, fmt.Errorf("replay created note %s: not readable", postID)
}

// CreateNote is POST /v1/notes: a note, a reply (ReplyToID) or a quote
// (QuoteOfID).
func (s *Service) CreateNote(ctx context.Context, in *CreateNoteInput) (*NoteView, error) {
	if in.ReplyToID != nil && in.QuoteOfID != nil {
		return nil, fmt.Errorf("%w: reply_to_id and quote_of_id are mutually exclusive", ErrInvalidNoteRequest)
	}
	policy, err := normalizeReplyPolicy(in.ReplyPolicy)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(in.Text)
	if err := ValidateNoteText(text, len(in.MediaIDs)); err != nil {
		return nil, err
	}
	if err := ValidateNoteMediaCount(in.MediaIDs); err != nil {
		return nil, err
	}
	store, err := s.notesStore()
	if err != nil {
		return nil, err
	}
	author := in.AuthorID
	reply := in.ReplyToID != nil

	// Standing first, as on every publication path (publish_standing.go).
	if err := s.requirePublishStanding(ctx, author); err != nil {
		return nil, err
	}
	// A reply needs an account at least 7 days old; a note or a quote
	// needs Premium. Both fail closed.
	isPremium := false
	if reply {
		if err := s.requireAccountAge(ctx, author); err != nil {
			return nil, err
		}
	} else {
		if err := s.requirePremium(ctx, author); err != nil {
			return nil, err
		}
		isPremium = true
	}

	// Durable idempotency, fast path: a retry is answered before it spends
	// the rate limit.
	clientKey := noteIdemPrefix + in.IdempotencyKey
	fingerprint := noteFingerprint(struct {
		Text      string      `json:"text"`
		Media     []uuid.UUID `json:"media_ids"`
		ReplyTo   *uuid.UUID  `json:"reply_to_id"`
		QuoteOf   *uuid.UUID  `json:"quote_of_id"`
		Policy    string      `json:"reply_policy"`
		ActorNote string      `json:"kind"`
	}{text, in.MediaIDs, in.ReplyToID, in.QuoteOfID, policy, "note"})
	if in.IdempotencyKey != "" {
		postID, fp, found, err := store.LookupCreateIdempotency(ctx, author, clientKey)
		if err != nil {
			return nil, err
		}
		if found {
			if fp != fingerprint {
				return nil, ErrCreateKeyReused
			}
			return s.replayNote(ctx, postID, author)
		}
	}

	if reply {
		isPremium = s.isPremiumBestEffort(ctx, author)
	}
	if err := s.checkNoteRateLimit(ctx, author, reply, isPremium); err != nil {
		return nil, err
	}

	// The target: a visible, live note; for a reply, its policy and blocks.
	var target *postgres.Note
	switch {
	case in.ReplyToID != nil:
		target, err = s.loadNoteTarget(ctx, store, *in.ReplyToID, author)
		if err != nil {
			return nil, err
		}
		if err := s.checkReplyAllowed(ctx, store, target, author); err != nil {
			return nil, err
		}
	case in.QuoteOfID != nil:
		target, err = s.loadNoteTarget(ctx, store, *in.QuoteOfID, author)
		if err != nil {
			return nil, err
		}
	}

	media, _, err := s.noteMedia(ctx, store, author, in.MediaIDs)
	if err != nil {
		return nil, err
	}
	hashtags := s.noteHashtags(ctx, text)
	mentions := s.allowedMentions(ctx, author, extractMentions(text))
	review, err := s.noteSpam(ctx, author, text, len(media))
	if err != nil {
		return nil, err
	}
	pol, dist, err := noteDistribution()
	if err != nil {
		return nil, err
	}

	id := uuid.New()
	p := newNoteRow(id, author, text, media, hashtags, mentions, review, dist, s.clock())
	conv := id
	f := postgres.NoteFields{ConversationID: &conv, ReplyPolicy: policy, QuoteOfID: in.QuoteOfID}
	w := &postgres.NoteWrite{Notes: []*postgres.Post{p}, Fields: []postgres.NoteFields{f}}
	if reply {
		parent := target.Post.ID
		conv = parent
		if target.ConversationID != nil {
			conv = *target.ConversationID
		}
		f.ReplyToID = &parent
		f.ConversationID = &conv
		w.Fields[0] = f
		w.ReplyParent = &parent
	}
	if in.QuoteOfID != nil {
		w.QuoteRepost = &postgres.Repost{UserID: author, OriginalPostID: target.Post.ID,
			QuoteText: text, Visibility: target.Post.Visibility}
	}
	if s.noteEventsOn() {
		w.Events = append(w.Events, postgres.ThreadOutboxEvent{
			EventType: events.PostCreated, PostID: id, Payload: s.notePostCreated(ctx, p, pol, f)})
		w.Events = append(w.Events, noteReplyEvents(p, f, target)...)
	}
	if in.IdempotencyKey != "" {
		w.Idem = &postgres.CreateIdempotency{ActorID: author, ClientKey: clientKey, Fingerprint: fingerprint}
	}

	res, err := store.CreateNotes(ctx, w)
	if err != nil {
		var replay postgres.ErrCreateKeyReplay
		if errors.As(err, &replay) {
			return s.replayNote(ctx, replay.PostID, author)
		}
		if errors.Is(err, postgres.ErrCreateKeyConflict) {
			return nil, ErrCreateKeyReused
		}
		return nil, err
	}
	s.afterNoteCreated(ctx, p, mentions)
	if res != nil && res.QuoteRepostWritten && w.QuoteRepost != nil {
		s.afterQuoteRepost(ctx, w.QuoteRepost, target)
	}
	return s.freshNoteView(ctx, p, f, target, in.QuoteOfID != nil)
}

// afterNoteCreated is what a committed note does that is not durable: the
// mention rows and their notifications, and the outbox nudge. A note
// bumps no trending or hashtag counter and no live stream: it is in no
// public listing (announcePublishedPost is for posts).
func (s *Service) afterNoteCreated(ctx context.Context, p *postgres.Post, mentions []string) {
	s.storeMentions(ctx, p.ID, p.ContentType, mentions)
	if s.postMentionPublish() != nil && len(mentions) > 0 && p.EffectiveReviewStatus() == "approved" {
		postID, authorID := p.ID, p.AuthorID
		names := append([]string(nil), mentions...)
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s.publishPostMentions(ctx2, postID, authorID, names)
		}()
	}
	s.NudgeOutbox()
}

// afterQuoteRepost keeps the repost counters and post.reposted working for
// the quote's repost row, exactly as CreateRepost does for a fresh one.
func (s *Service) afterQuoteRepost(ctx context.Context, r *postgres.Repost, target *postgres.Note) {
	if s.pgStore != nil {
		if err := s.adjustEngagementCount(ctx, s.repostCounter, r.OriginalPostID, "repost_count", 1); err != nil {
			slog.Warn("quote note: repost count not incremented", "error", err, "post_id", r.OriginalPostID)
		}
	}
	if s.rdb != nil {
		key := fmt.Sprintf("post:%s:repost_count", r.OriginalPostID)
		s.rdb.Incr(ctx, key)
		s.rdb.Expire(ctx, key, 7*24*time.Hour)
	}
	if s.producer != nil {
		rr, authorID := *r, target.Post.AuthorID
		go func() {
			if err := s.producer.PublishPostReposted(context.Background(),
				rr.ID, rr.UserID, rr.OriginalPostID, authorID,
				"quote", rr.QuoteText, rr.Visibility, "", ""); err != nil {
				slog.Warn("quote note: post.reposted not published", "error", err)
			}
		}()
	}
}

// freshNoteView is the create response, built from the write (no read
// back): the author's own view of the note just made.
func (s *Service) freshNoteView(ctx context.Context, p *postgres.Post, f postgres.NoteFields, target *postgres.Note, quote bool) (*NoteView, error) {
	if s.pgStore != nil {
		if err := s.attachMediaState(ctx, []*postgres.Post{p}); err != nil {
			return nil, err
		}
	}
	d := &PostDetail{Post: p, Counts: &scylla.Counts{}, IsRepostable: repostableContent(p.ContentType, p.Visibility)}
	v := buildNoteView(d, &postgres.Note{Post: *p, NoteFields: f})
	v.ViewerCanReply = true
	author := p.AuthorID
	if a := s.noteAuthors(ctx, &author, []uuid.UUID{author}); a != nil {
		v.Author = a[author]
	}
	if quote && target != nil {
		quoted, err := s.loadNoteViews(ctx, []uuid.UUID{target.Post.ID}, &author, false)
		if err != nil {
			return nil, err
		}
		v.QuotedNote = quoted[target.Post.ID]
	}
	return v, nil
}

// CreateNoteThread is POST /v1/notes/thread: 2–25 notes by one author,
// created atomically. The first is a top-level note; each later entry
// replies to the one before it (so only the first reaches timelines), and
// all share the first's conversation and its reply_policy.
func (s *Service) CreateNoteThread(ctx context.Context, in *CreateNoteThreadInput) ([]*NoteView, error) {
	n := len(in.Entries)
	if n < NoteThreadMin || n > NoteThreadMax {
		return nil, fmt.Errorf("%w: a thread has %d-%d entries", ErrInvalidNoteThread, NoteThreadMin, NoteThreadMax)
	}
	texts := make([]string, n)
	for i, e := range in.Entries {
		texts[i] = strings.TrimSpace(e.Text)
		if err := ValidateNoteText(texts[i], len(e.MediaIDs)); err != nil {
			return nil, fmt.Errorf("%w: entry %d: %v", ErrInvalidNoteThread, i, err)
		}
		if err := ValidateNoteMediaCount(e.MediaIDs); err != nil {
			return nil, fmt.Errorf("%w: entry %d: %v", ErrInvalidNoteThread, i, err)
		}
	}
	policy, err := normalizeReplyPolicy(in.ReplyPolicy)
	if err != nil {
		return nil, err
	}
	store, err := s.notesStore()
	if err != nil {
		return nil, err
	}
	author := in.AuthorID
	if err := s.requirePublishStanding(ctx, author); err != nil {
		return nil, err
	}
	if err := s.requirePremium(ctx, author); err != nil {
		return nil, err
	}

	clientKey := noteThreadIdemPrefix + in.IdempotencyKey
	type fpEntry struct {
		Text  string      `json:"text"`
		Media []uuid.UUID `json:"media_ids"`
	}
	fpEntries := make([]fpEntry, n)
	var allMedia []uuid.UUID
	for i, e := range in.Entries {
		fpEntries[i] = fpEntry{texts[i], e.MediaIDs}
		allMedia = append(allMedia, e.MediaIDs...)
	}
	fingerprint := noteFingerprint(struct {
		Entries []fpEntry `json:"entries"`
		Policy  string    `json:"reply_policy"`
		Kind    string    `json:"kind"`
	}{fpEntries, policy, "note-thread"})
	if in.IdempotencyKey != "" {
		rootID, fp, found, err := store.LookupCreateIdempotency(ctx, author, clientKey)
		if err != nil {
			return nil, err
		}
		if found {
			if fp != fingerprint {
				return nil, ErrCreateKeyReused
			}
			return s.replayNoteThread(ctx, store, rootID, author)
		}
	}
	// A thread is one authored unit: one count against the notes budget.
	if err := s.checkNoteRateLimit(ctx, author, false, true); err != nil {
		return nil, err
	}

	var rows map[uuid.UUID]postgres.NoteMediaRow
	if len(allMedia) > 0 {
		rows, err = store.NoteMedia(ctx, allMedia)
		if err != nil {
			return nil, fmt.Errorf("verify note media: %w", err)
		}
	}
	medias := make([][]postgres.PostMedia, n)
	for i, e := range in.Entries {
		m, _, err := noteMediaFrom(author, e.MediaIDs, rows)
		if err != nil {
			if errors.Is(err, ErrTooManyNoteMedia) || errors.Is(err, ErrNoteMediaNotAllowed) {
				return nil, fmt.Errorf("%w: entry %d: %v", ErrInvalidNoteThread, i, err)
			}
			return nil, err
		}
		medias[i] = m
	}
	review, err := s.noteSpam(ctx, author, strings.Join(texts, "\n"), len(allMedia))
	if err != nil {
		return nil, err
	}
	pol, dist, err := noteDistribution()
	if err != nil {
		return nil, err
	}

	now := s.clock()
	rootID := uuid.New()
	w := &postgres.NoteWrite{}
	mentionsBy := make([][]string, n)
	var prev *uuid.UUID
	for i := range in.Entries {
		id := rootID
		if i > 0 {
			id = uuid.New()
		}
		mentionsBy[i] = s.allowedMentions(ctx, author, extractMentions(texts[i]))
		p := newNoteRow(id, author, texts[i], medias[i], s.noteHashtags(ctx, texts[i]), mentionsBy[i], review, dist,
			now.Add(time.Duration(i)*time.Millisecond))
		root := rootID
		p.ThreadRootID = &root
		p.ThreadSeq = i
		f := postgres.NoteFields{ConversationID: &root, ReplyPolicy: policy}
		if prev != nil {
			replyTo := *prev
			p.ThreadReplyToID = &replyTo
			f.ReplyToID = &replyTo
		}
		if i < n-1 {
			f.ReplyCount = 1
		}
		w.Notes = append(w.Notes, p)
		w.Fields = append(w.Fields, f)
		if s.noteEventsOn() {
			w.Events = append(w.Events, postgres.ThreadOutboxEvent{
				EventType: events.PostCreated, PostID: id, Payload: s.notePostCreated(ctx, p, pol, f)})
		}
		idCopy := id
		prev = &idCopy
	}
	if in.IdempotencyKey != "" {
		w.Idem = &postgres.CreateIdempotency{ActorID: author, ClientKey: clientKey, Fingerprint: fingerprint}
	}
	if _, err := store.CreateNotes(ctx, w); err != nil {
		var replay postgres.ErrCreateKeyReplay
		if errors.As(err, &replay) {
			return s.replayNoteThread(ctx, store, replay.PostID, author)
		}
		if errors.Is(err, postgres.ErrCreateKeyConflict) {
			return nil, ErrCreateKeyReused
		}
		return nil, err
	}
	out := make([]*NoteView, 0, n)
	for i, p := range w.Notes {
		s.afterNoteCreated(ctx, p, mentionsBy[i])
		v, err := s.freshNoteView(ctx, p, w.Fields[i], nil, false)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) replayNoteThread(ctx context.Context, store noteStore, rootID, author uuid.UUID) ([]*NoteView, error) {
	rows, err := store.GetNoteThread(ctx, rootID)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*postgres.Note, len(rows))
	for i := range rows {
		ptrs[i] = &rows[i]
	}
	return s.hydrateNotes(ctx, ptrs, &author, false)
}
