package com.us.android.core.call

import com.google.common.truth.Truth.assertThat
import com.us.android.core.call.data.CallApi
import com.us.android.core.call.data.CallDto
import com.us.android.core.call.data.CallHistoryItemDto
import com.us.android.core.call.data.CallRepository
import com.us.android.core.call.data.CreateCallRequest
import com.us.android.core.call.data.IceServerConfig
import com.us.android.core.call.data.JoinDto
import com.us.android.core.call.data.PendingInviteDto
import com.us.android.core.call.data.StatusPayload
import com.us.android.core.call.engine.CallEngine
import com.us.android.core.call.engine.CallEngineEvent
import com.us.android.core.call.engine.CallEngineFactory
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.chat.data.ChatSocket
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.ScrubRecoveryFlag
import com.us.android.core.database.ChatConversationEntity
import com.us.android.core.database.ChatDao
import com.us.android.core.database.ChatMessageEntity
import com.us.android.core.database.ChatPendingSendEntity
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import org.webrtc.EglBase
import org.webrtc.VideoTrack
import retrofit2.HttpException
import retrofit2.Response

/**
 * The call state machine against a STATEFUL backend model that mirrors the
 * corrected call-service semantics (pinned separately by the live-PostgreSQL
 * lifecycle suite): the caller's own join does NOT activate; a join after
 * accept does; leave/decline/end terminate a direct call for both peers.
 * The session fake models real socket readiness — Disconnected until
 * started — and scriptable send failures, because CALL-LB-2's entire finding
 * was a fake that always said yes.
 */
class CallSessionManagerTest {

    private val peer = "bbbbbbbb-0000-0000-0000-000000000002"

    // ── the backend model ───────────────────────────────────────────────

    private class ModelSession(
        var state: String,
        val inviteId: String,
        var inviteStatus: String,
    )

    /**
     * CallApi as the FIXED backend behaves. Join activates only when the
     * invite was accepted first (the callee's join); the caller's early join
     * leaves the call ringing. Leave/decline/end all terminate.
     */
    private class ModelCallApi : CallApi {
        val sessions = linkedMapOf<String, ModelSession>()
        val journal = mutableListOf<String>()
        var createGate: CompletableDeferred<Unit>? = null
        var createCalls = 0
        var nextCallId = 1
        var failCreateWith: Exception? = null
        var failJoinWith: Exception? = null

        fun liveCallCount(): Int = sessions.values.count { it.state != "ended" }

        fun seedRinging(callId: String, inviteId: String, video: Boolean): ModelSession {
            val session = ModelSession(state = "ringing", inviteId = inviteId, inviteStatus = "pending")
            sessions[callId] = session
            sessions[callId]!!.let { if (video) Unit }
            return session
        }

        override suspend fun createCall(body: CreateCallRequest): ApiEnvelope<CallDto> {
            createGate?.await()
            createCalls++
            journal += "rest:create"
            failCreateWith?.let { throw it }
            val id = "cccccccc-0000-0000-0000-00000000000$nextCallId"
            nextCallId++
            sessions[id] = ModelSession(state = "ringing", inviteId = "$id-invite", inviteStatus = "pending")
            return ApiEnvelope(data = CallDto(id = id, state = "ringing"))
        }

        override suspend fun call(callId: String): ApiEnvelope<CallDto> =
            ApiEnvelope(data = CallDto(id = callId, state = sessions[callId]?.state ?: "ended"))

        override suspend fun pendingInvites(): ApiEnvelope<List<PendingInviteDto>> =
            ApiEnvelope(
                data = sessions.filterValues { it.inviteStatus == "pending" && it.state != "ended" }
                    .map { (id, s) ->
                        PendingInviteDto(
                            inviteId = s.inviteId,
                            callId = id,
                            inviterUserId = PEER_ID,
                            callType = "direct_audio",
                            audioOnly = true,
                            callState = s.state,
                        )
                    },
            )

        override suspend fun join(callId: String): ApiEnvelope<JoinDto> {
            journal += "rest:join"
            failJoinWith?.let { throw it }
            val session = sessions[callId]
            // The corrected backend: activation is the ANSWER transition —
            // only a join that follows the invite's accept flips the state.
            if (session != null && session.inviteStatus == "accepted" && session.state == "ringing") {
                session.state = "active"
            }
            return ApiEnvelope(data = JoinDto(callId = callId))
        }

        override suspend fun acceptInvite(callId: String, inviteId: String): ApiEnvelope<StatusPayload> {
            journal += "rest:accept"
            sessions[callId]?.inviteStatus = "accepted"
            return ApiEnvelope(data = StatusPayload("ok"))
        }

        override suspend fun declineInvite(callId: String, inviteId: String): ApiEnvelope<StatusPayload> {
            journal += "rest:decline"
            sessions[callId]?.let {
                it.inviteStatus = "declined"
                it.state = "ended"
            }
            return ApiEnvelope(data = StatusPayload("ok"))
        }

        override suspend fun leave(callId: String): ApiEnvelope<StatusPayload> {
            journal += "rest:leave"
            sessions[callId]?.state = "ended"
            return ApiEnvelope(data = StatusPayload("ok"))
        }

        override suspend fun end(callId: String): ApiEnvelope<StatusPayload> {
            journal += "rest:end"
            sessions[callId]?.state = "ended"
            return ApiEnvelope(data = StatusPayload("ok"))
        }

        override suspend fun history(): ApiEnvelope<List<CallHistoryItemDto>> =
            ApiEnvelope(data = emptyList())

        companion object {
            const val PEER_ID = "bbbbbbbb-0000-0000-0000-000000000002"
        }
    }

    private class FakeEngine : CallEngine {
        private val _events = MutableSharedFlow<CallEngineEvent>(extraBufferCapacity = 16)
        override val events: SharedFlow<CallEngineEvent> = _events.asSharedFlow()
        var started = false
        var released = false
        var failCompleteAnswer = false
        val remoteIce = mutableListOf<Triple<String, String, Int>>()
        var answered: String? = null
        var completedAnswer: String? = null

        fun emit(event: CallEngineEvent) = check(_events.tryEmit(event))

        override fun start(video: Boolean, iceServers: List<IceServerConfig>) {
            started = true
        }
        override suspend fun createOffer(): String = "offer-sdp"
        override suspend fun acceptOffer(remoteSdp: String): String {
            answered = remoteSdp
            return "answer-sdp"
        }
        override suspend fun completeWithAnswer(remoteSdp: String) {
            if (failCompleteAnswer) error("answer cannot be applied")
            completedAnswer = remoteSdp
        }
        override fun addRemoteIceCandidate(candidate: String, sdpMid: String, sdpMLineIndex: Int) {
            remoteIce += Triple(candidate, sdpMid, sdpMLineIndex)
        }
        override fun setMuted(muted: Boolean) = Unit
        override fun setVideoEnabled(enabled: Boolean) = Unit
        override fun switchCamera() = Unit
        override fun localVideoTrack(): VideoTrack? = null
        override fun remoteVideoTrack(): VideoTrack? = null
        override fun eglContext(): EglBase.Context? = null
        override fun release() {
            released = true
        }
    }

    /**
     * Session with REAL readiness semantics: Disconnected until start() is
     * called; connects only when [autoConnect]. Send results are scriptable
     * per frame type.
     */
    private class FakeSession(private val journal: MutableList<String>) : ChatSessionManager(
        socket = ChatSocket(OkHttpClient(), "ws://unused"),
        store = minimalStore(),
        tokenProvider = object : com.us.android.core.network.TokenProvider {
            override fun currentAccessToken(): String? = "token"
        },
        repository = ChatRepository(unusedChatApiProxy(), ErrorMapper(Json { ignoreUnknownKeys = true })),
    ) {
        val inbound = MutableSharedFlow<ChatSocketEvent>(extraBufferCapacity = 16)
        val sent = mutableListOf<String>()
        var autoConnect = true
        var startCalls = 0
        var failSendsOfType: String? = null
        private val connectionState = MutableStateFlow(ConnectionState.Disconnected)

        override val events: SharedFlow<ChatSocketEvent> = inbound.asSharedFlow()
        override val connection: StateFlow<ConnectionState> = connectionState

        override fun start() {
            startCalls++
            journal += "socket:start"
            if (autoConnect) connectionState.value = ConnectionState.Connected
        }

        override fun subscribeRoom(conversationId: String) = Unit
        override fun unsubscribeRoom(conversationId: String) = Unit

        override fun sendCallFrame(frame: String): Boolean {
            val type = Regex("\"type\":\"([^\"]+)\"").find(frame)?.groupValues?.get(1) ?: "?"
            if (connectionState.value != ConnectionState.Connected) {
                journal += "ws:DROPPED:$type"
                return false
            }
            if (type == failSendsOfType) {
                journal += "ws:FAILED:$type"
                return false
            }
            journal += "ws:$type"
            sent += frame
            return true
        }
    }

    private class Recorder : CallNotifier, CallAudioController {
        var incomingShown = 0
        var ongoingStarted = 0
        var ongoingStopped = 0
        override fun showIncoming(callId: String, callerId: String, video: Boolean) {
            incomingShown++
        }
        override fun clearIncoming() = Unit
        override fun startOngoing(peerId: String, video: Boolean) {
            ongoingStarted++
        }
        override fun stopOngoing() {
            ongoingStopped++
        }
        override fun onCallStarted(video: Boolean) = Unit
        override fun setSpeaker(on: Boolean) = Unit
        override fun onCallEnded() = Unit
    }

    private class Harness(
        scope: CoroutineScope,
        val api: ModelCallApi = ModelCallApi(),
        ringTimeoutMillis: Long = 60_000L,
        signalingTimeoutMillis: Long = 60_000L,
        reconcileIntervalMillis: Long = 600_000L,
        iceRetryDelayMillis: Long = 500L,
    ) {
        val session = FakeSession(api.journal)
        val engine = FakeEngine()
        val recorder = Recorder()
        val manager = CallSessionManager(
            repository = CallRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true })),
            session = session,
            engineFactory = CallEngineFactory { engine },
            notifier = recorder,
            audio = recorder,
            scope = scope,
            ringTimeoutMillis = ringTimeoutMillis,
            signalingReadyTimeoutMillis = signalingTimeoutMillis,
            reconcileIntervalMillis = reconcileIntervalMillis,
            iceRetryDelayMillis = iceRetryDelayMillis,
        )

        suspend fun signal(raw: String) {
            val frame = Json.parseToJsonElement(raw).jsonObject
            val type = frame["type"]!!.toString().trim('"')
            session.inbound.emit(ChatSocketEvent.CallSignal(type, frame))
        }
    }

    private fun TestScope.harness(
        api: ModelCallApi = ModelCallApi(),
        ringTimeoutMillis: Long = 60_000L,
        signalingTimeoutMillis: Long = 60_000L,
        reconcileIntervalMillis: Long = 600_000L,
        iceRetryDelayMillis: Long = 500L,
    ) = Harness(
        backgroundScope,
        api,
        ringTimeoutMillis,
        signalingTimeoutMillis,
        reconcileIntervalMillis,
        iceRetryDelayMillis,
    )

    private fun forbidden(): HttpException = HttpException(
        Response.error<Any>(
            403,
            """{"error":{"code":"CALL_NOT_ALLOWED","message":"Calling this user is not permitted"}}"""
                .toResponseBody("application/json".toMediaType()),
        ),
    )

    // ── CALL-LB-1: create/join/ring against the modeled backend ─────────

    @Test
    fun `the caller order create-join-ring leaves the call discoverable as ringing`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val api = ModelCallApi()
        val caller = harness(api)

        assertThat(caller.manager.startOutgoing(peer, video = false, conversationId = null)).isNull()

        // The single ordered trace the review demanded: socket readiness,
        // REST create, the caller's OWN join, and only then the ring frame.
        assertThat(api.journal).containsExactly(
            "socket:start",
            "rest:create",
            "rest:join",
            "ws:call_ring",
        ).inOrder()
        // The caller's join did NOT activate — the callee still sees ringing.
        val callId = (caller.manager.state.value as CallState.Outgoing).callId
        assertThat(api.sessions[callId]!!.state).isEqualTo("ringing")

        // A SECOND manager — the callee — discovers and rings from it.
        val callee = harness(api)
        callee.manager.refreshIncoming()
        runCurrent()
        assertThat(callee.manager.state.value).isInstanceOf(CallState.Incoming::class.java)
        assertThat(callee.recorder.incomingShown).isEqualTo(1)

        // And the callee's accept-then-join is what activates.
        assertThat(callee.manager.acceptIncoming()).isNull()
        assertThat(api.sessions[callId]!!.state).isEqualTo("active")
    }

    // ── CALL-LB-2: readiness + truthful sends ───────────────────────────

    @Test
    fun `no connectivity refuses the start truthfully and never touches the server`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness(signalingTimeoutMillis = 100L)
        h.session.autoConnect = false

        val refusal = h.manager.startOutgoing(peer, video = false, conversationId = null)

        assertThat(refusal).isEqualTo("No connection. Try again.")
        assertThat(h.api.createCalls).isEqualTo(0)
        assertThat(h.manager.state.value).isEqualTo(CallState.Idle)
    }

    @Test
    fun `cold-start accept starts the socket and accepts only after readiness`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val api = ModelCallApi()
        api.seedRinging("cccccccc-0000-0000-0000-000000000009", "inv-9", video = false)
        val h = harness(api)

        // The push path: no chat screen ever ran, the socket never started.
        h.manager.refreshIncoming()
        runCurrent()
        assertThat(h.manager.state.value).isInstanceOf(CallState.Incoming::class.java)
        assertThat(h.session.startCalls).isEqualTo(0)

        assertThat(h.manager.acceptIncoming()).isNull()

        // Ordered: the socket became ready BEFORE the REST accept, and the
        // accept frame followed the REST accept.
        val trace = api.journal.filter { it.startsWith("socket:") || it == "rest:accept" || it == "ws:call_accept" }
        assertThat(trace).containsExactly("socket:start", "rest:accept", "ws:call_accept").inOrder()
    }

    @Test
    fun `cold-start accept with no connectivity keeps ringing and touches nothing`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val api = ModelCallApi()
        api.seedRinging("cccccccc-0000-0000-0000-000000000009", "inv-9", video = false)
        val h = harness(api, signalingTimeoutMillis = 100L)
        h.session.autoConnect = false
        h.manager.refreshIncoming()
        runCurrent()

        val refusal = h.manager.acceptIncoming()

        // The pre-fix behavior REST-accepted and then silently lost the
        // frame, hanging both sides. Now: nothing server-visible happened
        // and the user can retry.
        assertThat(refusal).isEqualTo("No connection. Try again.")
        assertThat(api.journal).doesNotContain("rest:accept")
        assertThat(h.manager.state.value).isInstanceOf(CallState.Incoming::class.java)
    }

    @Test
    fun `an accept frame that fails after the REST accept leaves instead of hanging`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val api = ModelCallApi()
        api.seedRinging("cccccccc-0000-0000-0000-000000000009", "inv-9", video = false)
        val h = harness(api)
        h.session.failSendsOfType = "call_accept"
        h.manager.refreshIncoming()
        runCurrent()

        val refusal = h.manager.acceptIncoming()

        assertThat(refusal).isEqualTo("No connection. Try again.")
        // Truthful convergence: the REST accept happened, so the call is
        // LEFT server-side rather than abandoned active.
        assertThat(api.journal).containsAtLeast("rest:accept", "rest:leave").inOrder()
        assertThat((h.manager.state.value as CallState.Ended).reason).isEqualTo(CallEndReason.Failed)
    }

    @Test
    fun `a failed callee join leaves the call instead of answering with an empty grant`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val api = ModelCallApi()
        api.seedRinging("cccccccc-0000-0000-0000-000000000009", "inv-9", video = false)
        val h = harness(api)
        h.manager.refreshIncoming()
        runCurrent()
        api.failJoinWith = java.io.IOException("join grant unavailable")

        val refusal = h.manager.acceptIncoming()

        assertThat(refusal).isEqualTo("The call could not be answered.")
        // Server truth: accepted, join FAILED, then LEFT — the direct call
        // ends for both peers instead of limping on with an empty grant.
        assertThat(api.journal).containsAtLeast("rest:accept", "rest:join", "rest:leave").inOrder()
        assertThat(api.journal.none { it == "ws:call_accept" }).isTrue()
        assertThat(h.engine.started).isFalse()
        assertThat(api.liveCallCount()).isEqualTo(0)
        assertThat((h.manager.state.value as CallState.Ended).reason).isEqualTo(CallEndReason.Failed)
    }

    @Test
    fun `a ring frame that fails ends the just-created call server-side`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness()
        h.session.failSendsOfType = "call_ring"

        val refusal = h.manager.startOutgoing(peer, video = false, conversationId = null)

        assertThat(refusal).isEqualTo("No connection. Try again.")
        assertThat(h.api.journal).containsAtLeast("rest:create", "rest:end").inOrder()
        assertThat(h.api.liveCallCount()).isEqualTo(0)
        assertThat(h.manager.state.value).isEqualTo(CallState.Idle)
    }

    @Test
    fun `a failed caller join ends the call server-side instead of ringing`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness()
        h.api.failJoinWith = java.io.IOException("gateway down")

        val refusal = h.manager.startOutgoing(peer, video = false, conversationId = null)

        assertThat(refusal).isEqualTo("The call couldn't be started. Try again.")
        // Server truth: created, join failed, then ENDED — never rung.
        assertThat(h.api.journal).containsAtLeast("rest:create", "rest:join", "rest:end").inOrder()
        assertThat(h.api.journal.none { it.startsWith("ws:call_ring") }).isTrue()
        assertThat(h.api.liveCallCount()).isEqualTo(0)
        assertThat(h.manager.state.value).isEqualTo(CallState.Idle)
        assertThat(h.engine.started).isFalse()
    }

    @Test
    fun `hanging up with a dead socket still ends the call server-side`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val api = ModelCallApi()
        val h = harness(api)
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val callId = (h.manager.state.value as CallState.Outgoing).callId
        h.signal("""{"type":"call_accept","call_id":"$callId","sender_id":"$peer"}""")
        runCurrent()
        h.engine.emit(CallEngineEvent.Connected)
        runCurrent()

        // The socket dies; the terminal frame cannot be delivered. The REST
        // mutation must not be gated on readiness and stays authoritative.
        h.session.failSendsOfType = "call_end"
        h.manager.hangUp()

        assertThat(api.journal).contains("rest:leave")
        assertThat(api.sessions[callId]!!.state).isEqualTo("ended")
        assertThat(api.liveCallCount()).isEqualTo(0)
        assertThat((h.manager.state.value as CallState.Ended).reason).isEqualTo(CallEndReason.HungUp)
    }

    @Test
    fun `a peer converges to ended by reconciliation when the terminal frame is lost`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        // Bounded interval NAMED here: the remote side must converge within
        // one reconcile tick (1s in this test) of the server call ending.
        val api = ModelCallApi()
        val h = harness(api, reconcileIntervalMillis = 1_000L)
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val callId = (h.manager.state.value as CallState.Outgoing).callId
        h.signal("""{"type":"call_accept","call_id":"$callId","sender_id":"$peer"}""")
        runCurrent()
        h.engine.emit(CallEngineEvent.Connected)
        runCurrent()
        assertThat(h.manager.state.value).isInstanceOf(CallState.Active::class.java)

        // The PEER ends via REST but its call_end frame never arrives
        // (socket outage) — only the server knows.
        api.sessions[callId]!!.state = "ended"

        advanceTimeBy(1_100L)
        runCurrent()

        assertThat((h.manager.state.value as CallState.Ended).reason)
            .isEqualTo(CallEndReason.RemoteEnded)
        assertThat(h.engine.released).isTrue()
    }

    @Test
    fun `a busy frame that fails declines the second call server-side`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val api = ModelCallApi()
        val h = harness(api)
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val currentCallId = (h.manager.state.value as CallState.Outgoing).callId
        val otherCaller = "eeeeeeee-0000-0000-0000-000000000005"
        val otherCall = "ffffffff-0000-0000-0000-000000000006"
        api.seedRinging(otherCall, "inv-other", video = false)
        h.session.failSendsOfType = "call_busy"

        h.signal(
            """{"type":"call_ring","call_id":"$otherCall","sender_id":"$otherCaller","video":false}""",
        )
        runCurrent()

        // The frame was lost, so the SECOND call converges over REST —
        // the current call is untouched.
        assertThat(api.journal).contains("ws:FAILED:call_busy")
        assertThat(api.journal).contains("rest:decline")
        assertThat(api.sessions[otherCall]!!.state).isEqualTo("ended")
        assertThat((h.manager.state.value as CallState.Outgoing).callId).isEqualTo(currentCallId)
        assertThat(api.sessions[currentCallId]!!.state).isNotEqualTo("ended")
    }

    @Test
    fun `a failed local ice send is retried through the recovered socket`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness(iceRetryDelayMillis = 100L)
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val callId = (h.manager.state.value as CallState.Outgoing).callId
        h.signal("""{"type":"call_accept","call_id":"$callId","sender_id":"$peer"}""")
        runCurrent()

        h.session.failSendsOfType = "ice_candidate"
        h.engine.emit(CallEngineEvent.LocalIceCandidate("cand-lost", "0", 0))
        runCurrent()
        assertThat(h.session.sent.none { it.contains("cand-lost") }).isTrue()

        // The socket recovers; the queued candidate must be DELIVERED, not
        // discarded, and the call stays up.
        h.session.failSendsOfType = null
        advanceTimeBy(250L)
        runCurrent()

        assertThat(h.session.sent.any { it.contains("cand-lost") }).isTrue()
        assertThat(h.manager.state.value).isInstanceOf(CallState.Connecting::class.java)
        assertThat(h.api.liveCallCount()).isEqualTo(1)
    }

    @Test
    fun `local ice that can never be delivered fails the call truthfully`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness(iceRetryDelayMillis = 100L)
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val callId = (h.manager.state.value as CallState.Outgoing).callId
        h.signal("""{"type":"call_accept","call_id":"$callId","sender_id":"$peer"}""")
        runCurrent()

        h.session.failSendsOfType = "ice_candidate"
        h.engine.emit(CallEngineEvent.LocalIceCandidate("cand-doomed", "0", 0))
        runCurrent()

        advanceTimeBy(2_000L) // 10 bounded retries at 100ms, then the verdict
        runCurrent()

        // Never "connecting" forever on a dead signaling path: the call is
        // left server-side and ends locally as Failed.
        assertThat(h.api.journal).contains("rest:leave")
        assertThat(h.api.liveCallCount()).isEqualTo(0)
        assertThat((h.manager.state.value as CallState.Ended).reason).isEqualTo(CallEndReason.Failed)
    }

    @Test
    fun `an answer that cannot be applied ends the server call before ending locally`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness()
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val callId = (h.manager.state.value as CallState.Outgoing).callId
        h.signal("""{"type":"call_accept","call_id":"$callId","sender_id":"$peer"}""")
        runCurrent()
        h.engine.failCompleteAnswer = true

        h.signal("""{"type":"call_answer","call_id":"$callId","sender_id":"$peer","sdp":"bad"}""")
        runCurrent()

        assertThat(h.api.journal).contains("rest:end")
        assertThat(h.api.liveCallCount()).isEqualTo(0)
        assertThat((h.manager.state.value as CallState.Ended).reason).isEqualTo(CallEndReason.Failed)
    }

    // ── CALL-LB-3: terminal convergence + subsequent eligibility ────────

    @Test
    fun `a remote end converges the server and both peers can call again`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val api = ModelCallApi()
        val h = harness(api)
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val callId = (h.manager.state.value as CallState.Outgoing).callId
        api.sessions[callId]!!.inviteStatus = "accepted"
        h.signal("""{"type":"call_accept","call_id":"$callId","sender_id":"$peer"}""")
        runCurrent()
        h.engine.emit(CallEngineEvent.Connected)
        runCurrent()

        h.signal("""{"type":"call_end","call_id":"$callId","sender_id":"$peer"}""")
        runCurrent()

        assertThat((h.manager.state.value as CallState.Ended).reason)
            .isEqualTo(CallEndReason.RemoteEnded)
        // The convergence leave reached the server; nothing is live.
        assertThat(api.journal).contains("rest:leave")
        assertThat(api.liveCallCount()).isEqualTo(0)

        // SUBSEQUENT ELIGIBILITY: the same device starts a fresh call.
        h.manager.reset()
        assertThat(h.manager.startOutgoing(peer, video = false, conversationId = null)).isNull()
        assertThat(api.liveCallCount()).isEqualTo(1)
    }

    @Test
    fun `hanging up an active call leaves and frees the device for the next call`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val api = ModelCallApi()
        val h = harness(api)
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val callId = (h.manager.state.value as CallState.Outgoing).callId
        h.signal("""{"type":"call_accept","call_id":"$callId","sender_id":"$peer"}""")
        runCurrent()
        h.engine.emit(CallEngineEvent.Connected)
        runCurrent()

        h.manager.hangUp()

        assertThat(api.sessions[callId]!!.state).isEqualTo("ended")
        h.manager.reset()
        assertThat(h.manager.startOutgoing(peer, video = false, conversationId = null)).isNull()
    }

    // ── CALL-LB-5: concurrent starts ────────────────────────────────────

    @Test
    fun `simultaneous starts produce exactly one create and one truthful refusal`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val api = ModelCallApi()
        // Hold the winner INSIDE its network call so the loser's attempt
        // arrives while the reservation is held — the exact interleaving.
        api.createGate = CompletableDeferred()
        val h = harness(api)

        val first = async { h.manager.startOutgoing(peer, video = false, conversationId = null) }
        runCurrent()
        val second = async { h.manager.startOutgoing(peer, video = true, conversationId = null) }
        runCurrent()

        // The loser is refused by the RESERVATION, before any server call.
        assertThat(second.await()).isEqualTo("You're already in a call.")

        api.createGate!!.complete(Unit)
        assertThat(first.await()).isNull()
        assertThat(api.createCalls).isEqualTo(1)
        assertThat(api.liveCallCount()).isEqualTo(1)
    }

    // ── CALL-LB-6: cold-start video permission gate ─────────────────────

    @Test
    fun `a cold-start video invite cannot be accepted without the camera`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val api = ModelCallApi()
        val callId = "cccccccc-0000-0000-0000-000000000009"
        api.seedRinging(callId, "inv-9", video = true)
        // pendingInvites reports it as video.
        val videoApi = object : CallApi by api {
            override suspend fun pendingInvites(): ApiEnvelope<List<PendingInviteDto>> =
                ApiEnvelope(
                    data = listOf(
                        PendingInviteDto(
                            inviteId = "inv-9",
                            callId = callId,
                            inviterUserId = ModelCallApi.PEER_ID,
                            callType = "direct_video",
                            audioOnly = false,
                            callState = "ringing",
                        ),
                    ),
                )
        }
        val h = Harness(backgroundScope, api)
        val manager = CallSessionManager(
            repository = CallRepository(videoApi, ErrorMapper(Json { ignoreUnknownKeys = true })),
            session = h.session,
            engineFactory = CallEngineFactory { h.engine },
            notifier = h.recorder,
            audio = h.recorder,
            scope = backgroundScope,
        )
        manager.refreshIncoming()
        runCurrent()
        assertThat((manager.state.value as CallState.Incoming).video).isTrue()

        // DENIED camera: the accept is refused BEFORE any server call and
        // the invite keeps ringing for a retry.
        val refused = manager.acceptIncoming(cameraGranted = false)
        assertThat(refused).isEqualTo("Video calls need camera access.")
        assertThat(api.journal).doesNotContain("rest:accept")
        assertThat(manager.state.value).isInstanceOf(CallState.Incoming::class.java)
        assertThat(h.engine.started).isFalse()

        // GRANTED camera: the same invite accepts normally.
        assertThat(manager.acceptIncoming(cameraGranted = true)).isNull()
        assertThat(api.journal).contains("rest:accept")
        assertThat(h.engine.started).isTrue()
    }

    @Test
    fun `an audio invite ignores the camera grant`() = runTest(UnconfinedTestDispatcher()) {
        val api = ModelCallApi()
        api.seedRinging("cccccccc-0000-0000-0000-000000000009", "inv-9", video = false)
        val h = harness(api)
        h.manager.refreshIncoming()
        runCurrent()

        assertThat(h.manager.acceptIncoming(cameraGranted = false)).isNull()
        assertThat(api.journal).contains("rest:accept")
    }

    // ── carried protections (adapted to the model) ──────────────────────

    @Test
    fun `a policy refusal surfaces one generic line and stays idle`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness()
        h.api.failCreateWith = forbidden()

        val refusal = h.manager.startOutgoing(peer, video = false, conversationId = null)

        assertThat(refusal).isEqualTo("This person can't be called right now.")
        assertThat(h.manager.state.value).isEqualTo(CallState.Idle)
    }

    @Test
    fun `an unanswered ring times out, ends server-side, and reports no answer`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness(ringTimeoutMillis = 1_000L)
        h.manager.startOutgoing(peer, video = false, conversationId = null)

        advanceTimeBy(1_500L)
        runCurrent()

        assertThat(h.api.journal).contains("rest:end")
        assertThat(h.api.liveCallCount()).isEqualTo(0)
        assertThat((h.manager.state.value as CallState.Ended).reason).isEqualTo(CallEndReason.NoAnswer)
        assertThat(h.engine.released).isTrue()
    }

    @Test
    fun `an unconfirmed ring frame rings nothing`() = runTest(UnconfinedTestDispatcher()) {
        val h = harness() // model has NO pending invites

        h.signal(
            """{"type":"call_ring","call_id":"cccccccc-0000-0000-0000-000000000009",""" +
                """"sender_id":"$peer","video":false}""",
        )
        runCurrent()

        assertThat(h.manager.state.value).isEqualTo(CallState.Idle)
        assertThat(h.recorder.incomingShown).isEqualTo(0)
    }

    @Test
    fun `a second ring while busy answers busy without touching the current call`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness()
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val callId = (h.manager.state.value as CallState.Outgoing).callId
        val otherCaller = "eeeeeeee-0000-0000-0000-000000000005"
        val otherCall = "ffffffff-0000-0000-0000-000000000006"

        h.signal(
            """{"type":"call_ring","call_id":"$otherCall","sender_id":"$otherCaller","video":false}""",
        )
        runCurrent()

        assertThat(h.api.journal).contains("ws:call_busy")
        assertThat((h.manager.state.value as CallState.Outgoing).callId).isEqualTo(callId)
    }

    @Test
    fun `signals from the wrong peer or wrong call are ignored`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness()
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val callId = (h.manager.state.value as CallState.Outgoing).callId
        val stranger = "eeeeeeee-0000-0000-0000-000000000005"

        h.signal("""{"type":"call_end","call_id":"$callId","sender_id":"$stranger"}""")
        h.signal(
            """{"type":"call_end","call_id":"ffffffff-0000-0000-0000-000000000006",""" +
                """"sender_id":"$peer"}""",
        )
        runCurrent()

        assertThat(h.manager.state.value).isInstanceOf(CallState.Outgoing::class.java)
    }

    @Test
    fun `local ice is held until the callee accepted, then flushed in order`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness()
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val callId = (h.manager.state.value as CallState.Outgoing).callId

        h.engine.emit(CallEngineEvent.LocalIceCandidate("cand-1", "0", 0))
        h.engine.emit(CallEngineEvent.LocalIceCandidate("cand-2", "0", 1))
        runCurrent()
        assertThat(h.session.sent.none { it.contains("ice_candidate") }).isTrue()

        h.signal("""{"type":"call_accept","call_id":"$callId","sender_id":"$peer"}""")
        runCurrent()

        val ice = h.session.sent.filter { it.contains("ice_candidate") }
        assertThat(ice).hasSize(2)
        assertThat(ice[0]).contains("cand-1")
        assertThat(ice[1]).contains("cand-2")
    }

    @Test
    fun `remote ice arriving before the answer is buffered, then applied`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness()
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val callId = (h.manager.state.value as CallState.Outgoing).callId
        h.signal("""{"type":"call_accept","call_id":"$callId","sender_id":"$peer"}""")
        runCurrent()

        h.signal(
            """{"type":"ice_candidate","call_id":"$callId","sender_id":"$peer",""" +
                """"candidate":"early","sdp_mid":"0","sdp_mline_index":0}""",
        )
        runCurrent()
        assertThat(h.engine.remoteIce).isEmpty()

        h.signal("""{"type":"call_answer","call_id":"$callId","sender_id":"$peer","sdp":"remote"}""")
        runCurrent()

        assertThat(h.engine.remoteIce).containsExactly(Triple("early", "0", 0))
    }

    @Test
    fun `media failure ends the call as failed and leaves server-side`() = runTest(
        UnconfinedTestDispatcher(),
    ) {
        val h = harness()
        h.manager.startOutgoing(peer, video = false, conversationId = null)
        val callId = (h.manager.state.value as CallState.Outgoing).callId
        h.signal("""{"type":"call_accept","call_id":"$callId","sender_id":"$peer"}""")
        runCurrent()
        h.engine.emit(CallEngineEvent.Connected)
        runCurrent()

        h.engine.emit(CallEngineEvent.Failed)
        runCurrent()

        assertThat((h.manager.state.value as CallState.Ended).reason).isEqualTo(CallEndReason.Failed)
        assertThat(h.api.journal).contains("rest:leave")
    }
}

// ── shared minimal chat fakes ─────────────────────────────────────────────

private class MinimalChatDao : ChatDao {
    override suspend fun upsertConversations(rows: List<ChatConversationEntity>) = Unit
    override fun conversations(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
    override fun requests(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
    override fun unreadConversationCount(): Flow<Int> = MutableStateFlow(0)
    override suspend fun clearUnread(conversationId: String) = Unit
    override suspend fun markUnread(conversationId: String, preview: String, at: String) = Unit
    override suspend fun updateSettingsFlags(conversationId: String, pinned: Boolean, muted: Boolean) = Unit
    override suspend fun rawCheckpoint(query: androidx.sqlite.db.SupportSQLiteQuery): Int = 0
    override suspend fun deleteMessage(messageId: String) = Unit
    override suspend fun deleteConversation(conversationId: String) = Unit
    override suspend fun clearConversations() = Unit
    override suspend fun insertMessages(rows: List<ChatMessageEntity>) = Unit
    override suspend fun recentMessages(conversationId: String, limit: Int) = emptyList<ChatMessageEntity>()
    override suspend fun clearMessages() = Unit
    override suspend fun enqueueSend(row: ChatPendingSendEntity) = Unit
    override suspend fun pendingSends(): List<ChatPendingSendEntity> = emptyList()
    override fun pendingSendsFor(conversationId: String): Flow<List<ChatPendingSendEntity>> =
        MutableStateFlow(emptyList())
    override suspend fun recordAttempt(key: String) = Unit
    override suspend fun markFailed(key: String) = Unit
    override suspend fun retry(key: String) = Unit
    override suspend fun completeSend(key: String) = Unit
    override suspend fun clearPendingSends() = Unit
}

private fun minimalStore(): ChatStore = ChatStore(
    repository = ChatRepository(unusedChatApiProxy(), ErrorMapper(Json { ignoreUnknownKeys = true })),
    dao = MinimalChatDao(),
    scheduler = object : OutboxScheduler {
        override fun scheduleDrain() = Unit
        override fun cancelDrain() = Unit
    },
    scrubRecovery = object : ScrubRecoveryFlag {
        override fun isPending(): Boolean = false
        override fun setPending(pending: Boolean): Boolean = true
    },
)

private fun unusedChatApiProxy(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi
