# Frontend Builder Handoff (Chat + Real-Time)

This document is the integration handoff for the current chat backend.

## 1. What Is Implemented

- `message-service` (REST API): conversations, members, messages.
- `ws-gateway` (WebSocket): real-time fanout from Redis channel `chat:<user_id>`.
- Auth for both services via JWT (HS256).
- Idempotency enforcement on:
  - create direct conversation
  - create group conversation
  - send message

## 1.1 What Is Not Implemented In Chat

- No "Send invite to My Circle" endpoint in Chat.
- No friend request lifecycle in Chat.
- No built-in "only add users from my circle" policy check in Chat.

Those are Social Graph concerns.

## 2. Local Run Instructions

From `chat-service`:

```bash
docker compose up --build
```

Health endpoints:

- Message service: `GET http://localhost:8092/v1/chat/health`
- WS gateway: `GET http://localhost:8093/health`

## 3. Base URLs

- REST base: `http://localhost:8092/v1/chat`
- WS base: `ws://localhost:8093/v1/ws/connect`

## 4. Authentication Contract

## JWT requirements

- Algorithm: `HS256`
- Secret: same `JWT_SECRET` used by services (local default in compose: `dev_secret_change_me`)
- Required user claim:
  - `sub` (preferred) or `user_id`
  - value must be a UUID string
- Optional but supported claims: `exp`, `nbf`

## Where token must be sent

- REST: `Authorization: Bearer <token>`
- WS gateway:
  - Browser clients: use query string `?access_token=<token>`
  - Non-browser clients may also use `Authorization: Bearer <token>`

## 5. REST Response Envelope

All REST responses are wrapped:

```json
{
  "data": {},
  "meta": {
    "next_cursor": "..."
  }
}
```

Error shape:

```json
{
  "error": {
    "code": "SOME_CODE",
    "message": "Human-readable message",
    "details": null
  }
}
```

## 6. Required Headers

Always send:

- `Authorization: Bearer <token>`
- `Content-Type: application/json` (for request bodies)

Required on these POST routes:

- `Idempotency-Key: <unique-key>`
  - `POST /conversations/direct`
  - `POST /conversations/group`
  - `POST /conversations/:id/messages`

## 7. REST API Contract

## 7.0 Service Ownership: Chat vs Social Graph

Group creation:

- Client calls Chat service `CreateGroup(name, initialMemberIds)` via `POST /conversations/group`.
- Chat service creates:
  - conversation row
  - creator membership (admin)
  - initial member rows

Invite My Circle (friend request):

- Friend/circle request lifecycle belongs to Social Graph service, not Chat.
- Chat currently has no friend-request/invite endpoints for circle relationships.
- Once friendship/circle membership exists, frontend can add that user to a group with `POST /conversations/:id/members`.

Common integration patterns:

- Client-orchestrated (simplest):
  - frontend fetches circle members from Social Graph
  - frontend calls Chat to create group / add members
- Event-driven (scalable):
  - Social Graph emits events like `FriendshipAccepted`
  - Chat/Notification consumers react if product needs automation or suggestions

Important current behavior:

- Chat service does not currently validate whether a target user is in caller's circle.
- If circle-only group adds are required, enforce it in:
  - BFF/API gateway orchestration, or
  - a service-to-service Social Graph validation call from Chat.

## 7.0.1 Frontend Integration Playbook

Use this sequence in UI flows:

1. Create group:
   - Call Chat `POST /conversations/group` with `title` and initial `member_ids`.
2. Invite to circle / friend request:
   - Call Social Graph service endpoints (outside Chat scope).
3. Add person to group chat after social acceptance:
   - Call Chat `POST /conversations/:id/members`.

If your product requires strict circle-only participants:

1. Fetch or verify circle membership from Social Graph before calling Chat add-member.
2. Block add-member action in frontend when relationship is not accepted.

## 7.1 Create Direct Conversation

- Method: `POST`
- Path: `/conversations/direct`
- Idempotency key: required
- Body:

```json
{
  "other_user_id": "8eaf7cb6-5a08-4db8-ad8b-56fefe4ed72d"
}
```

- Success: `200 OK`

## 7.2 Create Group Conversation

- Method: `POST`
- Path: `/conversations/group`
- Idempotency key: required
- Body:

```json
{
  "title": "Project Alpha",
  "member_ids": [
    "8eaf7cb6-5a08-4db8-ad8b-56fefe4ed72d",
    "14f0ff5b-4667-4135-83c0-c40f4638d8b9"
  ]
}
```

- Success: `201 Created`

## 7.3 List Conversations

- Method: `GET`
- Path: `/conversations?limit=20&cursor=<opaque>`
- Success: `200 OK`
- Sorted by most recent activity (`updated_at` DESC)
- Use `meta.next_cursor` for pagination

## 7.4 Get Conversation

- Method: `GET`
- Path: `/conversations/:id`
- Success: `200 OK`

## 7.5 Add Group Member

- Method: `POST`
- Path: `/conversations/:id/members`
- Body:

```json
{
  "user_id": "8eaf7cb6-5a08-4db8-ad8b-56fefe4ed72d"
}
```

- Success: `200 OK`
- Permission: caller must be group admin

## 7.6 Remove Group Member

- Method: `DELETE`
- Path: `/conversations/:id/members/:userId`
- Success: `200 OK`
- Rules:
  - self-removal allowed
  - removing others requires admin
  - last admin cannot be removed

## 7.7 Update Group Title

- Method: `PUT`
- Path: `/conversations/:id`
- Body:

```json
{
  "title": "New Group Name"
}
```

- Success: `200 OK`
- Permission: admin only

## 7.8 Send Message

- Method: `POST`
- Path: `/conversations/:id/messages`
- Idempotency key: required
- Body for text:

```json
{
  "type": "text",
  "text": "Hello"
}
```

- Body for media:

```json
{
  "type": "media",
  "media_id": "8aa53ccf-1d87-423f-a3ac-4c14f908eb75"
}
```

- Success: `200 OK`
- Response message fields use `msg_id`

## 7.9 Get Messages

- Method: `GET`
- Path: `/conversations/:id/messages?limit=30&cursor=<opaque>`
- Success: `200 OK`
- Order: newest first
- Use `meta.next_cursor` for pagination

## 7.10 Delete Message

- Method: `DELETE`
- Path: `/conversations/:id/messages/:messageId`
- Body is required:

```json
{
  "bucket": "202602",
  "ts": "2026-02-16T18:00:12.123456789Z"
}
```

- Success: `200 OK`
- Permission:
  - sender can delete
  - for group chats, group admin can delete any message

Important:

- `ts` must match backend timestamp exactly.
- Do not parse and reformat `created_at` through JS `Date` before delete.
- Store raw timestamp string returned by API/WS and reuse it.

## 8. Idempotency Behavior

If `Idempotency-Key` is missing on required routes:

- `400 MISSING_IDEMPOTENCY_KEY`

If same key is reused with different payload:

- `409 IDEMPOTENCY_KEY_CONFLICT`

If same key is still being processed:

- `409 IDEMPOTENCY_IN_PROGRESS`

If same key is retried with same payload after success:

- cached response is returned.

Frontend rule:

- Generate one unique key per user action and reuse only for retries of the exact same request payload.

## 9. WebSocket Contract

## Connect

Browser example:

```ts
const ws = new WebSocket(
  `ws://localhost:8093/v1/ws/connect?access_token=${encodeURIComponent(token)}`
);
```

## Incoming event format

Message service publishes this shape (JSON text frame):

```json
{
  "type": "message",
  "payload": {
    "conversation_id": "5f23b4a8-fdc4-4908-8eac-4be9204d310e",
    "message_id": "2a17d0b3-a4da-4de8-b408-5f4c60f4688a",
    "sender_id": "14f0ff5b-4667-4135-83c0-c40f4638d8b9",
    "type": "text",
    "text": "hello",
    "media_id": null,
    "created_at": "2026-02-16T18:00:12.123456789Z"
  }
}
```

Notes:

- WS payload uses `message_id`.
- REST payload uses `msg_id`.
- Normalize these in frontend model.

## Client send behavior

- Incoming client messages are currently ignored by gateway.
- Use REST API for all writes.

## 10. Suggested Frontend Data Normalization

For message entities keep:

- `id`: from `msg_id` or `message_id`
- `conversation_id`
- `sender_id`
- `type`
- `text`
- `media_id`
- `created_at_raw` (exact original timestamp string)
- `bucket`: derived once from raw timestamp in UTC (`YYYYMM`)

Bucket helper:

```ts
function toBucket(tsRaw: string): string {
  const iso = new Date(tsRaw).toISOString(); // UTC
  return iso.slice(0, 7).replace("-", "");   // YYYYMM
}
```

Use `created_at_raw` + `bucket` when calling delete.

## 11. Known Limitations (Current Backend)

- No read receipts
- No delivery receipts
- No typing indicators
- No reactions
- No message edit
- No pinned/archived/muted conversation features
- No dedicated user profile data in chat responses (only user IDs/roles)
- No Social Graph invite/friend request APIs in Chat (by design)
- No Chat-side enforcement that added members are in caller's circle

## 12. Local Dev Token Quick Generator (Optional)

If you need a local token quickly (Node.js):

```js
const crypto = require("crypto");
const secret = process.env.JWT_SECRET || "dev_secret_change_me";
const header = Buffer.from(JSON.stringify({ alg: "HS256", typ: "JWT" })).toString("base64url");
const payload = Buffer.from(JSON.stringify({
  sub: "14f0ff5b-4667-4135-83c0-c40f4638d8b9",
  exp: Math.floor(Date.now() / 1000) + 3600
})).toString("base64url");
const sig = crypto.createHmac("sha256", secret).update(`${header}.${payload}`).digest("base64url");
console.log(`${header}.${payload}.${sig}`);
```

## 13. Quick End-to-End Test

1. Start stack with `docker compose up --build`.
2. Create a direct conversation via REST with `Idempotency-Key`.
3. Open WS for user A and user B.
4. Send message via REST from user A.
5. Confirm user B receives WS frame.
6. Fetch messages via REST and verify same message appears.
