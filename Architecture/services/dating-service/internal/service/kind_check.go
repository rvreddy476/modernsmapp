// Kind messages (mechanic M13, DATING_KIND_CHECK_ENABLED).
//
//	POST /v1/dating/kind-check {text}            -> {kind, reasons}
//	POST /v1/dating/matches/:id/bothered {bothered}
//	GET/PUT /v1/dating/comment-filter {filter_unkind, words}
//
// The apps ask kind-check before sending a chat message (a "send anyway?"
// nudge, never a block) and for a received one (blur it and ask "did this
// bother you?"). The check is the unkind word list, the layer-1 scan's abuse
// verdict and, when configured, the LLM's score; nothing about the text is
// stored. A "bothered" answer is kept as safety evidence and offers the
// report flow.
//
// Spark comments: in the recipient's incoming sparks and Liked you, a note
// that is unkind (unless the recipient switched that off) or uses one of
// their hidden words carries note_hidden, and the apps tuck it away behind
// a tap. The note itself is still sent: the recipient may read it.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/dating-service/internal/moderation"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

const (
	// maxKindCheckLen is the longest text kind-check reads.
	maxKindCheckLen = 2000
	// kindCheckHourlyLimit caps checks per user per hour.
	kindCheckHourlyLimit = 600
	// kindLLMThreshold is the LLM unsafeness score that flags a text.
	kindLLMThreshold = 0.6
	kindLLMTimeout   = 2 * time.Second
	// Comment-filter word limits.
	maxHiddenWords   = 50
	minHiddenWordLen = 2
	maxHiddenWordLen = 30
)

var (
	// ErrInvalidKindCheck maps to 400 INVALID_KIND_CHECK.
	ErrInvalidKindCheck = refuseField("INVALID_KIND_CHECK", "text", "text is required and at most 2000 characters", map[string]any{"max": maxKindCheckLen})
	// ErrKindCheckRateLimited maps to 429 KIND_CHECK_RATE_LIMITED.
	ErrKindCheckRateLimited = errors.New("rate_limited: too many checks this hour")
	// ErrInvalidCommentFilter maps to 400 INVALID_COMMENT_FILTER.
	ErrInvalidCommentFilter = refuseField("INVALID_COMMENT_FILTER", "words", "up to 50 distinct words of 2 to 30 characters",
		map[string]any{"max_words": maxHiddenWords, "min_len": minHiddenWordLen, "max_len": maxHiddenWordLen})
)

// Note filter reasons (Spark.NoteHidden).
const (
	NoteHiddenUnkind    = "unkind"
	NoteHiddenYourWords = "your_words"
)

// KindCheckResult is POST /kind-check.
type KindCheckResult struct {
	Kind    bool     `json:"kind"`
	Reasons []string `json:"reasons"`
}

// KindCheck judges one text.
func (s *Service) KindCheck(ctx context.Context, userID uuid.UUID, text string) (*KindCheckResult, error) {
	if !s.mechanics.KindCheck {
		return nil, ErrMechanicDisabled
	}
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > maxKindCheckLen {
		return nil, ErrInvalidKindCheck
	}
	if s.rdb != nil {
		key := fmt.Sprintf("dating:kindcheck:%s:%s", userID, time.Now().UTC().Format("2006010215"))
		n, err := s.rdb.Incr(ctx, key).Result()
		if err == nil {
			if n == 1 {
				s.rdb.Expire(ctx, key, time.Hour)
			}
			if n > kindCheckHourlyLimit {
				return nil, ErrKindCheckRateLimited
			}
		}
	}
	reasons := unkindReasons(text)
	// The mock LLM (local/dev) scores from the layer-1 patterns, which would
	// flag a shared phone number as "tone": only a real model counts here.
	if _, mock := s.moderationLLM.(*moderation.MockClient); len(reasons) == 0 && s.moderationLLM != nil && !mock {
		lctx, cancel := context.WithTimeout(ctx, kindLLMTimeout)
		resp, err := s.moderationLLM.Score(lctx, text)
		cancel()
		if err != nil {
			slog.Debug("kind check: llm unavailable", "error", err)
		} else if resp != nil && resp.Confidence >= kindLLMThreshold {
			reasons = append(reasons, "tone")
		}
	}
	if reasons == nil {
		reasons = []string{}
	}
	return &KindCheckResult{Kind: len(reasons) == 0, Reasons: reasons}, nil
}

// unkindReasons is the word list plus the layer-1 abuse verdict.
func unkindReasons(text string) []string {
	_, reasons := moderation.Unkind(text)
	scan := moderation.ScanMessage(text)
	for _, p := range scan.Patterns {
		if p == moderation.PatternAbuse && !contains(reasons, moderation.UnkindInsult) {
			reasons = append(reasons, moderation.UnkindInsult)
		}
	}
	return reasons
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// BotheredView is POST /matches/:id/bothered.
type BotheredView struct {
	MatchID     uuid.UUID `json:"match_id"`
	Bothered    bool      `json:"bothered"`
	OfferReport bool      `json:"offer_report"`
}

// Bothered records the caller's answer about a message in one of their
// matches. Someone else's match reads as not found.
func (s *Service) Bothered(ctx context.Context, userID, matchID uuid.UUID, bothered bool) (*BotheredView, error) {
	if !s.mechanics.KindCheck {
		return nil, ErrMechanicDisabled
	}
	m, err := s.store.GetMatch(ctx, matchID)
	if err != nil || m == nil || (m.UserA != userID && m.UserB != userID) {
		return nil, store.ErrMatchNotFound
	}
	other := m.UserA
	if other == userID {
		other = m.UserB
	}
	if err := s.store.RecordMessageFeedback(ctx, matchID, userID, other, bothered); err != nil {
		return nil, err
	}
	if bothered {
		if err := s.store.RecordSafetyEvent(ctx, userID, "message_bothered", map[string]any{"match_id": matchID.String(), "other_user_id": other.String()}); err != nil {
			slog.Error("kind check: safety event not recorded", "match_id", matchID, "error", err)
		}
	}
	return &BotheredView{MatchID: matchID, Bothered: bothered, OfferReport: bothered}, nil
}

// CommentFilterView is GET/PUT /comment-filter.
type CommentFilterView struct {
	FilterUnkind bool     `json:"filter_unkind"`
	Words        []string `json:"words"`
}

// GetCommentFilter returns the caller's spark-comment filter.
func (s *Service) GetCommentFilter(ctx context.Context, userID uuid.UUID) (*CommentFilterView, error) {
	if !s.mechanics.KindCheck {
		return nil, ErrMechanicDisabled
	}
	f, err := s.store.GetCommentFilter(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &CommentFilterView{FilterUnkind: f.FilterUnkind, Words: f.Words}, nil
}

// PutCommentFilter replaces the caller's filter. Words are trimmed,
// lower-cased and must be distinct.
func (s *Service) PutCommentFilter(ctx context.Context, userID uuid.UUID, in CommentFilterView) (*CommentFilterView, error) {
	if !s.mechanics.KindCheck {
		return nil, ErrMechanicDisabled
	}
	if len(in.Words) > maxHiddenWords {
		return nil, ErrInvalidCommentFilter
	}
	words := make([]string, 0, len(in.Words))
	seen := map[string]bool{}
	for _, w := range in.Words {
		w = strings.ToLower(strings.TrimSpace(w))
		n := utf8.RuneCountInString(w)
		if n < minHiddenWordLen || n > maxHiddenWordLen || seen[w] {
			return nil, ErrInvalidCommentFilter
		}
		seen[w] = true
		words = append(words, w)
	}
	if err := s.store.SetCommentFilter(ctx, userID, store.CommentFilter{FilterUnkind: in.FilterUnkind, Words: words}); err != nil {
		return nil, err
	}
	return s.GetCommentFilter(ctx, userID)
}

// markHiddenNotes sets NoteHidden on the recipient's incoming sparks whose
// note their filter hides. A filter that cannot be read hides only unkind
// notes (the default).
func (s *Service) markHiddenNotes(ctx context.Context, recipientID uuid.UUID, sparks []*store.Spark) {
	if !s.mechanics.KindCheck {
		return
	}
	f, err := s.store.GetCommentFilter(ctx, recipientID)
	if err != nil {
		f = store.CommentFilter{FilterUnkind: true}
	}
	for _, sp := range sparks {
		if sp == nil || sp.Note == nil || strings.TrimSpace(*sp.Note) == "" {
			continue
		}
		if f.FilterUnkind && len(unkindReasons(*sp.Note)) > 0 {
			sp.NoteHidden = NoteHiddenUnkind
			continue
		}
		lower := strings.ToLower(*sp.Note)
		for _, w := range f.Words {
			if strings.Contains(lower, w) {
				sp.NoteHidden = NoteHiddenYourWords
				break
			}
		}
	}
}
