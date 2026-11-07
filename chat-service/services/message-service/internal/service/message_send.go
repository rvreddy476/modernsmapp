// Sending a message: reply references, the content gates (links, request
// first-message rules), the message-request side of the sender, the SendRich
// pipeline with idempotency, and the frame body published to live sockets.

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// bareDomainRe matches an unadorned domain (e.g. "example.com") so a message
// request's first message cannot smuggle a link without an http(s):// prefix.
var bareDomainRe = regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9-]*\.(com|net|org|io|co|in|me|app|dev|xyz|info|link|to|ly|gg|sh)\b`)

// containsLink reports whether text contains a URL or bare domain. Message
// requests are link-free (spec §9.5) to blunt the most common spam vector.
func containsLink(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") || strings.Contains(lower, "www.") {
		return true
	}
	return bareDomainRe.MatchString(text)
}

// --- Messages ---

// ReplyRef is the sender-supplied quote: which message is being answered and
// a display snapshot of it. The preview is DENORMALISED — it is what the
// sender's client showed them, stored verbatim so the quote renders without
// a second lookup. It is display data, not evidence.
type ReplyRef struct {
	ID       uuid.UUID
	Preview  string
	SenderID uuid.UUID
}

func replyRefID(r *ReplyRef) *uuid.UUID {
	if r == nil {
		return nil
	}
	return &r.ID
}

func replyRefPreview(r *ReplyRef) string {
	if r == nil {
		return ""
	}
	return r.Preview
}

func replyRefSenderID(r *ReplyRef) *uuid.UUID {
	if r == nil {
		return nil
	}
	return &r.SenderID
}

func (s *Service) SendMessage(ctx context.Context, userID, conversationID uuid.UUID, msgType, text string, mediaID, postID *uuid.UUID, replyTo *ReplyRef, idempotencyKey string) (*MessageResponse, error) {
	return s.SendRich(ctx, userID, conversationID, SendInput{
		Type: msgType, Text: text, MediaID: mediaID, PostID: postID, ReplyTo: replyTo, IdempotencyKey: idempotencyKey,
	})
}

// contentGates are the send gates that apply to anything a member puts in
// front of the others, so a send and an edit (H2) refuse the same things.
// The caller has already checked membership. It returns the conversation's
// dating meta for the first-move gate.
func (s *Service) contentGates(ctx context.Context, userID, conversationID uuid.UUID) (*postgres.ConversationMeta, error) {
	// Chat pause (directive §3.2, P0-1 correction): a paused SENDER sends
	// nothing; a paused direct RECIPIENT receives nothing new. Both read the
	// LOCAL policy projection — no HTTP on the hot path when the projection
	// is fresh. An UNKNOWN policy fails closed like a pause: the Postgres
	// projection (bounded by policyStaleGrace) carries availability through a
	// short identity outage, so "unknown" means the pause state genuinely
	// cannot be established, and sending anyway would override a pause we
	// cannot see.
	if own := s.GetChatPolicy(ctx, userID); !own.Known || own.ChatPaused {
		return nil, ErrMessagingNotAllowed
	}
	// P0-3 dating-match send gate: reject sends to a closed dating
	// conversation (match unmatched / expired / one side blocked /
	// paused / suspended). The closed_at flag is flipped by the
	// dating-event consumer when match.closed / match.expired land.
	// Membership already covers the block case via
	// conversation_members.left_at; the closed_at check is the
	// dating-side equivalent.
	meta, err := s.convStore.GetConversationMeta(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if meta != nil && meta.SourceApp == "dating" {
		if meta.ClosedAt != nil {
			return nil, ErrMatchClosed
		}
		// Audit H2: closed_at waits on an event. Until it lands, dating's
		// authoritative state decides (suspension, chat hold, deletion,
		// block, unmatch), through a short cache; it fails closed.
		if err := s.requireOpenDatingMatch(ctx, meta.MatchID); err != nil {
			return nil, err
		}
	}
	// Direct-recipient pause (P0-1 correction): look up the other member's
	// projected policy. Lookup errors propagate — swallowing them turned a
	// database hiccup into a delivery to a possibly-paused recipient — and an
	// unknown recipient policy fails closed for the same reason as above.
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if conv != nil && conv.Type == "direct" {
		members, err := s.convStore.GetMembers(ctx, conversationID)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			if m.UserID == userID {
				continue
			}
			if p := s.GetChatPolicy(ctx, m.UserID); !p.Known || p.ChatPaused {
				return nil, ErrMessagingNotAllowed
			}
		}
	}

	return meta, nil
}

// SendRich is the one send pipeline (chat parity §2.1): SendMessage is the
// legacy positional wrapper. Every gate below applies to every type.
func (s *Service) SendRich(ctx context.Context, userID, conversationID uuid.UUID, in SendInput) (*MessageResponse, error) {
	msgType, text, mediaID, postID, replyTo, idempotencyKey := in.Type, in.Text, in.MediaID, in.PostID, in.ReplyTo, in.IdempotencyKey
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, ErrIdempotencyKeyRequired
	}
	if err := validateSendInput(in); err != nil {
		return nil, err
	}
	// A post card runs every gate below unchanged — membership (blocks sever
	// it), chat pause on either side, dating closure / first move, rate limit
	// and the message-request rules. Who-can-message was decided when the
	// conversation was opened; a post is just another message in it.
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}
	meta, err := s.contentGates(ctx, userID, conversationID)
	if err != nil {
		return nil, err
	}

	req := struct {
		UserID         uuid.UUID  `json:"user_id"`
		ConversationID uuid.UUID  `json:"conversation_id"`
		Type           string     `json:"type"`
		Text           string     `json:"text"`
		MediaID        *uuid.UUID `json:"media_id,omitempty"`
		PostID         *uuid.UUID `json:"post_id,omitempty"` // omitempty: pre-existing request hashes unchanged
		ReplyToID      *uuid.UUID `json:"reply_to_id,omitempty"`
		// Chat parity rich fields — all omitempty, so the hash of every
		// pre-existing text / media / post request is unchanged.
		ReplyNoteID   *uuid.UUID     `json:"reply_note_id,omitempty"`
		DurationMs    int            `json:"duration_ms,omitempty"`
		Waveform      []int          `json:"waveform,omitempty"`
		StickerID     *uuid.UUID     `json:"sticker_id,omitempty"`
		GIF           *gifHashRef    `json:"gif,omitempty"`
		ProfileUserID *uuid.UUID     `json:"profile_user_id,omitempty"`
		SoundID       *uuid.UUID     `json:"sound_id,omitempty"`
		StreamID      *uuid.UUID     `json:"stream_id,omitempty"`
		Forwarded     bool           `json:"forwarded,omitempty"`
		Location      *LocationInput `json:"location,omitempty"`
	}{UserID: userID, ConversationID: conversationID, Type: msgType, Text: text, MediaID: mediaID, PostID: postID,
		ReplyNoteID: in.ReplyNoteID, DurationMs: in.DurationMs, Waveform: in.Waveform, StickerID: in.StickerID, GIF: gifHashOf(in.GIF),
		ProfileUserID: in.ProfileUserID, SoundID: in.SoundID, StreamID: in.StreamID, Forwarded: in.Forwarded,
		Location: in.Location}
	if replyTo != nil {
		req.ReplyToID = &replyTo.ID
	}
	requestHash, err := hashRequestPayload(req)
	if err != nil {
		return nil, err
	}
	return withIdempotency(ctx, s, idempotencyKey, req, func() (*MessageResponse, error) {
		// DM rate limit (spec §10.4): cap message sends per user per window.
		// Inside the idempotency closure so a 429 releases the key and an
		// idempotent retry of a previously-accepted send returns the cache.
		if err := s.checkRateLimit(ctx, dmRateLimit, userID); err != nil {
			return nil, err
		}

		// Message-request gating (spec §3.3, §9.5): in a pending request
		// conversation only the original sender may post, and only a single
		// constrained first message until the recipient accepts. This runs
		// inside the idempotency closure so an idempotent retry returns the
		// cached response instead of being rejected as a follow-up.
		// Dating mechanic M5: until the first message lands, only a first
		// mover may send. Inside the closure so an idempotent retry of an
		// accepted send returns the cached response. The opening answer
		// dating-service validated is the one send let through.
		if firstMoveBlocks(meta, userID) && !firstMoveBypassed(ctx) {
			return nil, ErrFirstMovePending
		}

		firstRequestMessage := false
		conv, err := s.convStore.GetConversation(ctx, conversationID)
		if err != nil {
			return nil, err
		}
		if conv != nil && conv.IsRequest {
			mr, err := s.convStore.GetMessageRequestByConversation(ctx, conversationID)
			if err != nil {
				return nil, err
			}
			side := requestSideOf(mr, userID, time.Now())
			if side == requestClosed {
				return nil, ErrAwaitingRequestAcceptance
			}
			if side == requestRecipient {
				// REPLYING IS ACCEPTING.
				//
				// The recipient used to be refused here and told to tap
				// Accept first — a second step for a decision they have
				// already made, because nobody types a reply to someone
				// they are declining. Answering is the clearest consent
				// there is, so it is honoured as consent.
				//
				// This runs the SAME path as the Accept button, so the
				// request status and the MessageRequestAccepted event
				// still happen exactly once (accepting no longer makes a
				// graph connection — DM consent model, 20 Oct 2026). Decline and Block are untouched and remain the
				// only other ways out.
				//
				// A failed accept is fatal to the send: delivering a reply
				// into a conversation still marked pending would leave the
				// recipient having spoken on an unresolved request.
				if err := s.AcceptRequest(ctx, userID, conversationID); err != nil {
					return nil, err
				}
				// No longer a request, so none of the first-message limits
				// apply to this reply.
				conv.IsRequest = false
			} else {
				if mr.Preview != "" {
					// The one allowed first message was already sent. A fast
					// path only: ReserveMessageDeliveryIntent's claim decides
					// between sends racing past this check.
					return nil, ErrAwaitingRequestAcceptance
				}
				// A post card is refused here exactly like a link: the
				// single message allowed before acceptance is plain text,
				// so a stranger cannot push content (or a link-equivalent
				// card) into a request. Once the recipient accepts — or
				// replies, which accepts — posts flow like any message.
				if msgType == MessageTypePost {
					return nil, fmt.Errorf("%w: posts are not allowed until the request is accepted", ErrRequestFirstMessageInvalid)
				}
				// A location is refused the same way (23 Oct 2026): where
				// someone is never reaches a person who has not accepted them.
				if msgType == MessageTypeLocation {
					return nil, fmt.Errorf("%w: locations are not allowed until the request is accepted", ErrRequestFirstMessageInvalid)
				}
				if msgType != MessageTypeText || strings.TrimSpace(text) == "" {
					return nil, fmt.Errorf("%w: first message must be non-empty text", ErrRequestFirstMessageInvalid)
				}
				if containsLink(text) {
					return nil, fmt.Errorf("%w: links are not allowed", ErrRequestFirstMessageInvalid)
				}
				if utf8.RuneCountInString(text) > requestFirstMessageMaxRunes {
					return nil, fmt.Errorf("%w: exceeds %d characters", ErrRequestFirstMessageInvalid, requestFirstMessageMaxRunes)
				}
				firstRequestMessage = true
			}
		}

		now := time.Now()
		msgID := uuid.New()
		bucket := now.UTC().Format("200601")
		members, err := s.convStore.GetMembers(ctx, conversationID)
		if err != nil {
			return nil, err
		}
		var requestReceiverID *uuid.UUID
		if firstRequestMessage {
			for _, member := range members {
				if member.UserID != userID {
					receiver := member.UserID
					requestReceiverID = &receiver
					break
				}
			}
		}
		sourceApp := "chat"
		var matchID *uuid.UUID
		if meta != nil && meta.SourceApp != "" {
			sourceApp = meta.SourceApp
			matchID = meta.MatchID
		}
		// Chat parity §2.1: resolve the rich payload (catalogue sticker,
		// client gif, note reply, attachment metadata) before anything is
		// reserved, so a refusal leaves nothing behind.
		extra, replyResolved, err := s.buildExtra(ctx, userID, in, activeMemberIDs(members))
		if err != nil {
			return nil, err
		}
		if replyResolved != nil {
			replyTo = replyResolved
		}
		// Disappearing messages: the timer in force NOW is captured into the
		// message itself. The durable intent stores it, so a replay keeps the
		// first attempt's expiry and a later timer change never moves it.
		if meta != nil {
			extra.ExpiresAt = disappearExpiry(now, meta.DisappearAfterMs)
		}
		if mediaID != nil && !in.forwardedMediaOK {
			// The reference is stable across retries but opaque outside this
			// service. media-service atomically validates and pins the asset
			// before any cross-store message mutation starts.
			referenceID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("atpost-chat-attachment:"+userID.String()+":"+idempotencyKey))
			if err := s.reserveChatAttachment(ctx, referenceID, userID, *mediaID); err != nil {
				return nil, err
			}
		}
		extraBytes, _ := encodeExtra(extra)
		intent, err := s.deliveryStore().ReserveMessageDeliveryIntent(ctx, postgres.MessageDeliveryIntent{
			IdempotencyKey:    idempotencyKey,
			RequestHash:       requestHash,
			ConversationID:    conversationID,
			SenderID:          userID,
			MessageID:         msgID,
			Bucket:            bucket,
			MessageTS:         now,
			MessageType:       msgType,
			MessageText:       text,
			MediaID:           mediaID,
			PostID:            postID,
			MemberIDs:         activeMemberIDs(members),
			FirstRequest:      firstRequestMessage,
			RequestReceiverID: requestReceiverID,
			SourceApp:         sourceApp,
			MatchID:           matchID,
			ReplyToID:         replyRefID(replyTo),
			ReplyToPreview:    replyRefPreview(replyTo),
			ReplyToSenderID:   replyRefSenderID(replyTo),
			Extra:             extraBytes,
		})
		if err != nil {
			if errors.Is(err, postgres.ErrDeliveryIntentConflict) {
				return nil, ErrIdempotencyConflict
			}
			// The preview check above is only a fast path: parallel sends
			// with different keys all pass it. The reservation's claim is
			// the authority, and a send that lost it gets the same answer
			// as any second message into a pending request.
			if errors.Is(err, postgres.ErrRequestFirstMessageTaken) {
				return nil, ErrAwaitingRequestAcceptance
			}
			return nil, err
		}
		now, msgID, bucket = intent.MessageTS, intent.MessageID, intent.Bucket

		l := s.log.With("conversation_id", conversationID, "sender_id", userID, "msg_id", msgID)
		if err := s.settleDelivery(ctx, intent, text, replyTo, extraBytes); err != nil {
			l.Error("message delivery is pending durable repair", "err", err)
			return nil, err
		}
		// The durable intent is the truth on a replayed key.
		storedExtra := decodeExtra(string(intent.Extra))

		// 5. Real-time delivery via Redis pub/sub to all members (best-effort).
		// Runs after the durable outbox handoff so a successful publish
		// always corresponds to a queued outbox event.
		//
		// Phase 1 §1: for dating_match conversations we tag the payload
		// with source_app + match_id so ws-gateway / clients can route to
		// the dating UI rather than the generic chat list.
		var sourceAppMeta string
		var matchIDMeta string
		if meta != nil && meta.SourceApp == "dating" {
			sourceAppMeta = meta.SourceApp
			if meta.MatchID != nil {
				matchIDMeta = meta.MatchID.String()
			}
		}
		go func() {
			pubCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			msgBody := messageFrameBody(conversationID, msgID, userID, msgType, text, mediaID, postID, bucket, now)
			if intent.ReplyToID != nil {
				msgBody["reply_to_id"] = intent.ReplyToID
				msgBody["reply_to_preview"] = intent.ReplyToPreview
				msgBody["reply_to_sender_id"] = intent.ReplyToSenderID
			}
			if sourceAppMeta != "" {
				msgBody["source_app"] = sourceAppMeta
			}
			if matchIDMeta != "" {
				msgBody["match_id"] = matchIDMeta
			}
			addExtraToFrame(msgBody, storedExtra)
			payload, _ := json.Marshal(map[string]interface{}{
				"type":    "message",
				"payload": msgBody,
			})
			// M2: per-member fan-out collapsed into a single Redis
			// pipeline. The previous loop issued one round-trip per
			// member, which for a 100-member group added 100×RTT to
			// the send (~100ms in the same DC, much worse cross-AZ).
			// Pipeline batches the PUBLISH commands and waits for one
			// flush. Pub/Sub fan-out semantics stay identical from
			// the ws-gateway's point of view — it subscribes per
			// chat:<user_id> channel as before.
			pipe := s.rdb.Pipeline()
			recipients := 0
			for _, m := range members {
				// The sender gets this too, and that is deliberate.
				//
				// This channel is message DELIVERY, not notification.
				// Skipping the sender meant the tab that sent the message
				// was the only place it ever appeared: a second tab, a
				// second browser or the phone, all signed in as the same
				// person, never learned the message existed until a full
				// reload. Found on 22 Sep 2026 from two browsers showing
				// different message lists for the same conversation.
				//
				// The sending tab already holds an optimistic copy, so it
				// receives its own frame back. Clients de-duplicate by
				// message_id — the same rule that already makes the
				// convroom publish below safe alongside this one.
				publishPersonal(pubCtx, pipe, m, payload)
				recipients++
			}
			// Conversation-room publish (scoped-rooms foundation, directive
			// §5.3): entitled gateway subscriptions receive the frame once
			// per conversation instead of once per member. Personal-channel
			// fan-out stays (it is the public delivery path today); clients
			// de-duplicate by message_id. One extra PUBLISH, harmless when
			// nothing is subscribed.
			pipe.Publish(pubCtx, fmt.Sprintf("convroom:%s", conversationID), payload)
			// Always flush. The convroom frame is queued on this same
			// pipeline, so gating Exec on the per-member count silently
			// dropped it whenever there were no other members — the
			// pipeline was built and then never sent.
			if _, err := pipe.Exec(pubCtx); err != nil {
				l.Warn("failed to pipeline-publish to redis pubsub", "err", err, "recipients", recipients)
			}
		}()

		// The Redis list chat_messages:{conversation} that used to be pushed
		// here was written and never read: a third, unencrypted copy of every
		// message with no expiry. Removed with field encryption (22 Oct
		// 2026); cmd/fieldcrypt-backfill -redis deletes the lists left behind.

		resp := &MessageResponse{
			ConversationID:  conversationID,
			Bucket:          bucket,
			Ts:              now,
			MsgID:           msgID,
			SenderID:        userID,
			Type:            msgType,
			Text:            text,
			MediaID:         mediaID,
			PostID:          postID,
			ReplyToID:       intent.ReplyToID,
			ReplyToPreview:  intent.ReplyToPreview,
			ReplyToSenderID: intent.ReplyToSenderID,
			CreatedAt:       now,
		}
		applyExtra(resp, storedExtra)
		one := []MessageResponse{*resp}
		s.resolveStickerURLs(ctx, userID, one)
		return &one[0], nil
	})
}

// requestFirstMessageMaxRunes caps the single message allowed in a pending
// message request (send and edit alike).
const requestFirstMessageMaxRunes = 500

// requestSide is who a member is in a request conversation, by the rule
// send applies.
type requestSide int

const (
	// requestClosed: no pending, unexpired request; nobody may write.
	requestClosed requestSide = iota
	// requestSender: the requester; one constrained first message only.
	requestSender
	// requestRecipient: the recipient; replying accepts the request.
	requestRecipient
)

// requestSideOf: a request past its 30-day expiry is closed even before the
// sweep marks it (zero ExpiresAt: an envelope without one).
func requestSideOf(mr *postgres.MessageRequest, userID uuid.UUID, now time.Time) requestSide {
	if mr == nil || mr.Status != "pending" || (!mr.ExpiresAt.IsZero() && !mr.ExpiresAt.After(now)) {
		return requestClosed
	}
	if userID == mr.SenderID {
		return requestSender
	}
	return requestRecipient
}

// messageFrameBody is the realtime "message" frame payload published to
// chat:<user> and convroom:<conversation>. post_id rides only on a post card;
// it is the card's whole content (no post data is copied — see
// MessageTypePost).
func messageFrameBody(conversationID, msgID, senderID uuid.UUID, msgType, text string, mediaID, postID *uuid.UUID, bucket string, createdAt time.Time) map[string]interface{} {
	body := map[string]interface{}{
		"conversation_id": conversationID,
		"message_id":      msgID,
		"sender_id":       senderID,
		"type":            msgType,
		"text":            text,
		"media_id":        mediaID,
		"created_at":      createdAt,
		// The message's Scylla address, as in the HTTP send response: edit,
		// delete and reactions address a message by (bucket, ts, msg_id), so
		// a client holding only this frame (the sender's own echo, or a
		// recipient) can act on it without refetching the page.
		"msg_id": msgID,
		"bucket": bucket,
		"ts":     createdAt,
	}
	if postID != nil {
		body["post_id"] = postID
	}
	return body
}
