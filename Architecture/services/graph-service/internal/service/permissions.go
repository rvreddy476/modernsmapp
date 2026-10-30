package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/atpost/graph-service/internal/permission"
	"github.com/google/uuid"
)

// ResolvePermissions gathers the relationship snapshot between actor and
// target plus the target's privacy settings, then resolves every requested
// action through the §4 permission matrix.
func (s *Service) ResolvePermissions(ctx context.Context, actorID, targetID uuid.UUID, actions []permission.Action) (map[permission.Action]permission.Decision, error) {
	facts, err := s.permissionFacts(ctx, actorID, targetID)
	if err != nil {
		return nil, err
	}
	privacy := s.fetchPrivacy(ctx, targetID)
	facts = withActionScopedFacts(actions, facts, privacy, scopedFactSources{
		actorPrivacy: func() permission.Privacy { return s.fetchPrivacy(ctx, actorID) },
		acceptedChat: func() bool { return s.acceptedChatFact(ctx, actorID, targetID) },
		followSecondDegree: func() bool {
			ok, err := s.store.CheckFollowSecondDegree(ctx, actorID, targetID)
			if err != nil {
				log.Printf("[graph] follow second-degree check failed for %s->%s, treating as false: %v", actorID, targetID, err)
				return false
			}
			return ok
		},
	})
	return permission.ResolveAll(actions, facts, privacy), nil
}

// scopedFactSources are the lookups behind the action-scoped facts.
// actorPrivacy is the ACTOR's own settings (the same cached, fail-closed
// fetch as the target's); acceptedChat is the accepted 1:1 chat probe;
// followSecondDegree asks whether a third party follows both. Each must fail
// closed (false / strict defaults).
type scopedFactSources struct {
	actorPrivacy       func() permission.Privacy
	acceptedChat       func() bool
	followSecondDegree func() bool
}

// audienceSettingInPlay reports whether a requested action resolves through
// the target's connections_only audience (permission.Facts
// InConnectionsAudience), i.e. whether the accepted-chat fact could change
// its answer.
func audienceSettingInPlay(actions []permission.Action, target permission.Privacy) bool {
	for _, a := range actions {
		var setting string
		switch a {
		case permission.ActionSeeOnlineStatus:
			setting = target.WhoCanSeeOnlineStatus
		case permission.ActionSeeLastSeen:
			setting = target.WhoCanSeeLastSeen
		case permission.ActionSeeReadReceipts:
			setting = target.WhoCanSeeReadReceipts
		case permission.ActionViewProfile:
			setting = target.WhoCanSeeProfilePhoto
		case permission.ActionAddToGroup:
			switch target.WhoCanAddToGroups {
			case "connections_only", "connections_and_contacts", "friends_of_friends_invites":
				return true
			}
			continue
		default:
			continue
		}
		if setting == "connections_only" {
			return true
		}
	}
	return false
}

// withActionScopedFacts adds the facts only some actions read, fetching each
// only when such an action was requested (and, for the audience facts, only
// when the target's setting for it consults them), and never for a blocked
// pair (a block is fatal anyway). target is the TARGET's settings, already
// fetched. Pure apart from the callbacks, so the wiring is unit-tested.
func withActionScopedFacts(actions []permission.Action, facts permission.Facts, target permission.Privacy, src scopedFactSources) permission.Facts {
	if facts.Blocked {
		return facts
	}
	var actor *permission.Privacy
	actorSettings := func() permission.Privacy {
		if actor == nil {
			p := src.actorPrivacy()
			actor = &p
		}
		return *actor
	}
	if wantsAction(actions, permission.ActionMessage, permission.ActionStartConversation) {
		// The SENDER's own pause (DM consent contract §2/§4).
		p := actorSettings()
		facts.ActorSettingsUnavailable = p.Unavailable
		facts.ActorChatPaused = p.ChatAvailability == "paused"
	}
	// Presence reciprocity (founder, 23 Oct 2026): the VIEWER's own online /
	// last-seen settings; an unreadable viewer counts as hiding.
	if wantsAction(actions, permission.ActionSeeOnlineStatus, permission.ActionSeeLastSeen) {
		p := actorSettings()
		facts.ActorHidesOnline = permission.HidesPresence(p, p.WhoCanSeeOnlineStatus)
		facts.ActorHidesLastSeen = permission.HidesPresence(p, p.WhoCanSeeLastSeen)
	}
	// An accepted 1:1 chat grants calls, lets the pair keep messaging (the
	// profile button reads "Message", not "Send request"), and puts the actor
	// in the connections_only audience (online status, last seen, read
	// receipts, profile photo, group adds). For the audience it is needed
	// only when the target does not already follow the actor.
	if !facts.IsConnection {
		chat := wantsAction(actions, permission.ActionCall, permission.ActionMessage, permission.ActionStartConversation)
		audience := !facts.TargetFollowsActor && audienceSettingInPlay(actions, target)
		if chat || audience {
			facts.AcceptedChat = src.acceptedChat()
		}
	}
	// Friends of friends over follows, for friends_of_friends_invites only,
	// and only when neither the audience nor a shared legacy connection has
	// already decided the answer.
	if wantsAction(actions, permission.ActionAddToGroup) &&
		target.WhoCanAddToGroups == "friends_of_friends_invites" &&
		!facts.InConnectionsAudience() && !facts.SecondDegree {
		facts.FollowSecondDegree = src.followSecondDegree()
	}
	return facts
}

// wantsAction reports whether any of want is among the requested actions.
func wantsAction(actions []permission.Action, want ...permission.Action) bool {
	for _, a := range actions {
		for _, w := range want {
			if a == w {
				return true
			}
		}
	}
	return false
}

// permissionFacts assembles the relationship snapshot. GetRelationshipFull
// reports a block only in the target→actor direction, so the actor→target
// direction is checked separately — §4 treats a block either way as fatal.
func (s *Service) permissionFacts(ctx context.Context, actorID, targetID uuid.UUID) (permission.Facts, error) {
	full, err := s.store.GetRelationshipFull(ctx, actorID, targetID)
	if err != nil {
		return permission.Facts{}, err
	}
	actorBlockedTarget, err := s.store.CheckBlock(ctx, actorID, targetID)
	if err != nil {
		return permission.Facts{}, err
	}
	// Account control: a deactivated or deletion-scheduled account (either
	// side) answers exactly like a blocked pair until it is reactivated or
	// the deletion is cancelled. Nothing is erased on this path.
	hidden, err := s.store.AnyHidden(ctx, actorID, targetID)
	if err != nil {
		return permission.Facts{}, err
	}
	if hidden {
		actorBlockedTarget = true
	}
	// Second degree matters only when the pair is NOT directly connected —
	// skip the extra EXISTS for the common connected case. A lookup failure
	// fails toward "not second degree" (the strictly safer answer for every
	// consumer of this fact), never toward granting.
	secondDegree := false
	if !full.IsConnection && !full.Blocked && !actorBlockedTarget {
		secondDegree, err = s.store.CheckSecondDegree(ctx, actorID, targetID)
		if err != nil {
			log.Printf("[graph] second-degree check failed for %s->%s, treating as false: %v", actorID, targetID, err)
			secondDegree = false
		}
	}
	blocked := full.Blocked || actorBlockedTarget
	// Dating-match call grant. Skipped entirely for a blocked pair (a block
	// is fatal in the matrix anyway) and for an existing connection (the
	// connection already grants calls), so the extra hop happens only on the
	// path where it can change the answer. datingMatchFact fails closed: any
	// error, any unknown answer, or the flag being off yields false.
	datingMatch := false
	if !blocked && !full.IsConnection {
		datingMatch = s.datingMatchFact(ctx, actorID, targetID)
	}
	return permission.Facts{
		Blocked:            blocked,
		IsConnection:       full.IsConnection,
		ActorFollowsTarget: full.Follows,
		TargetFollowsActor: full.FollowedBy,
		SecondDegree:       secondDegree,
		DatingMatch:        datingMatch,
	}, nil
}

// strictPrivacyDefaults is the fail-safe privacy posture used when the target's
// real settings cannot be fetched — it never silently opens messaging up.
func strictPrivacyDefaults() permission.Privacy {
	return permission.Privacy{
		// P0-2 correction: chat actions and presence disclosure DENY outright
		// while the target's real settings are unreadable. The previous
		// connections_only posture let an existing connection open a chat and
		// read online/receipt state during an identity outage even when the
		// target's real setting was no_one or paused — an unknown setting is
		// not a licence to act on the most permissive plausible one.
		WhoCanMessage:         "no_one",
		WhoCanCall:            "no_one",
		WhoCanAddToGroups:     "no_one",
		WhoCanSeeOnlineStatus: "no_one",
		WhoCanSeeReadReceipts: "no_one",
		WhoCanSeeLastSeen:     "no_one",
		WhoCanSeeProfilePhoto: "everyone",
		// NOT 'paused': the no_one values above already deny every chat
		// action; 'paused' would additionally masquerade as a state the user
		// chose. This stays 'enabled' purely so the pause-specific reason
		// codes remain truthful.
		ChatAvailability: "enabled",
		// AccountVisibility / AllowCommentsFrom stay EMPTY (unknown), not
		// "public"/"everyone". The matrix fails closed on unknown for
		// view_posts (follower-only) and comment (deny), while the follow
		// path only converts to a request on an EXPLICIT "private" — so an
		// identity outage neither exposes a private account's posts nor
		// strands public follows as pending requests.

		// Marks the snapshot as unreadable so the message decision answers
		// reason "unavailable" (DM consent contract §4) instead of the
		// no_one the fail-safe value would otherwise report as the user's
		// own choice. Never cached: only successful fetches are.
		Unavailable: true,
	}
}

// PrivacyCacheTTL is how stale a cached privacy record may be.
//
// It was 60 seconds, matching the spec's §6.2 freshness budget, and there is
// no settings-invalidation consumer here — nothing tells graph-service that a
// user just changed who_can_message. Sixty seconds of that is a full minute in
// which someone who has just set themselves to `no_one` still receives direct
// messages, and a minute in which someone who has just opened up is still told
// they cannot be reached. The first of those is a privacy failure, not a
// staleness inconvenience.
//
// Three seconds is the freshness bound this service now documents and the
// closure proof measures against. It costs one extra internal GET per target
// per three seconds under load, which is a cache on a single-row read behind
// the internal key — cheap next to enforcing a privacy setting the user
// believes is already in effect.
//
// If this becomes a load problem, the answer is an invalidation channel keyed
// on the settings write, not a longer TTL: the TTL is the fallback for when
// invalidation is missed, and its length is the size of the privacy hole.
const PrivacyCacheTTL = 3 * time.Second

// fetchPrivacy returns the target's privacy settings, cached in Redis for
// [PrivacyCacheTTL]. Any fetch failure falls back to strict defaults rather
// than failing the permission check — a dependency outage must not open
// messaging up.
func (s *Service) fetchPrivacy(ctx context.Context, userID uuid.UUID) permission.Privacy {
	cacheKey := "privacy:" + userID.String()
	if val, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil {
		var p permission.Privacy
		if json.Unmarshal([]byte(val), &p) == nil {
			return p
		}
	}

	if s.userServiceURL == "" {
		return strictPrivacyDefaults()
	}

	p, err := s.fetchPrivacyFromUserService(ctx, userID)
	if err != nil {
		log.Printf("[graph] privacy fetch failed for %s, using strict defaults: %v", userID, err)
		return strictPrivacyDefaults()
	}

	if data, err := json.Marshal(p); err == nil {
		s.rdb.Set(ctx, cacheKey, data, PrivacyCacheTTL)
	}
	return p
}

// Batch privacy fetch bounds (CanBatch). identity-platform's user-service
// has no batch settings endpoint (only GET /v1/users/:id/settings), so the
// uncached targets of one batch are fetched concurrently instead of one after
// another: 20 uncached authors behind a slow identity used to take 20 serial
// round trips and blow post-service's 5 s budget, emptying the home feed.
const (
	// PrivacyFetchConcurrency caps in-flight settings GETs per batch.
	PrivacyFetchConcurrency = 10
	// PrivacyFetchTimeout bounds each settings GET.
	PrivacyFetchTimeout = 1500 * time.Millisecond
	// PrivacyBatchBudget bounds the whole batch's privacy phase. It is
	// further capped at half of the caller's remaining deadline so the
	// answer still reaches the caller in time.
	PrivacyBatchBudget = 2500 * time.Millisecond
)

// fetchPrivacyBatch returns a privacy snapshot for every id. Cache hits come
// from one Redis MGET; misses are fetched concurrently (at most
// [PrivacyFetchConcurrency] in flight, each bounded by [PrivacyFetchTimeout],
// all bounded by [PrivacyBatchBudget]). Any id whose privacy cannot be read in
// time gets strictPrivacyDefaults (fail closed) on its own; one slow or
// failing target never fails the batch or the other targets.
func (s *Service) fetchPrivacyBatch(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]permission.Privacy {
	out := make(map[uuid.UUID]permission.Privacy, len(ids))
	uniq := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, seen := out[id]; seen {
			continue
		}
		out[id] = strictPrivacyDefaults()
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return out
	}

	misses := uniq
	if s.rdb != nil {
		keys := make([]string, len(uniq))
		for i, id := range uniq {
			keys[i] = "privacy:" + id.String()
		}
		if vals, err := s.rdb.MGet(ctx, keys...).Result(); err == nil && len(vals) == len(uniq) {
			misses = misses[:0:0]
			for i, v := range vals {
				str, ok := v.(string)
				var p permission.Privacy
				if ok && json.Unmarshal([]byte(str), &p) == nil {
					out[uniq[i]] = p
					continue
				}
				misses = append(misses, uniq[i])
			}
		}
	}
	if len(misses) == 0 || s.userServiceURL == "" {
		return out
	}

	budget := PrivacyBatchBudget
	if dl, ok := ctx.Deadline(); ok {
		if half := time.Until(dl) / 2; half < budget {
			budget = half
		}
	}
	batchCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, PrivacyFetchConcurrency)
	)
	for _, id := range misses {
		select {
		case sem <- struct{}{}:
		case <-batchCtx.Done():
			// Budget spent before this target got a slot: it keeps the
			// strict default.
			log.Printf("[graph] privacy batch budget spent, strict defaults for %s", id)
			continue
		}
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			defer func() { <-sem }()
			callCtx, callCancel := context.WithTimeout(batchCtx, PrivacyFetchTimeout)
			defer callCancel()
			p, err := s.fetchPrivacyFromUserService(callCtx, id)
			if err != nil {
				log.Printf("[graph] privacy fetch failed for %s, using strict defaults: %v", id, err)
				return
			}
			mu.Lock()
			out[id] = p
			mu.Unlock()
			if s.rdb != nil {
				if data, err := json.Marshal(p); err == nil {
					s.rdb.Set(context.WithoutCancel(ctx), "privacy:"+id.String(), data, PrivacyCacheTTL)
				}
			}
		}(id)
	}
	wg.Wait()
	return out
}

func (s *Service) fetchPrivacyFromUserService(ctx context.Context, userID uuid.UUID) (permission.Privacy, error) {
	url := fmt.Sprintf("%s/v1/users/%s/settings", s.userServiceURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return permission.Privacy{}, err
	}
	if s.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return permission.Privacy{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return permission.Privacy{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return permission.Privacy{}, fmt.Errorf("user-service returned %d: %s", resp.StatusCode, string(body))
	}

	var envelope struct {
		Data struct {
			WhoCanMessage         string `json:"who_can_message"`
			WhoCanCall            string `json:"who_can_call"`
			WhoCanAddToGroups     string `json:"who_can_add_to_groups"`
			WhoCanSeeOnlineStatus string `json:"who_can_see_online_status"`
			WhoCanSeeReadReceipts string `json:"who_can_see_read_receipts"`
			WhoCanSeeLastSeen     string `json:"who_can_see_last_seen"`
			WhoCanSeeProfilePhoto string `json:"who_can_see_profile_photo"`
			ChatAvailability      string `json:"chat_availability"`
			AccountVisibility     string `json:"account_visibility"`
			AllowCommentsFrom     string `json:"allow_comments_from"`
			WhoCanMention         string `json:"who_can_mention"`
			WhoCanTag             string `json:"who_can_tag"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return permission.Privacy{}, fmt.Errorf("decode privacy response: %w", err)
	}
	// A SUCCESSFUL fetch that omits the private-accounts fields is a
	// pre-migration identity build; the product defaults are public/everyone.
	// This is distinct from a FAILED fetch, which keeps the strict-default
	// unknowns above.
	if envelope.Data.AccountVisibility == "" {
		envelope.Data.AccountVisibility = "public"
	}
	if envelope.Data.AllowCommentsFrom == "" {
		envelope.Data.AllowCommentsFrom = "everyone"
	}
	// Same rule for the mention / tag settings (2026-10-24): an identity build
	// that predates them answers without the keys, and their product default
	// is everyone.
	if envelope.Data.WhoCanMention == "" {
		envelope.Data.WhoCanMention = "everyone"
	}
	if envelope.Data.WhoCanTag == "" {
		envelope.Data.WhoCanTag = "everyone"
	}
	return permission.Privacy{
		WhoCanMessage:         envelope.Data.WhoCanMessage,
		WhoCanCall:            envelope.Data.WhoCanCall,
		WhoCanAddToGroups:     envelope.Data.WhoCanAddToGroups,
		WhoCanSeeOnlineStatus: envelope.Data.WhoCanSeeOnlineStatus,
		WhoCanSeeReadReceipts: envelope.Data.WhoCanSeeReadReceipts,
		WhoCanSeeLastSeen:     envelope.Data.WhoCanSeeLastSeen,
		WhoCanSeeProfilePhoto: envelope.Data.WhoCanSeeProfilePhoto,
		ChatAvailability:      envelope.Data.ChatAvailability,
		AccountVisibility:     envelope.Data.AccountVisibility,
		AllowCommentsFrom:     envelope.Data.AllowCommentsFrom,
		WhoCanMention:         envelope.Data.WhoCanMention,
		WhoCanTag:             envelope.Data.WhoCanTag,
	}, nil
}
