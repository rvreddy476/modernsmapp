package com.us.android.core.chat.data

import com.us.android.core.network.TokenProvider
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
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
 *  - stops PERMANENTLY when the credential is rejected — a rejected token
 *    earns a rate limit, not a connection, and the next [start] (after
 *    re-auth) begins fresh;
 *  - applies every message frame to the DURABLE store first, so the inbox
 *    cache and unread state advance even while no thread is open
 *    (CH-LB-4.6), then re-emits for whoever is on screen;
 *  - is an ACCELERATION layer only. HTTP history repairs any gap after
 *    reconnect; nothing here is the source of truth.
 */
@Singleton
// `open` (class + the three functions a thread screen calls) solely so the
// feature-level ViewModel tests can substitute an inert session; production
// behavior is unchanged.
open class ChatSessionManager @Inject constructor(
    private val socket: ChatSocket,
    private val store: ChatStore,
    private val tokenProvider: TokenProvider,
    private val repository: ChatRepository,
) {

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

    /**
     * Starts (or restarts) the session socket. Idempotent — a second start
     * while running is a no-op, so every screen may call it defensively.
     */
    open fun start() {
        if (sessionScope?.isActive == true) return
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
                // The loop only returns on permanent credential rejection,
                // failed readiness, or cancellation. Release the root so a
                // later re-authentication start() begins fresh — an active
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
        synchronized(roomsLock) { subscribedRooms.clear() }
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
        synchronized(roomsLock) { subscribedRooms.clear() }
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

    /** Drops room interest when the thread closes. */
    open fun unsubscribeRoom(conversationId: String) {
        val wasTracked = synchronized(roomsLock) { subscribedRooms.remove(conversationId) }
        if (wasTracked && _connection.value == ConnectionState.Connected) {
            socket.send(unsubscribeFrame(json, conversationId))
        }
    }

    private suspend fun sendSubscribe(conversationId: String) {
        // Fresh entitlement per subscribe: the membership authority re-checks
        // ACTIVE membership and signs the current generation, so a member
        // removed since the last token cannot ride an old one.
        val token = when (val result = repository.subscriptionEntitlement(conversationId)) {
            is com.us.android.core.common.result.AppResult.Success -> result.data
            is com.us.android.core.common.result.AppResult.Failure -> return
        }
        if (token.isNotBlank()) socket.send(subscribeFrame(json, token))
    }

    private suspend fun runLoop(scope: CoroutineScope) {
        var attempt = 0
        while (true) {
            _connection.value = ConnectionState.Connecting
            var permanent = false
            socket.connect { tokenProvider.currentAccessToken() }.collect { event ->
                when (event) {
                    is ChatSocketEvent.Connected -> {
                        attempt = 0
                        _connection.value = ConnectionState.Connected
                        // Reconnect reconciliation (CH-LB-4.3): repair the
                        // durable inbox from HTTP, then re-subscribe every
                        // open room with a fresh entitlement. Children of the
                        // session job, so stopAndJoin awaits them.
                        scope.launch { store.syncInbox() }
                        val rooms = synchronized(roomsLock) { subscribedRooms.toList() }
                        rooms.forEach { scope.launch { sendSubscribe(it) } }
                    }
                    is ChatSocketEvent.Disconnected -> {
                        permanent = event.permanent
                        _connection.value = ConnectionState.Disconnected
                    }
                    is ChatSocketEvent.MessageReceived ->
                        // Durable FIRST: the cache row and the unread flag
                        // must survive even if nobody is collecting.
                        store.applyRealtimeMessage(event.message)
                    is ChatSocketEvent.SubscriptionRevoked ->
                        // Removed from the conversation: drop the room so no
                        // reconnect re-subscribes it.
                        synchronized(roomsLock) { subscribedRooms.remove(event.conversationId) }
                    else -> Unit
                }
                _events.emit(event)
            }
            if (permanent) {
                // Rejected credential: stop until re-auth restarts us.
                _connection.value = ConnectionState.Disconnected
                return
            }
            attempt++
            delay(reconnectDelayMillis(attempt))
            // On reconnect the store re-syncs from HTTP so anything missed
            // while offline is repaired from the durable source of truth.
            store.markReconnectPending()
        }
    }
}
