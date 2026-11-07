// Opening and reading conversations: direct chats (with the messaging
// permission check), dating-match chats and their close/open state, groups,
// the single-conversation read, the legacy list path and the helpers that
// build a ConversationResponse for one viewer.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// --- Conversations ---

func (s *Service) CreateDirectConversation(ctx context.Context, userID, otherID uuid.UUID, idempotencyKey string) (*ConversationResponse, error) {
	if userID == otherID {
		return nil, invalidInput("cannot create conversation with self")
	}

	// The ACTOR's own chat pause blocks new conversations they initiate
	// (directive §3.2). The target's pause is enforced by the graph decision
	// below. Unknown own-policy fails closed for CREATION (sensitive action).
	if own := s.GetChatPolicy(ctx, userID); !own.Known || own.ChatPaused {
		if own.Known {
			return nil, notAllowed(ReasonYouPaused)
		}
		return nil, ErrMessagingNotAllowed
	}

	// DM gating (spec §1, §4): a non-connection cannot silently open a direct
	// DM. Depending on the target's privacy + relationship state the attempt
	// is permitted, downgraded to a Message Request, or rejected outright —
	// with graph's reason, so the client can say why.
	allowed, asRequest, reason, err := s.checkMessagePermission(ctx, userID, otherID)
	if err != nil {
		return nil, err
	}
	if !allowed && !asRequest {
		return nil, notAllowed(reason)
	}

	// Decline cooldown (directive §3.3): a recently deleted/blocked/reported
	// request bars a re-request for 7 days REGARDLESS of the idempotency key
	// presented. Checked outside the idempotency closure on purpose — a
	// rotated key is exactly the bypass this closes.
	//
	// The refusal never reveals what happened (DM consent model):
	//   - deleted ("ignored"): the requester gets the SAME conversation back,
	//     unchanged — still a request, still out of the recipient's
	//     Requests — exactly as if it were pending. Their one message is
	//     spent, so further sends get AWAITING_REQUEST_ACCEPTANCE as for any
	//     pending request.
	//   - blocked / reported: the requester's membership was severed, so
	//     there is nothing to hand back; they get the same answer graph gives
	//     for a block, "unavailable".
	if !allowed && asRequest {
		prev, err := s.groupStore().GetLatestRequestBetween(ctx, userID, otherID)
		if err != nil {
			return nil, err
		}
		if prev != nil && prev.RespondedAt != nil && time.Since(*prev.RespondedAt) < requestDeclineCooldown {
			switch prev.Status {
			case "ignored":
				return s.getConversationResponse(ctx, prev.ConversationID)
			case "blocked", "reported":
				return nil, notAllowed(ReasonUnavailable)
			}
		}
	}

	req := struct {
		UserID  uuid.UUID `json:"user_id"`
		OtherID uuid.UUID `json:"other_id"`
	}{UserID: userID, OtherID: otherID}
	return withIdempotency(ctx, s, idempotencyKey, req, func() (*ConversationResponse, error) {
		// DM consent model: the Postgres store opens, revives, reopens or
		// promotes the conversation in one transaction (see
		// OpenDirectConversation). The legacy steps below remain only for
		// stores without it (test fakes).
		if opener, ok := s.convStore.(directOpener); ok {
			return s.openDirect(ctx, opener, userID, otherID, allowed)
		}
		convID, created, err := s.convStore.CreateDirectConversation(ctx, userID, otherID, userID)
		if err != nil {
			return nil, err
		}
		// Downgrade to a Message Request only for a brand-new conversation —
		// an existing conversation means the pair could already talk.
		if !allowed && asRequest && created {
			// Rate-limit message-request creation (spec §10.4): a single
			// user may open at most messageRequestRateLimit.Max new
			// requests per window. Checked only on the actual creation
			// path so idempotent retries / existing conversations are free.
			// Fails CLOSED when the limiter store is unavailable.
			if err := s.checkRateLimitFailClosed(ctx, messageRequestRateLimit, userID); err != nil {
				return nil, err
			}
			if err := s.convStore.MarkConversationAsRequest(ctx, convID); err != nil {
				return nil, err
			}
			if err := s.convStore.CreateMessageRequest(ctx, convID, userID, otherID); err != nil {
				return nil, err
			}
			/*
				Tell the recipient a request exists.

				MessageRequestCreated used to be emitted ONLY from the message
				delivery path (delivery.go), keyed on a message id and carrying
				that message as the preview. So a request opened without a
				message — which is what the product now does when someone taps
				Message — created the row, put it in the recipient's Requests
				lane, and notified nobody. Observed on dev: request 19, created
				15:17, recipient's most recent notification two days older.

				Emitted here as well, deduped on the CONVERSATION so the two
				paths cannot double-notify: whichever happens first wins, and
				delivery.go's later insert for the same conversation is a
				no-op. The preview is empty because there is nothing to preview
				yet; notification-service already renders the no-preview case,
				and SetMessageRequestPreview fills it in if a message follows.
			*/
			payload := sharedEvents.MessageRequestPayload{
				ConversationID: convID.String(),
				SenderID:       userID.String(),
				ReceiverID:     otherID.String(),
				OccurredAt:     time.Now().UTC(),
			}
			if err := s.deliveryStore().InsertOutboxEventOnce(ctx,
				"message-request:"+convID.String(), sharedEvents.MessageRequestCreated, payload); err != nil {
				return nil, fmt.Errorf("queue message request event: %w", err)
			}
		}
		return s.getConversationResponse(ctx, convID)
	})
}

// CreateDatingMatchConversation provisions the 1:1 conversation that
// backs a freshly formed dating match. Bypasses the usual
// checkMessagePermission DM gate — the match itself is the consent
// signal, so requiring mutual-circle membership would lock matched
// pairs out of chat. Idempotent on matchID via a partial unique index;
// safe to retry from the match-formation saga.
//
// Caller is dating-service (internal-key gated at the handler).
// P0-3 in dating/PRODUCTION_GAP_ANALYSIS.md.
//
// firstMovers (dating mechanic M5) names who may send the first message;
// empty leaves the conversation open to both. They must be members of the
// pair. A retry re-applies them while no message has landed.
func (s *Service) CreateDatingMatchConversation(ctx context.Context, userA, userB, matchID uuid.UUID, opts DatingMatchOptions) (*ConversationResponse, error) {
	firstMovers := opts.FirstMovers
	if userA == userB {
		return nil, invalidInput("dating-match conversation requires two distinct users")
	}
	if matchID == uuid.Nil {
		return nil, invalidInput("match_id is required")
	}
	if err := validateFirstMovers(userA, userB, firstMovers); err != nil {
		return nil, err
	}
	convID, _, err := s.convStore.CreateDatingMatchConversation(ctx, userA, userB, matchID)
	if err != nil {
		return nil, err
	}
	if len(firstMovers) > 0 {
		if err := s.setDatingFirstMovers(ctx, convID, firstMovers); err != nil {
			return nil, err
		}
	}
	if opts.ReceiptsGated || opts.CallAfterExchange {
		st, err := s.datingExtras()
		if err != nil {
			return nil, err
		}
		if err := st.SetDatingConversationRules(ctx, convID, opts.ReceiptsGated, opts.CallAfterExchange); err != nil {
			return nil, err
		}
	}
	return s.getConversationResponse(ctx, convID)
}

// CloseDatingMatchConversation flips closed_at on the conversation
// keyed by matchID. Idempotent — a re-close preserves the original
// closed_at. Called by the dating-event consumer when
// match.closed / match.expired lands.
func (s *Service) CloseDatingMatchConversation(ctx context.Context, matchID uuid.UUID) error {
	if matchID == uuid.Nil {
		return invalidInput("match_id is required")
	}
	return s.convStore.MarkConversationClosedByMatch(ctx, matchID)
}

// HasOpenDatingMatch reports whether the two users currently hold an OPEN
// dating-match conversation. graph-service calls it (internal-key gated at
// the handler) to decide whether a matched pair may place a live call: a
// match opens chat and calls together, and closing the match closes both.
//
// It reads exactly the row the send-path gate reads, so a pair that can no
// longer message can no longer call either. Errors propagate — the caller
// fails closed on them.
func (s *Service) HasOpenDatingMatch(ctx context.Context, userA, userB uuid.UUID) (bool, error) {
	if userA == uuid.Nil || userB == uuid.Nil {
		return false, invalidInput("two user ids are required")
	}
	if userA == userB {
		return false, nil
	}
	return s.convStore.HasOpenDatingMatch(ctx, userA, userB)
}

// checkMessagePermission asks graph-service whether actor may DM target.
// Returns (allowed, asRequest, reason): allowed=true means a direct DM is
// permitted; allowed=false + asRequest=true means it must route through a
// Message Request; both false means messaging is not permitted at all, and
// reason (normalised by normalizeDenyReason) says why when graph said so.
func (s *Service) checkMessagePermission(ctx context.Context, actorID, targetID uuid.UUID) (allowed bool, asRequest bool, reason string, err error) {
	if s.graphServiceURL == "" {
		// Deploy misconfiguration. Fail closed on direct DMs but keep the
		// request path open so messaging is degraded, not bricked.
		s.log.Warn("GRAPH_SERVICE_URL not configured — DM gating degraded to request-only")
		return false, true, "", nil
	}
	url := fmt.Sprintf("%s/v1/permissions/check?target_user_id=%s&actions=message", s.graphServiceURL, targetID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, false, "", err
	}
	req.Header.Set("X-User-Id", actorID.String())
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false, false, "", fmt.Errorf("permission check request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, false, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return false, false, "", fmt.Errorf("permission check returned %d: %s", resp.StatusCode, string(body))
	}

	var envelope struct {
		Data struct {
			Decisions struct {
				Message struct {
					Allowed  bool   `json:"allowed"`
					Fallback string `json:"fallback"`
					Reason   string `json:"reason"`
				} `json:"message"`
			} `json:"decisions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false, false, "", fmt.Errorf("decode permission response: %w", err)
	}
	d := envelope.Data.Decisions.Message
	if !d.Allowed && d.Fallback != "message_request" {
		reason = normalizeDenyReason(d.Reason)
	}
	return d.Allowed, d.Fallback == "message_request", reason, nil
}

func (s *Service) CreateGroupConversation(ctx context.Context, userID uuid.UUID, title string, memberIDs []uuid.UUID, idempotencyKey string) (*ConversationResponse, error) {
	if title == "" {
		return nil, invalidInput("group title is required")
	}
	if len(memberIDs) < 1 {
		return nil, invalidInput("at least one other member is required")
	}

	req := struct {
		UserID    uuid.UUID   `json:"user_id"`
		Title     string      `json:"title"`
		MemberIDs []uuid.UUID `json:"member_ids"`
	}{UserID: userID, Title: title, MemberIDs: memberIDs}
	return withIdempotency(ctx, s, idempotencyKey, req, func() (*ConversationResponse, error) {
		convID, err := s.convStore.CreateGroupConversation(ctx, userID, title, memberIDs)
		if err != nil {
			return nil, err
		}

		// Outbox event
		allMembers := make([]string, 0, len(memberIDs)+1)
		allMembers = append(allMembers, userID.String())
		for _, m := range memberIDs {
			if m != userID {
				allMembers = append(allMembers, m.String())
			}
		}
		_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.ConversationCreated, sharedEvents.ConversationCreatedPayload{
			ConversationID: convID.String(),
			Type:           "group",
			Title:          title,
			CreatedBy:      userID.String(),
			MemberIDs:      allMembers,
			CreatedAt:      time.Now(),
		})

		return s.getConversationResponse(ctx, convID)
	})
}

func (s *Service) GetConversation(ctx context.Context, userID, conversationID uuid.UUID) (*ConversationResponse, error) {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}
	return s.getConversationResponseFor(ctx, userID, conversationID)
}

func (s *Service) listConversationsLegacy(ctx context.Context, userID uuid.UUID, limit int, cursor *ConversationCursor) ([]ConversationResponse, *ConversationCursor, error) {
	var cursorUpdatedAt *time.Time
	var cursorID *uuid.UUID
	if cursor != nil {
		cursorUpdatedAt = &cursor.UpdatedAt
		cursorID = &cursor.ID
	}

	convs, err := s.convStore.ListConversationsByUser(ctx, userID, limit, cursorUpdatedAt, cursorID)
	if err != nil {
		return nil, nil, err
	}

	// One cursor query for the whole page — never per row.
	convIDs := make([]uuid.UUID, len(convs))
	for i, c := range convs {
		convIDs[i] = c.ID
	}
	cursors, err := s.groupStore().GetReadCursors(ctx, userID, convIDs)
	if err != nil {
		s.log.Warn("read cursor batch failed — unread flags degraded", "err", err)
		cursors = nil
	}
	// Pinned/muted flags for the page, one query (never per row). Failure
	// degrades the flags to defaults rather than failing the inbox.
	settings, err := s.extrasStore().GetSettingsForConversations(ctx, userID, convIDs)
	if err != nil {
		s.log.Warn("conversation settings batch failed — pin/mute flags degraded", "err", err)
		settings = nil
	}

	out := make([]ConversationResponse, 0, len(convs))
	for _, c := range convs {
		members, err := s.convStore.GetMembers(ctx, c.ID)
		if err != nil {
			return nil, nil, err
		}
		enrichedMembers := s.enrichMembers(ctx, members)
		hasUnread := false
		if c.LastMessageAt != nil {
			// A message the viewer sent themselves is never "unread".
			if c.LastMessageSender == nil || *c.LastMessageSender != userID {
				rc, ok := cursors[c.ID]
				hasUnread = !ok || rc.LastReadAt.Before(*c.LastMessageAt)
			}
		}
		out = append(out, ConversationResponse{
			ID:                 c.ID,
			Type:               c.Type,
			Title:              c.Title,
			CreatedBy:          c.CreatedBy,
			IsRequest:          c.IsRequest,
			Members:            enrichedMembers,
			CreatedAt:          c.CreatedAt,
			UpdatedAt:          c.UpdatedAt,
			AvatarMediaID:      c.AvatarMediaID,
			Description:        c.Description,
			LastMessageAt:      c.LastMessageAt,
			LastMessagePreview: c.LastMessagePreview,
			LastMessageSender:  c.LastMessageSender,
			HasUnread:          hasUnread,
			IsPinned:           settings[c.ID].IsPinned,
			IsMuted:            settings[c.ID].IsMuted,
			SourceApp:          datingSourceApp(c.SourceApp),
			MatchID:            datingMatchID(c.SourceApp, c.MatchID),
		})
	}
	s.resolveGroupAvatarURLs(ctx, userID, out)

	var next *ConversationCursor
	if len(convs) == limit {
		last := convs[len(convs)-1]
		next = &ConversationCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	return out, next, nil
}

// --- Helpers ---

func (s *Service) getConversationResponse(ctx context.Context, convID uuid.UUID) (*ConversationResponse, error) {
	conv, err := s.convStore.GetConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return nil, notFound("conversation not found")
	}
	members, err := s.convStore.GetMembers(ctx, convID)
	if err != nil {
		return nil, err
	}
	enrichedMembers := s.enrichMembers(ctx, members)
	return &ConversationResponse{
		ID:                 conv.ID,
		Type:               conv.Type,
		Title:              conv.Title,
		CreatedBy:          conv.CreatedBy,
		IsRequest:          conv.IsRequest,
		Members:            enrichedMembers,
		CreatedAt:          conv.CreatedAt,
		UpdatedAt:          conv.UpdatedAt,
		AvatarMediaID:      conv.AvatarMediaID,
		Description:        conv.Description,
		LastMessageAt:      conv.LastMessageAt,
		LastMessagePreview: conv.LastMessagePreview,
		LastMessageSender:  conv.LastMessageSender,
		SourceApp:          datingSourceApp(conv.SourceApp),
		MatchID:            datingMatchID(conv.SourceApp, conv.MatchID),
	}, nil
}

// datingSourceApp shows the source app only for a Pulse chat.
func datingSourceApp(app string) string {
	if app == "dating" {
		return app
	}
	return ""
}

// datingMatchID shows the match id only for a Pulse chat.
func datingMatchID(app string, id *uuid.UUID) *uuid.UUID {
	if app != "dating" {
		return nil
	}
	return id
}

// getConversationResponseFor is getConversationResponse plus the viewer-
// scoped group avatar URL (chat-app pass).
func (s *Service) getConversationResponseFor(ctx context.Context, viewerID, convID uuid.UUID) (*ConversationResponse, error) {
	resp, err := s.getConversationResponse(ctx, convID)
	if err != nil {
		return nil, err
	}
	page := []ConversationResponse{*resp}
	s.resolveGroupAvatarURLs(ctx, viewerID, page)
	s.attachReadCursors(ctx, viewerID, &page[0])
	s.decorateRows(ctx, viewerID, page)
	s.attachMyJoin(ctx, viewerID, &page[0])
	s.attachConversationReceipts(ctx, viewerID, &page[0])
	return &page[0], nil
}
