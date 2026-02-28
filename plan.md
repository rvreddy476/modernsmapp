# Voice & Video Calling Implementation Plan

## Architecture Overview

WebRTC peer-to-peer calling with signaling relayed through the existing ws-gateway via Redis pub/sub. No new services needed — the ws-gateway's `readLoop` is extended to forward signaling frames to the target user's Redis channel.

```
Caller Browser ↔ ws-gateway ↔ Redis pub/sub ↔ ws-gateway ↔ Callee Browser
                     (signaling relay)
                         ↕
              Caller ←— WebRTC P2P media —→ Callee
```

STUN-only (Google public servers) for NAT traversal. No call history persistence — real-time signaling only.

---

## Phase 1: Backend — Extend ws-gateway for Bidirectional Signaling

### File: `chat-service/services/ws-gateway/internal/http/server.go`

**1a. Modify `readLoop` to process inbound client frames**

Currently `readLoop` reads and discards all client messages (line 137: `_, _, err := conn.ReadMessage()`). Change it to:

- Read the message payload instead of discarding
- Parse as JSON, extract `type` field
- For signaling types (`call_offer`, `call_answer`, `ice_candidate`, `call_end`, `call_decline`, `call_busy`), extract the `target_user_id` from the payload
- Publish the full payload (with `sender_id` injected server-side for security) to Redis channel `chat:<target_user_id>`
- Ignore/discard any other message types (preserving current behavior for non-signaling frames)

The `readLoop` signature changes to accept `rdb *redis.Client` and `userID uuid.UUID` (it already has `userID`; just needs `rdb`).

```go
// Signaling message types that get relayed
var signalingTypes = map[string]bool{
    "call_offer": true, "call_answer": true, "ice_candidate": true,
    "call_end": true, "call_decline": true, "call_busy": true,
}

func (s *Server) readLoop(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, userID uuid.UUID) {
    defer cancel()
    conn.SetReadLimit(s.opts.MaxMessageSize)
    _ = conn.SetReadDeadline(time.Now().Add(s.opts.PongWait))
    conn.SetPongHandler(func(string) error {
        return conn.SetReadDeadline(time.Now().Add(s.opts.PongWait))
    })

    for {
        _, raw, err := conn.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
                s.log.Warn("websocket read failed", "err", err, "user_id", userID)
            }
            return
        }
        if len(raw) == 0 {
            continue
        }
        // Parse and relay signaling messages
        var envelope map[string]any
        if json.Unmarshal(raw, &envelope) != nil {
            continue
        }
        msgType, _ := envelope["type"].(string)
        if !signalingTypes[msgType] {
            continue
        }
        targetStr, _ := envelope["target_user_id"].(string)
        targetID, err := uuid.Parse(targetStr)
        if err != nil || targetID == uuid.Nil {
            continue
        }
        // Inject sender_id server-side (prevent spoofing)
        envelope["sender_id"] = userID.String()
        relay, _ := json.Marshal(envelope)
        channel := fmt.Sprintf("chat:%s", targetID.String())
        s.rdb.Publish(ctx, channel, string(relay))
    }
}
```

**1b. Update `serveConnection` call**

Pass `ctx` and adjust the `readLoop` goroutine call since it now needs `ctx`:
```go
go s.readLoop(ctx, cancel, conn, userID)
```

This is a minimal, clean change — ~40 lines of new code in the gateway. No new routes, no new dependencies.

---

## Phase 2: Frontend — Call Service (`callService.ts`)

### New file: `postbook-ui/src/services/callService.ts`

A standalone service that manages WebRTC peer connections and call state. Exports:

```typescript
// Types
export type CallState = 'idle' | 'outgoing' | 'incoming' | 'connecting' | 'active';
export type CallType = 'audio' | 'video';

export interface CallInfo {
  state: CallState;
  type: CallType;
  peerId: string;         // remote user ID
  peerName: string;
  peerAvatar: string;
  startedAt?: number;
}

// Functions
export function initiateCall(contact: User, type: CallType): void;
export function acceptCall(): void;
export function declineCall(): void;
export function endCall(): void;
export function toggleMute(): boolean;          // returns new mute state
export function toggleCamera(): boolean;        // returns new camera state
export function subscribeToCallState(cb: (info: CallInfo | null) => void): () => void;
```

**Internal flow:**

1. **`initiateCall`**:
   - `navigator.mediaDevices.getUserMedia({ audio: true, video: type === 'video' })`
   - Create `RTCPeerConnection` with STUN config: `{ iceServers: [{ urls: 'stun:stun.l.google.com:19302' }] }`
   - Add local tracks to peer connection
   - Create offer via `pc.createOffer()`, set as local description
   - Send `{ type: 'call_offer', target_user_id, call_type, sdp }` through the existing WebSocket (`socket.send()`)
   - Listen for ICE candidates, send each as `{ type: 'ice_candidate', target_user_id, candidate }`
   - Set state to `outgoing`

2. **Incoming call (from WebSocket)**:
   - Extend `messageService.ts` WebSocket handler to detect `type: 'call_offer'` and dispatch to callService
   - Set state to `incoming`, store the offer SDP
   - Notify UI subscribers

3. **`acceptCall`**:
   - Get user media
   - Create `RTCPeerConnection`, add tracks
   - Set remote description from stored offer
   - Create answer, set local description
   - Send `{ type: 'call_answer', target_user_id, sdp }` via WebSocket
   - Set state to `connecting` → `active` when ICE completes

4. **`declineCall`**: Send `{ type: 'call_decline', target_user_id }`, cleanup

5. **`endCall`**: Send `{ type: 'call_end', target_user_id }`, stop all tracks, close peer connection, set state to `idle`

6. **`toggleMute` / `toggleCamera`**: Toggle `track.enabled` on the local media stream

**WebSocket integration**: Add a `sendSignaling(data: object)` function that calls `socket.send(JSON.stringify(data))` on the existing global socket in messageService. Export it from messageService.

---

## Phase 3: Frontend — Extend `messageService.ts`

### File: `postbook-ui/src/services/messageService.ts`

Small changes:

1. **Export `sendSignaling`** — wraps `socket.send(JSON.stringify(data))` so callService can send through the existing connection
2. **Add call event listeners** — new `callSignalListeners` set, similar to `reactionListeners`
3. **Extend `socket.onmessage`** — add branches for `call_offer`, `call_answer`, `ice_candidate`, `call_end`, `call_decline`, `call_busy` → dispatch to `callSignalListeners`
4. **Export `subscribeToCallSignals(cb): () => void`**

---

## Phase 4: Frontend — Call UI Components

### 4a. New file: `postbook-ui/src/components/CallOverlay.tsx`

A full-screen overlay that appears during any call state (incoming/outgoing/active). This is rendered at the app level in PostbookApp, not inside ChatWindow.

**States and UI:**

| State | UI |
|-------|-----|
| `incoming` | Ringing overlay with caller avatar/name, Accept (green) and Decline (red) buttons, ringtone |
| `outgoing` | "Calling..." with callee avatar/name, Cancel button, ringback tone |
| `connecting` | "Connecting..." spinner |
| `active` | Call controls bar — Mute, Camera toggle, End call, call duration timer |

**For video calls in `active` state:**
- Remote video fills the overlay background
- Local video in a small draggable PiP corner
- Call controls float at the bottom

**For audio calls in `active` state:**
- Contact avatar centered with subtle pulse animation
- Call duration timer
- Mute + End call controls

### 4b. Modify: `postbook-ui/src/components/ChatWindow.tsx`

Add call buttons to the chat header (between the contact info and close button):

```tsx
{/* Call buttons */}
<div className="flex items-center gap-1.5">
  <button onClick={() => initiateCall(contact, 'audio')} className="...">
    <PhoneIcon />
  </button>
  <button onClick={() => initiateCall(contact, 'video')} className="...">
    <VideoIcon />
  </button>
</div>
```

Two small icon buttons — phone icon for audio, camera icon for video. Styled consistently with the existing header design (8x8 rounded-xl buttons with slate-50 background).

### 4c. Modify: `postbook-ui/src/features/postbook/PostbookApp.tsx`

Add the `CallOverlay` component at the top level (outside the chat windows):

```tsx
<CallOverlay />  {/* Renders only when call state !== 'idle' */}
```

---

## Phase 5: Ringtone & Audio Feedback

### New files:
- `postbook-ui/public/sounds/ringtone.mp3` — incoming call ring
- `postbook-ui/public/sounds/ringback.mp3` — outgoing call waiting tone

Use the Web Audio API or simple `<audio>` element in CallOverlay to play/loop ringtone when `state === 'incoming'` and ringback when `state === 'outgoing'`. Stop on state change.

We can use royalty-free tones or generate simple tones programmatically using Web Audio API oscillators (no external files needed).

---

## File Change Summary

| File | Action | Description |
|------|--------|-------------|
| `ws-gateway/internal/http/server.go` | Modify | Extend readLoop to relay signaling frames via Redis |
| `postbook-ui/src/services/messageService.ts` | Modify | Add sendSignaling export, call signal listener set, WS dispatch |
| `postbook-ui/src/services/callService.ts` | **New** | WebRTC peer connection management, call state machine |
| `postbook-ui/src/components/CallOverlay.tsx` | **New** | Full-screen call UI (incoming/outgoing/active states) |
| `postbook-ui/src/components/ChatWindow.tsx` | Modify | Add phone + video call buttons to header |
| `postbook-ui/src/features/postbook/PostbookApp.tsx` | Modify | Mount CallOverlay at app level |

**No new backend services. No new Docker containers. No database changes.**

---

## Signaling Protocol

All messages sent over the existing WebSocket as JSON:

```jsonc
// Caller → Server → Callee
{ "type": "call_offer", "target_user_id": "uuid", "call_type": "audio|video", "sdp": "..." }

// Callee → Server → Caller
{ "type": "call_answer", "target_user_id": "uuid", "sdp": "..." }

// Both directions
{ "type": "ice_candidate", "target_user_id": "uuid", "candidate": { ... } }

// Either party
{ "type": "call_end", "target_user_id": "uuid" }
{ "type": "call_decline", "target_user_id": "uuid" }
{ "type": "call_busy", "target_user_id": "uuid" }

// Server injects "sender_id" into all relayed messages for security
```

## Implementation Order

1. Backend (Phase 1) — ~40 lines changed in ws-gateway
2. messageService.ts (Phase 3) — small additions
3. callService.ts (Phase 2) — core WebRTC logic
4. CallOverlay.tsx (Phase 4a) — UI
5. ChatWindow.tsx header buttons (Phase 4b)
6. PostbookApp.tsx mount (Phase 4c)
7. Ringtone (Phase 5) — Web Audio oscillators
