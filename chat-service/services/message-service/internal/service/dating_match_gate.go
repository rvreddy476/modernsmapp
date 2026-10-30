package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Dating backend audit H2 — the send gate's backstop.
//
// chat.conversations.closed_at is set only when the dating consumer applies
// dating.match.closed / dating.user.blocked. dating-service now commits those
// events through an outbox, but an event still takes time to arrive, and a
// legacy suspension or chat hold never produced one. So while closed_at is
// NULL, a send in a dating conversation also asks dating-service whether the
// match's members may chat: the match open, the pair unblocked, neither
// member suspended, deleted or on a chat-blocking risk level.
//
//   - dating answers "not open", or does not know the match (404): the send
//     is refused with ErrMatchClosed (409 MATCH_CLOSED, the code clients
//     already handle);
//   - dating answers "not open" with reason pending_review (a member is
//     held for review, e.g. after an underage report): the send is refused
//     with ErrMatchOnHold (409 MATCH_ON_HOLD). It wraps ErrMatchClosed, so
//     every path that refuses a closed match refuses a held one, but clients
//     can tell "paused" from "ended". The hold is reversible: dating keeps the
//     match open, and chat resumes when a moderator clears the member;
//   - dating cannot be asked (unconfigured, unreachable, 5xx, a malformed
//     answer): the send is refused with an unclassified error (503
//     UNAVAILABLE, which clients retry). It never goes through on an answer
//     nobody gave.
//
// Answers are cached per match: an open answer for datingStateOpenTTL, so a
// suspension reaches an active chat within that window even with no event;
// a hold for the same short TTL, so chat resumes within it once the review
// clears; a closed answer for datingStateClosedTTL. The dating consumer
// drops a match's entry when it applies a closure for it.

const (
	datingChatStatePathFmt = "/v1/dating/internal/matches/%s/chat-state"
	datingChatStateTimeout = 2 * time.Second

	datingStateOpenTTL   = 15 * time.Second
	datingStateClosedTTL = 5 * time.Minute
	// datingStateMaxEntries bounds the cache; beyond it expired entries are
	// swept, and if none expired the cache starts over.
	datingStateMaxEntries = 50000
)

// errDatingStateUnavailable wraps every failure to learn a match's state.
var errDatingStateUnavailable = errors.New("dating match state unavailable")

// ErrMatchOnHold refuses a send while a member of the match is held for
// review (dating reason pending_review). It IS an ErrMatchClosed
// (errors.Is), mapped to its own code (409 MATCH_ON_HOLD).
var ErrMatchOnHold = fmt.Errorf("%w: on hold while a member is under review", ErrMatchClosed)

// datingHoldReason is dating's chat-state reason for a reversible hold.
const datingHoldReason = "pending_review"

// datingVerdict is dating's answer about one match.
type datingVerdict struct {
	Open bool
	// OnHold: not open only for a reversible review hold.
	OnHold bool
}

// datingMatchStateChecker answers whether matchID's members may chat.
type datingMatchStateChecker interface {
	DatingMatchChatState(ctx context.Context, matchID uuid.UUID) (datingVerdict, error)
}

type datingStateEntry struct {
	verdict datingVerdict
	expires time.Time
}

// datingMatchStateCache is a small TTL cache; the zero value is ready.
type datingMatchStateCache struct {
	mu      sync.Mutex
	entries map[uuid.UUID]datingStateEntry
}

func (c *datingMatchStateCache) get(id uuid.UUID, now time.Time) (datingVerdict, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok || !now.Before(e.expires) {
		return datingVerdict{}, false
	}
	return e.verdict, true
}

// datingStateTTL: only a closure is cached long; an open answer and a
// reversible hold expire quickly so neither becomes sticky.
func datingStateTTL(v datingVerdict) time.Duration {
	if v.Open || v.OnHold {
		return datingStateOpenTTL
	}
	return datingStateClosedTTL
}

func (c *datingMatchStateCache) put(id uuid.UUID, v datingVerdict, now time.Time) {
	ttl := datingStateTTL(v)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[uuid.UUID]datingStateEntry)
	}
	if _, present := c.entries[id]; !present && len(c.entries) >= datingStateMaxEntries {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= datingStateMaxEntries {
			c.entries = make(map[uuid.UUID]datingStateEntry)
		}
	}
	c.entries[id] = datingStateEntry{verdict: v, expires: now.Add(ttl)}
}

func (c *datingMatchStateCache) forget(id uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

// InvalidateDatingMatchState drops the cached chat state of matchID. The
// dating consumer calls it when it applies a closure for the match.
func (s *Service) InvalidateDatingMatchState(matchID uuid.UUID) {
	s.datingStates.forget(matchID)
}

// requireOpenDatingMatch refuses a send in a dating conversation whose match
// dating-service does not report open. See the file comment.
func (s *Service) requireOpenDatingMatch(ctx context.Context, matchID *uuid.UUID) error {
	if matchID == nil || *matchID == uuid.Nil {
		// A dating conversation is always created for a match; one without
		// a match id cannot be checked, so it cannot be written to.
		return ErrMatchClosed
	}
	now := s.clock()
	if v, ok := s.datingStates.get(*matchID, now); ok {
		return datingGateResult(v)
	}
	checker := s.datingMatchState
	if checker == nil {
		checker = httpDatingMatchState{s: s}
	}
	v, err := checker.DatingMatchChatState(ctx, *matchID)
	if err != nil {
		s.log.Warn("dating match state lookup failed; refusing the send", "match_id", *matchID, "err", err)
		return fmt.Errorf("%w: %w", errDatingStateUnavailable, err)
	}
	s.datingStates.put(*matchID, v, now)
	return datingGateResult(v)
}

// datingGateResult is the send gate's answer for dating's verdict.
func datingGateResult(v datingVerdict) error {
	switch {
	case v.Open:
		return nil
	case v.OnHold:
		return ErrMatchOnHold
	}
	return ErrMatchClosed
}

// httpDatingMatchState asks dating-service's internal chat-state route with
// the internal service key and deliberately NO end-user identity (dating
// refuses any internal call that carries one).
type httpDatingMatchState struct{ s *Service }

func (h httpDatingMatchState) DatingMatchChatState(ctx context.Context, matchID uuid.UUID) (datingVerdict, error) {
	base := h.s.datingServiceURL
	if base == "" {
		return datingVerdict{}, errors.New("DATING_SERVICE_URL is not configured")
	}
	callCtx, cancel := context.WithTimeout(ctx, datingChatStateTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, base+fmt.Sprintf(datingChatStatePathFmt, matchID), nil)
	if err != nil {
		return datingVerdict{}, err
	}
	req.Header.Set("Accept", "application/json")
	// TODO(dating-service-tokens): a service token (audience "dating",
	// scope "dating:match.chat_state") once chat can mint one; the internal
	// key is dating's legacy service credential.
	if h.s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", h.s.internalServiceKey)
	}
	client := h.s.httpClient
	if client == nil {
		client = &http.Client{Timeout: datingChatStateTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return datingVerdict{}, fmt.Errorf("call dating-service: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// Dating does not know the match (purged, or never formed): its
		// JSON NOT_FOUND. Any other 404 (a dating-service that predates the
		// route answers gin's plain-text one) is not an answer about the
		// match, so it fails like an outage (503), never as MATCH_CLOSED.
		var nf struct {
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &nf) == nil && nf.Error != nil && nf.Error.Code == "NOT_FOUND" {
			return datingVerdict{Open: false}, nil
		}
		return datingVerdict{}, errors.New("dating-service has no chat-state route (404)")
	default:
		return datingVerdict{}, fmt.Errorf("dating-service answered %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			Open   *bool  `json:"open"`
			Reason string `json:"reason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return datingVerdict{}, fmt.Errorf("decode dating chat state: %w", err)
	}
	if out.Data.Open == nil {
		return datingVerdict{}, errors.New("dating chat state has no open field")
	}
	if *out.Data.Open {
		return datingVerdict{Open: true}, nil
	}
	return datingVerdict{OnHold: out.Data.Reason == datingHoldReason}, nil
}
