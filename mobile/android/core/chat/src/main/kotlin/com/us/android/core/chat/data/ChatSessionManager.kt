package com.us.android.core.chat.data

import com.us.android.core.network.TokenProvider
import com.us.android.core.network.TokenRefresher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.Json
import javax.inject.Inject
import javax.inject.Singleton

/**
 * ONE session-scoped socket for the whole authenticated app session
 * (directive §5.3/§5.4, CH-LB-4.1).
 *
 * Replaces the per-open-thread ownership: every screen that cares about
 * realtime chat collects [events] from this singleton instead of opening its
 * own connection. The manager:
 *
 *  - owns the reconnect loop (full-jitter backoff via [reconnectDelayMillis]);
 *  - mints the access token before it connects (audit H1): at cold start
 *    the session exists before its first token does, and a socket that went
 *    out without one used to end for good, leaving the user offline and
 *    their foreground pushes suppressed for the whole session;
 *  - never retries a REJECTED credential as it is — a rejected token earns
 *    a rate limit, not a connection. It waits for a different token (or one
 *    due a refresh, or a [start]) and then connects again;
 *  - applies every message frame to the DURABLE store first, so the inbox
 *    cache and unread state advance even while no thread is open
 *    (CH-LB-4.6), then re-emits for whoever is on screen;
 *  - is an ACCELERATION layer only. HTTP history repairs any gap after
 *    reconnect; nothing here is the source of truth.
 */
private const val REFUSED_CHAT_LOCKED = "chat_locked"

@Singleton
// `open` (class + the three functions a thread screen calls) solely so the
// feature-level ViewModel tests can substitute an inert session; production
// behavior is unchanged.
open class ChatSessionManager @Inject constructor(
    private val socket: ChatSocket,
    private val store: ChatStore,
    private val tokenProvider: TokenProvider,
    private val repository: ChatRepository,
    // Message ticks (2026-10-22): delivered acks. Null in tests, which keeps
    // the session as it was.
    private val deliveredAcks: DeliveredAcker? = null,
    // Hidden chats (24 Oct 2026): a hidden chat's room is subscribed with
    // its open token, never an entitlement. Null in tests: nothing is hidden.
    private val hidden: com.us.android.core.chat.hidden.HiddenTokenStore? = null,
    /**
     * Mints the access token before a connect (audit H1/M2): none exists yet
     * at cold start, and one near its end would be closed by the gateway
     * seconds after the upgrade. Null in tests: the provider's token is used
     * as it is.
     */
    private val tokenRefresher: TokenRefresher? = null,
) {

    /**
     * Tells the sender's ticks this device has [messageId] (`ts` its server
     * time, when known). Batched per conversation, at most once a second.
     */
    open fun acknowledgeDelivered(conversationId: String, messageId: String?, ts: String?) {
        deliveredAcks?.offer(conversationId, messageId, ts)
    }

    /** Connection state for screens that show an offline banner. */
    enum class ConnectionState { Disconnected, Connecting, Connected }

    /**
     * One job tree per started session. EVERYTHING this manager launches —
     * the socket loop, the reconnect [ChatStore.syncInbox] reconciliation and
     * the room subscribes — is a child of [sessionScope]'s supervisor job, so
     * [stopAndJoin] can cancel AND await the whole tree. The previous shape
     * launched reconciliation as a detached sibling; logout could complete
     * while that sibling was still writing (review finding F2-LB-1).
     */
    @Volatile
    private var sessionScope: CoroutineScope? = null
    private val json = Json { ignoreUnknownKeys = true }

    private val _events = MutableSharedFlow<ChatSocketEvent>(extraBufferCapacity = 64)

    /** Every socket event, after the durable store has applied it. */
    // `open` (like start/subscribeRoom/sendCallFrame) so call-layer tests can
    // substitute a scripted stream; production behavior unchanged.
    open val events: SharedFlow<ChatSocketEvent> = _events.asSharedFlow()

    private val _connection = MutableStateFlow(ConnectionState.Disconnected)

    // `open` (like events/sendCallFrame) so call-layer tests can script
    // connection readiness; production behavior unchanged.
    open val connection: StateFlow<ConnectionState> = _connection.asStateFlow()

    /**
     * Conversations with a live room interest (an open thread), guarded by
     * [roomsLock]. The set is the durable INTENT; the actual subscribe frame
     * is (re)sent per connection with a FRESH owner-issued entitlement —
     * tokens live five minutes, so a cached one would go stale across any
     * real disconnect.
     */
    private val roomsLock = Any()
    private val subscribedRooms = mutableSetOf<String>()

    /** Rooms whose subscribe frame went out on THIS connection — typing frames go only to these. */
    private val liveRooms = mutableSetOf<String>()

    /**
     * Ends a wait early: a [start] while the loop backs off, or waits for a
     * credential, means somebody wants chat NOW (a screen opened, the app
     * came to the foreground). Conflated — one pending wake is enough.
     */
    private val wake = Channel<Unit>(Channel.CONFLATED)

    /** Monotonic milliseconds, for how long a connection stayed up. A test seam. */
    internal var elapsedMillis: () -> Long = { System.nanoTime() / NANOS_PER_MILLI }

    /**
     * Starts (or restarts) the session socket. Idempotent — a second start
     * while running opens no second socket, so every screen may call it
     * defensively; a loop that is waiting (backoff, or for a credential)
     * connects again at once.
     */
    open fun start() {
        if (sessionScope?.isActive == true) {
            wake.trySend(Unit)
            return
        }
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        sessionScope = scope
        scope.launch {
            try {
                // AWAITED readiness barrier (F2-LB-3): an owed disk scrub is
                // repaid before the socket exists. If repayment fails the
                // store is quarantined — every write and lease is refused —
                // and the socket MUST NOT start.
                if (store.ensureReady()) runLoop(scope)
            } finally {
                // The loop only returns on failed readiness or cancellation.
                // Release the root so a later start() begins fresh — an active
                // but childless supervisor previously swallowed every
                // restart (F2-LB-1 gap 3).
                endSession(scope)
            }
        }
    }

    private fun endSession(scope: CoroutineScope) {
        if (sessionScope === scope) {
            sessionScope = null
            _connection.value = ConnectionState.Disconnected
        }
        scope.cancel()
    }

    /** Stops the socket; prefer [stopAndJoin] on logout. */
    fun stop() {
        sessionScope?.cancel()
        sessionScope = null
        synchronized(roomsLock) {
            subscribedRooms.clear()
            liveRooms.clear()
        }
        _connection.value = ConnectionState.Disconnected
    }

    /**
     * Stops the session and AWAITS every job it owns — the loop, in-flight
     * reconciliation, room subscribes. Logout must not proceed to the wipe
     * while any of them could still be running (F2-LB-1); the store's write
     * gate then refuses whatever cancellation alone could not reach.
     */
    suspend fun stopAndJoin() {
        val scope = sessionScope
        sessionScope = null
        synchronized(roomsLock) {
            subscribedRooms.clear()
            liveRooms.clear()
        }
        _connection.value = ConnectionState.Disconnected
        scope?.coroutineContext?.get(Job)?.let { job ->
            job.cancel()
            job.join()
        }
    }

    /**
     * Registers room interest for an OPEN thread and subscribes the live
     * socket (scaled room path, carried blocker item 2: the entitled
     * `conversation.subscribe` the gateway verifies against the membership
     * authority's signature and revocation markers). Failure is silent by
     * design — the personal channel still delivers everything today, so the
     * room is an acceleration, not the delivery guarantee.
     */
    open fun subscribeRoom(conversationId: String) {
        if (conversationId.isBlank()) return
        synchronized(roomsLock) { subscribedRooms.add(conversationId) }
        if (_connection.value == ConnectionState.Connected) {
            sessionScope?.launch { sendSubscribe(conversationId) }
        }
    }

    /**
     * Sends one call-signaling frame on the live session socket (calling P0).
     * The ONE socket carries chat and call signaling both — a second socket
     * would fork token refresh and double every reconnect race. Returns false
     * when there is no live socket; the call layer decides what that means.
     */
    open fun sendCallFrame(frame: String): Boolean = socket.send(frame)

    /**
     * Chat parity (CONTRACT §4): a `typing.start` / `typing.stop` frame for
     * an OPEN room. False when there is no live socket or the room was never
     * subscribed — the caller then falls back to the REST route, which
     * keeps working either way.
     */
    open fun sendTyping(conversationId: String, typing: Boolean): Boolean {
        if (_connection.value != ConnectionState.Connected) return false
        val subscribed = synchronized(roomsLock) { conversationId in liveRooms }
        if (!subscribed) return false
        return socket.send(typingFrame(json, conversationId, typing))
    }

    /** Drops room interest when the thread closes. */
    open fun unsubscribeRoom(conversationId: String) {
        val wasTracked = synchronized(roomsLock) {
            liveRooms.remove(conversationId)
            subscribedRooms.remove(conversationId)
        }
        if (wasTracked && _connection.value == ConnectionState.Connected) {
            socket.send(unsubscribeFrame(json, conversationId))
        }
    }

    private suspend fun sendSubscribe(conversationId: String) {
        // A hidden chat: nothing about it reaches the personal channel, so
        // its room is the only realtime path. The open token rides the
        // subscribe frame and the gateway fetches the entitlement itself; no
        // open token, no subscribe (the thread is not showing then anyway).
        if (hidden?.isLocked(conversationId) == true) {
            val open = hidden.open() ?: return
            if (socket.send(com.us.android.core.chat.hidden.hiddenSubscribeFrame(json, conversationId, open))) {
                synchronized(roomsLock) { if (conversationId in subscribedRooms) liveRooms.add(conversationId) }
            }
            return
        }
        // Fresh entitlement per subscribe: the membership authority re-checks
        // ACTIVE membership and signs the current generation, so a member
        // removed since the last token cannot ride an old one.
        val token = when (val result = repository.subscriptionEntitlement(conversationId)) {
            is com.us.android.core.common.result.AppResult.Success -> result.data
            is com.us.android.core.common.result.AppResult.Failure -> return
        }
        if (token.isNotBlank() && socket.send(subscribeFrame(json, token))) {
            synchronized(roomsLock) { if (conversationId in subscribedRooms) liveRooms.add(conversationId) }
        }
    }

    /** Hidden chats: what the gateway said about one room. */
    private suspend fun onRoomNotice(scope: CoroutineScope, notice: ChatSocketEvent.RoomNotice) {
        val id = notice.conversationId
        when (notice.kind) {
            RoomNoticeKind.Refused -> {
                synchronized(roomsLock) { liveRooms.remove(id) }
                if (notice.reason == REFUSED_CHAT_LOCKED) {
                    // The open token was refused: the chat is hidden and closed now.
                    hidden?.markLocked(listOf(id))
                    hidden?.drop(com.us.android.core.chat.hidden.HiddenDropReason.Server, openOnly = true)
                }
            }
            RoomNoticeKind.Expired -> {
                val wanted = synchronized(roomsLock) {
                    liveRooms.remove(id)
                    id in subscribedRooms
                }
                // Still open on screen: subscribe again (the gateway never renews by itself).
                if (wanted) scope.launch { sendSubscribe(id) }
            }
            RoomNoticeKind.ConversationHidden -> {
                hidden?.markLocked(listOf(id))
                store.purgeHiddenLocally(listOf(id))
            }
        }
    }

    private suspend fun runLoop(scope: CoroutineScope) {
        var attempt = 0
        while (true) {
            _connection.value = ConnectionState.Connecting
            val token = connectToken()
            if (token.isNullOrBlank()) {
                // No credential YET (audit H1): a cold start before the first
                // refresh, or a refresh that failed for a transient reason.
                // Not a rejection, so the normal backoff applies.
                _connection.value = ConnectionState.Disconnected
                attempt++
                pause(reconnectDelayMillis(attempt))
                continue
            }
            val outcome = connectOnce(scope, token)
            _connection.value = ConnectionState.Disconnected
            // Only a connection that STAYED up resets the backoff (audit M2):
            // one the gateway closes right after the upgrade (a token inside
            // its expiry skew) must not come back at the minimum delay forever.
            attempt = if (outcome.stable) 1 else attempt + 1
            if (outcome.rejected) {
                awaitNewCredential(rejected = token)
                attempt = 0
            } else {
                pause(reconnectDelayMillis(attempt))
            }
            // On reconnect the store re-syncs from HTTP so anything missed
            // while offline is repaired from the durable source of truth.
            store.markReconnectPending()
        }
    }

    /** How one connection ended. */
    private class ConnectOutcome(val rejected: Boolean, val stable: Boolean)

    /**
     * The token to connect with: the current one, refreshed first when the
     * session has none yet (cold start) or it is near its end — the same rule
     * every REST request follows. A failed refresh falls back to whatever is
     * current, possibly nothing.
     */
    private suspend fun connectToken(): String? {
        val current = tokenProvider.currentAccessToken()
        val refresher = tokenRefresher ?: return current
        if (!refresher.shouldRefreshBeforeRequest()) return current
        return refresher.refreshIfStale(current) ?: tokenProvider.currentAccessToken()
    }

    /**
     * After the gateway REJECTED [rejected] (401/403 on the upgrade, past the
     * authenticator's own refresh): waits until there is a different token,
     * the current one is due a refresh, or [start] asks again. Retrying the
     * same credential earns a rate limit, not a connection; stopping for good
     * left a signed-in user offline until they backgrounded the app.
     */
    private suspend fun awaitNewCredential(rejected: String) {
        while (true) {
            val current = tokenProvider.currentAccessToken()
            if (!current.isNullOrBlank() && current != rejected) return
            if (tokenRefresher?.shouldRefreshBeforeRequest() == true) return
            if (pause(CREDENTIAL_RECHECK_MILLIS)) return
        }
    }

    /**
     * Waits [millis], or less when [start] is called meanwhile. A wake left
     * over from before the wait is dropped first: whoever asked then was
     * served by the connection that has just ended. True when woken early.
     */
    private suspend fun pause(millis: Long): Boolean {
        wake.tryReceive()
        return withTimeoutOrNull(millis) { wake.receive() } != null
    }

    private suspend fun connectOnce(scope: CoroutineScope, token: String): ConnectOutcome {
        var rejected = false
        var connectedAt: Long? = null
        socket.connect { token }.collect { event ->
            when (event) {
                is ChatSocketEvent.Connected -> {
                    connectedAt = elapsedMillis()
                    _connection.value = ConnectionState.Connected
                    // Reconnect reconciliation (CH-LB-4.3): repair the
                    // durable inbox from HTTP, then re-subscribe every
                    // open room with a fresh entitlement. Children of the
                    // session job, so stopAndJoin awaits them.
                    scope.launch { store.syncInbox() }
                    // The backend is reachable right now, whatever the OS
                    // thinks of the network: send anything still queued
                    // (fix pass 2 — the worker waits for a VALIDATED
                    // network, which some reachable networks never are).
                    store.drainNow()
                    val rooms = synchronized(roomsLock) { subscribedRooms.toList() }
                    rooms.forEach { scope.launch { sendSubscribe(it) } }
                }
                is ChatSocketEvent.Disconnected -> {
                    rejected = event.permanent
                    _connection.value = ConnectionState.Disconnected
                    // A new connection must re-subscribe before typing frames flow again.
                    synchronized(roomsLock) { liveRooms.clear() }
                }
                is ChatSocketEvent.MessageReceived -> {
                    // Durable FIRST: the cache row and the unread flag
                    // must survive even if nobody is collecting.
                    store.applyRealtimeMessage(event.message)
                    // This device has it: the sender's second tick.
                    acknowledgeDelivered(
                        event.message.conversationId,
                        event.message.id,
                        event.message.ts.ifBlank { null },
                    )
                }
                is ChatSocketEvent.SubscriptionRevoked ->
                    // Removed from the conversation: drop the room so no
                    // reconnect re-subscribes it.
                    synchronized(roomsLock) {
                        subscribedRooms.remove(event.conversationId)
                        liveRooms.remove(event.conversationId)
                    }
                is ChatSocketEvent.RoomNotice -> onRoomNotice(scope, event)
                // Chat parity: durable first, like a message — the
                // cached thread must not resurrect an unsent message or
                // forget a reaction when it is next opened offline.
                is ChatSocketEvent.MessageDeleted -> store.applyDeleted(event.messageId)
                // DM consent: an expired request leaves the cached Requests.
                is ChatSocketEvent.MessageRequestRemoved -> store.removeRequest(event.conversationId)
                is ChatSocketEvent.ReactionChanged ->
                    store.applyReaction(event.messageId, event.emoji, event.userId, event.added)
                // The peer read my last message: the inbox row's "Sent"
                // becomes "Seen" without waiting for the next sync.
                is ChatSocketEvent.ReadReceipt ->
                    store.applyReadReceipt(event.conversationId, event.userId, event.messageId)
                else -> Unit
            }
            _events.emit(event)
        }
        val stable = connectedAt?.let { elapsedMillis() - it >= STABLE_CONNECTION_MILLIS } == true
        return ConnectOutcome(rejected = rejected, stable = stable)
    }

    internal companion object {
        /** A connection up this long counts as healthy: the next drop backs off from the start. */
        const val STABLE_CONNECTION_MILLIS = 10_000L

        /** How often a rejected session looks again for a usable credential. */
        const val CREDENTIAL_RECHECK_MILLIS = 30_000L

        private const val NANOS_PER_MILLI = 1_000_000L
    }
}
