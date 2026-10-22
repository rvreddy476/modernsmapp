package postgres

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/atpost/chat-message-service/internal/contentcrypt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DM consent model (founder-approved 20 Oct 2026, contract §5).
//
// Nobody lands in someone's Chats without consent. A message request is the
// only way a stranger reaches you; Delete removes it from your Requests
// without telling the sender and bars a new request for 7 days; an
// unanswered request expires after 30 days; and once the recipient follows
// the sender the pair simply chat.

// RequestTTL is how long a request waits for an answer before it expires
// (the SQL spells it INTERVAL '30 days', like the expires_at column default).
const RequestTTL = 30 * 24 * time.Hour

// DirectOpenMode is what the DM permission decision allowed for this attempt.
type DirectOpenMode int

const (
	// OpenDirect: graph allowed a direct chat (the recipient follows the
	// sender, or a legacy connection).
	OpenDirect DirectOpenMode = iota
	// OpenAsRequest: graph downgraded the attempt to a message request.
	OpenAsRequest
)

// DirectOpenOutcome says what OpenDirectConversation did.
type DirectOpenOutcome string

const (
	// DirectExisting: the conversation was returned as it was.
	DirectExisting DirectOpenOutcome = "existing"
	// DirectCreated: a normal chat was created, or a severed one revived.
	DirectCreated DirectOpenOutcome = "created"
	// DirectPromoted: a request conversation became a normal chat because
	// the decision now allows a direct chat.
	DirectPromoted DirectOpenOutcome = "promoted"
	// RequestCreated: a brand-new conversation was opened as a request.
	RequestCreated DirectOpenOutcome = "request_created"
	// RequestReopened: an existing conversation (severed, or whose last
	// request was declined or expired) was opened as a fresh request.
	RequestReopened DirectOpenOutcome = "request_reopened"
)

// IsNewRequest reports whether the outcome put a new request in the
// recipient's Requests.
func (o DirectOpenOutcome) IsNewRequest() bool {
	return o == RequestCreated || o == RequestReopened
}

// DirectOpenResult is OpenDirectConversation's answer.
type DirectOpenResult struct {
	ConversationID uuid.UUID
	Outcome        DirectOpenOutcome
	// RequestCreatedAt is the new request's creation time (only when
	// Outcome.IsNewRequest()); it keys the notification dedupe.
	RequestCreatedAt time.Time
}

// OpenDirectConversation finds or creates the pair's 1:1 conversation and
// applies the permission decision to it, all in ONE transaction under the
// same per-pair advisory lock CreateDirectConversation takes:
//
//   - no conversation yet: create it, as a request when mode is
//     OpenAsRequest (conversation flag and envelope committed together, so a
//     crash can never leave a stranger with a normal chat);
//   - severed by a block (since lifted): revive it, as a request when mode
//     is OpenAsRequest;
//   - OpenAsRequest and the last request was declined ("ignored") or has
//     expired: reopen it as a fresh request from actorID — the recipient's
//     history before now is hidden so it reads as a new request;
//   - OpenDirect and it is a request conversation the actor sent, or a
//     finished one: promote it to a normal chat (a still-pending request the
//     ACTOR received is left alone: replying is what accepts it);
//   - anything else: return it unchanged.
//
// beforeNewRequest runs inside the transaction only when a new request is
// about to be written (the message-request rate limit); an error aborts
// and rolls everything back. The decline cooldown is the caller's check.
func (s *ConversationStore) OpenDirectConversation(ctx context.Context, actorID, otherID uuid.UUID, mode DirectOpenMode, beforeNewRequest func(context.Context) error) (DirectOpenResult, error) {
	if actorID == otherID {
		return DirectOpenResult{}, errors.New("cannot create conversation with self")
	}
	userA, userB := actorID, otherID
	if userA.String() > userB.String() {
		userA, userB = userB, userA
	}
	pairKey := userA.String() + ":" + userB.String()
	asRequest := mode == OpenAsRequest
	gate := func() error {
		if beforeNewRequest == nil {
			return nil
		}
		return beforeNewRequest(ctx)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return DirectOpenResult{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, pairKey); err != nil {
		return DirectOpenResult{}, err
	}

	var conversationID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT conversation_id FROM chat.direct_conversation_keys WHERE user_a = $1 AND user_b = $2`,
		userA, userB).Scan(&conversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		if asRequest {
			if err := gate(); err != nil {
				return DirectOpenResult{}, err
			}
		}
		conversationID = uuid.New()
		now := time.Now()
		if _, err := tx.Exec(ctx, `INSERT INTO chat.conversations (id, type, created_by, created_at, updated_at, is_request) VALUES ($1, 'direct', $2, $3, $3, $4)`,
			conversationID, actorID, now, asRequest); err != nil {
			return DirectOpenResult{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at, join_gen) VALUES ($1, $2, 'member', $3, nextval('chat.membership_gen_seq')), ($1, $4, 'member', $3, nextval('chat.membership_gen_seq'))`,
			conversationID, userA, now, userB); err != nil {
			return DirectOpenResult{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO chat.direct_conversation_keys (user_a, user_b, conversation_id) VALUES ($1, $2, $3)`,
			userA, userB, conversationID); err != nil {
			return DirectOpenResult{}, err
		}
		res := DirectOpenResult{ConversationID: conversationID, Outcome: DirectCreated}
		if asRequest {
			createdAt, err := upsertPendingRequest(ctx, tx, conversationID, actorID, otherID)
			if err != nil {
				return DirectOpenResult{}, err
			}
			res.Outcome, res.RequestCreatedAt = RequestCreated, createdAt
		}
		if err := tx.Commit(ctx); err != nil {
			return DirectOpenResult{}, err
		}
		return res, nil
	}
	if err != nil {
		return DirectOpenResult{}, err
	}

	var isRequest bool
	var severed int
	if err := tx.QueryRow(ctx, `
		SELECT c.is_request,
		       (SELECT COUNT(*) FROM chat.conversation_members m WHERE m.conversation_id = c.id AND m.left_at IS NOT NULL)
		FROM chat.conversations c WHERE c.id = $1 FOR UPDATE
	`, conversationID).Scan(&isRequest, &severed); err != nil {
		return DirectOpenResult{}, err
	}
	res := DirectOpenResult{ConversationID: conversationID, Outcome: DirectExisting}

	// A block severed it and the block is gone (the caller's permission
	// check passed): revive, as a request when that is all graph allows.
	if severed > 0 {
		if asRequest {
			if err := gate(); err != nil {
				return DirectOpenResult{}, err
			}
		}
		if _, err := reviveSeveredDirect(ctx, tx, conversationID); err != nil {
			return DirectOpenResult{}, err
		}
		res.Outcome = DirectCreated
		if asRequest {
			if err := markConversationRequest(ctx, tx, conversationID); err != nil {
				return DirectOpenResult{}, err
			}
			createdAt, err := upsertPendingRequest(ctx, tx, conversationID, actorID, otherID)
			if err != nil {
				return DirectOpenResult{}, err
			}
			res.Outcome, res.RequestCreatedAt = RequestReopened, createdAt
		} else if err := settleRequestAsAccepted(ctx, tx, conversationID); err != nil {
			return DirectOpenResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return DirectOpenResult{}, err
		}
		return res, nil
	}

	if !isRequest {
		return res, nil // a normal chat: the pair can already talk
	}
	var mrStatus string
	var mrReceiver uuid.UUID
	var mrExpires time.Time
	err = tx.QueryRow(ctx, `
		SELECT status, receiver_id, expires_at FROM chat.message_requests WHERE conversation_id = $1 FOR UPDATE
	`, conversationID).Scan(&mrStatus, &mrReceiver, &mrExpires)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, nil // legacy request row without an envelope: leave it
	}
	if err != nil {
		return DirectOpenResult{}, err
	}
	live := mrStatus == "pending" && mrExpires.After(time.Now())
	finished := mrStatus == "ignored" || mrStatus == "expired" || (mrStatus == "pending" && !live)

	if asRequest {
		if !finished {
			// The same request retried, or the other side's pending
			// request (the actor answers it by replying).
			return res, nil
		}
		if err := gate(); err != nil {
			return DirectOpenResult{}, err
		}
		createdAt, err := upsertPendingRequest(ctx, tx, conversationID, actorID, otherID)
		if err != nil {
			return DirectOpenResult{}, err
		}
		if err := markConversationRequest(ctx, tx, conversationID); err != nil {
			return DirectOpenResult{}, err
		}
		// The recipient sees a fresh request, not the thread they
		// already deleted.
		if _, err := tx.Exec(ctx, `
			UPDATE chat.conversation_members
			SET cleared_before = NOW(), hidden = FALSE, unread_count = 0, marked_unread = FALSE
			WHERE conversation_id = $1 AND user_id = $2
		`, conversationID, otherID); err != nil {
			return DirectOpenResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return DirectOpenResult{}, err
		}
		res.Outcome, res.RequestCreatedAt = RequestReopened, createdAt
		return res, nil
	}

	// OpenDirect on a request conversation.
	if live && mrReceiver == actorID {
		return res, nil // the actor's own incoming request: replying accepts it
	}
	if !live && !finished {
		return res, nil // accepted / blocked / reported: nothing to promote
	}
	if err := settleRequestAsAccepted(ctx, tx, conversationID); err != nil {
		return DirectOpenResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE chat.conversations
		SET is_request = FALSE, request_declined_at = NULL,
		    request_accepted_at = COALESCE(request_accepted_at, NOW()), updated_at = NOW()
		WHERE id = $1
	`, conversationID); err != nil {
		return DirectOpenResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DirectOpenResult{}, err
	}
	res.Outcome = DirectPromoted
	return res, nil
}

// settleRequestAsAccepted records that the pair now chat by consent (the
// decision allowed a direct chat): an unanswered, deleted or expired
// envelope becomes 'accepted', so the envelope never contradicts a normal
// chat (DirectChatAccepted relies on that). Block and report records stay.
func settleRequestAsAccepted(ctx context.Context, tx pgx.Tx, conversationID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE chat.message_requests SET status = 'accepted', responded_at = NOW()
		WHERE conversation_id = $1 AND status IN ('pending', 'ignored', 'expired')
	`, conversationID)
	return err
}

// markConversationRequest flags the conversation as a live request again.
func markConversationRequest(ctx context.Context, tx pgx.Tx, conversationID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE chat.conversations
		SET is_request = TRUE, request_declined_at = NULL, request_accepted_at = NULL, updated_at = NOW()
		WHERE id = $1
	`, conversationID)
	return err
}

// upsertPendingRequest writes a fresh pending envelope from sender to
// receiver (one row per conversation) and returns its creation time.
func upsertPendingRequest(ctx context.Context, tx pgx.Tx, conversationID, senderID, receiverID uuid.UUID) (time.Time, error) {
	var createdAt time.Time
	err := tx.QueryRow(ctx, `
		INSERT INTO chat.message_requests (conversation_id, sender_id, receiver_id, created_at, expires_at)
		VALUES ($1, $2, $3, NOW(), NOW() + INTERVAL '30 days')
		ON CONFLICT (conversation_id) DO UPDATE
		SET sender_id = EXCLUDED.sender_id,
		    receiver_id = EXCLUDED.receiver_id,
		    preview = '',
		    status = 'pending',
		    risk_score = 0,
		    created_at = EXCLUDED.created_at,
		    responded_at = NULL,
		    expires_at = EXCLUDED.expires_at
		RETURNING created_at
	`, conversationID, senderID, receiverID).Scan(&createdAt)
	return createdAt, err
}

// ExpiredRequest is one request the expiry sweep closed.
type ExpiredRequest struct {
	ConversationID uuid.UUID
	SenderID       uuid.UUID
	ReceiverID     uuid.UUID
	CreatedAt      time.Time
}

// ExpireDueMessageRequests marks at most limit pending requests whose
// expires_at has passed as 'expired' and returns them. One statement: the
// SKIP LOCKED subquery lets several replicas sweep side by side without
// blocking or double-expiring, and the status guard makes a replay a no-op.
// The conversation keeps is_request; every Requests query requires a live
// pending envelope, so the request simply leaves the recipient's Requests.
func (s *ConversationStore) ExpireDueMessageRequests(ctx context.Context, limit int) ([]ExpiredRequest, error) {
	if limit <= 0 {
		return nil, nil
	}
	// A MATERIALIZED CTE, not `WHERE id IN (subquery)`: the planner may
	// rescan an IN subquery, and each rescan with SKIP LOCKED + LIMIT picks
	// a fresh set — observed expiring 5 rows for a limit of 2.
	rows, err := s.db.Query(ctx, `
		WITH due AS MATERIALIZED (
			SELECT id FROM chat.message_requests
			WHERE status = 'pending' AND expires_at <= NOW()
			ORDER BY expires_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE chat.message_requests mr
		SET status = 'expired', responded_at = NOW()
		FROM due
		WHERE mr.id = due.id AND mr.status = 'pending'
		RETURNING mr.conversation_id, mr.sender_id, mr.receiver_id, mr.created_at
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExpiredRequest
	for rows.Next() {
		var e ExpiredRequest
		if err := rows.Scan(&e.ConversationID, &e.SenderID, &e.ReceiverID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RequestPageCursor is the position after the last row of a requests page:
// (request created_at, conversation id), newest first.
type RequestPageCursor struct {
	At time.Time
	ID uuid.UUID
}

// liveRequest is the predicate every Requests list shares: a request
// conversation the viewer ($1) is still an active member of, not deleted,
// whose envelope is pending and not past its expiry (the read-side guard for
// the window before the sweep). A request row without an envelope (legacy)
// still counts.
const liveRequest = `c.is_request = TRUE AND c.request_declined_at IS NULL
		  AND (mr.id IS NULL OR (mr.status = 'pending' AND mr.expires_at > NOW()))`

// incomingLiveRequest is a live request the viewer ($1) has to answer. A
// request without an envelope is incoming unless the viewer created it.
const incomingLiveRequest = `(COALESCE(mr.receiver_id = $1, c.created_by IS DISTINCT FROM $1) AND ` + liveRequest + `)`

// outgoingRequestView is a request the viewer ($1) sent, as the SENDER sees
// it. A deleted ("ignored") request still shows exactly like a pending one
// until the same 30-day expiry: the requester is never told it was deleted.
const outgoingRequestView = `(COALESCE(mr.sender_id = $1, c.created_by = $1) AND c.is_request = TRUE
		  AND (mr.id IS NULL OR (mr.status IN ('pending', 'ignored') AND mr.expires_at > NOW())))`

// ListRequestEnvelopesPage returns up to limit request rows for the viewer,
// newest request first, strictly after the cursor. direction is "incoming"
// (live requests the viewer must answer), "outgoing" (requests the viewer
// sent, see outgoingRequestView) or "all" (both). The order key is the
// REQUEST's creation time, so a reopened request sorts as new.
func (s *ConversationStore) ListRequestEnvelopesPage(ctx context.Context, userID uuid.UUID, direction string, after *RequestPageCursor, limit int) ([]RequestEnvelope, error) {
	if limit <= 0 {
		limit = 20
	}
	var where string
	switch direction {
	case "incoming":
		where = incomingLiveRequest
	case "outgoing":
		where = outgoingRequestView
	default:
		where = `(` + incomingLiveRequest + ` OR ` + outgoingRequestView + `)`
	}
	args := []any{userID}
	q := `
		SELECT c.id, c.type, c.title, c.created_by, c.is_request, c.created_at, c.updated_at,
		       COALESCE(mr.sender_id, '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE(mr.receiver_id, '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE(mr.preview, ''), COALESCE(mr.status, ''),
		       COALESCE(mr.created_at, c.created_at)
		FROM chat.conversations c
		JOIN chat.conversation_members m ON m.conversation_id = c.id AND m.user_id = $1 AND m.left_at IS NULL
		LEFT JOIN chat.message_requests mr ON mr.conversation_id = c.id
		WHERE ` + where
	if after != nil {
		args = append(args, after.At, after.ID)
		q += ` AND (COALESCE(mr.created_at, c.created_at), c.id) < ($2, $3)`
	}
	args = append(args, limit)
	q += ` ORDER BY COALESCE(mr.created_at, c.created_at) DESC, c.id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestEnvelope
	for rows.Next() {
		var e RequestEnvelope
		c := &e.Conversation
		if err := rows.Scan(&c.ID, &c.Type, &c.Title, &c.CreatedBy, &c.IsRequest, &c.CreatedAt, &c.UpdatedAt,
			&e.SenderID, &e.ReceiverID, &e.Preview, &e.Status, &e.RequestAt); err != nil {
			return nil, err
		}
		e.Preview = s.openPreview(ctx, contentcrypt.ScopeRequestPreview, contentcrypt.ConversationRow(c.ID), e.Preview)
		out = append(out, e)
	}
	return out, rows.Err()
}

// DirectChatAccepted answers graph-service's calls decision under
// who_can_call = accepted_chats_only: does the (unordered) pair hold a live
// 1:1 chat both agreed to? True only when the pair's direct conversation is
// not a request (created as a direct chat, accepted, or promoted by a
// follow), neither member has left (a block severs both), it is not a
// closed conversation, and its envelope, if any, is not pending, deleted
// ("ignored") or expired. A dating match's chat never counts (its calls are
// decided by the dating-match state route). Postgres only.
func (s *ConversationStore) DirectChatAccepted(ctx context.Context, userA, userB uuid.UUID) (bool, error) {
	if userA == userB || userA == uuid.Nil || userB == uuid.Nil {
		return false, nil
	}
	userA, userB = normalizePair(userA, userB)
	var accepted bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1
		    FROM chat.direct_conversation_keys k
		    JOIN chat.conversations c ON c.id = k.conversation_id
		    LEFT JOIN chat.message_requests mr ON mr.conversation_id = c.id
		    WHERE k.user_a = $1 AND k.user_b = $2
		      AND c.type = 'direct'
		      AND c.is_request = FALSE
		      AND c.closed_at IS NULL
		      -- A Pulse match's chat has its own call rules (dating-match/state).
		      AND COALESCE(c.source_app, '') <> 'dating'
		      AND (mr.id IS NULL OR mr.status NOT IN ('pending', 'ignored', 'expired'))
		      AND EXISTS (SELECT 1 FROM chat.conversation_members ma
		                  WHERE ma.conversation_id = c.id AND ma.user_id = $1 AND ma.left_at IS NULL)
		      AND EXISTS (SELECT 1 FROM chat.conversation_members mb
		                  WHERE mb.conversation_id = c.id AND mb.user_id = $2 AND mb.left_at IS NULL)
		)
	`, userA, userB).Scan(&accepted)
	return accepted, err
}
