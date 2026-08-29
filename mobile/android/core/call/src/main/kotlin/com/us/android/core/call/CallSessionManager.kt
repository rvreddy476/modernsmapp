package com.us.android.core.call

import com.us.android.core.call.data.CallRepository
import com.us.android.core.call.engine.CallEngine
import com.us.android.core.call.engine.CallEngineEvent
import com.us.android.core.call.engine.CallEngineFactory
import com.us.android.core.call.signaling.CallSignal
import com.us.android.core.call.signaling.CallSignals
import com.us.android.core.call.signaling.parseCallSignal
import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.Json

/** The one call this device is in, as every surface renders it. */
sealed interface CallState {
    data object Idle : CallState

    /** We are ringing someone. */
    data class Outgoing(val callId: String, val peerId: String, val video: Boolean) : CallState

    /** Someone is ringing us. */
    data class Incoming(
        val callId: String,
        val inviteId: String,
        val callerId: String,
        val video: Boolean,
    ) : CallState

    /** Both sides said yes; SDP/ICE are converging. */
    data class Connecting(
        val callId: String,
        val peerId: String,
        val video: Boolean,
        val incoming: Boolean,
    ) : CallState

    /** Media is flowing. */
    data class Active(
        val callId: String,
        val peerId: String,
        val video: Boolean,
        val startedAtMillis: Long,
        val muted: Boolean = false,
        val videoEnabled: Boolean = true,
        val speakerOn: Boolean = false,
        val remoteVideoAvailable: Boolean = false,
    ) : CallState

    /** Terminal, until the surface dismisses it ([CallSessionManager.reset]). */
    data class Ended(
        val reason: CallEndReason,
        val peerId: String,
        val video: Boolean,
        val durationMillis: Long,
    ) : CallState
}

enum class CallEndReason {
    HungUp, RemoteEnded, Declined, Busy, NoAnswer, Missed, Failed, NotAllowed
}

/** Ring/ongoing notification surface — the impl posts on the CALLS channel. */
interface CallNotifier {
    fun showIncoming(callId: String, callerId: String, video: Boolean)
    fun clearIncoming()
    fun startOngoing(peerId: String, video: Boolean)
    fun stopOngoing()
}

/** Audio focus + routing (earpiece/speaker) for the duration of a call. */
interface CallAudioController {
    fun onCallStarted(video: Boolean)
    fun setSpeaker(on: Boolean)
    fun onCallEnded()
}

/**
 * ONE active call per device (calling P0) — the state machine both call
 * surfaces render and every signal routes through.
 *
 * Server-authoritative throughout: a call EXISTS only when POST /v1/calls
 * accepted it (CallPolicy + the graph permission matrix), signaling relays
 * only while call-service's pair authorization stands, and every inbound
 * frame is re-validated fail-closed ([parseCallSignal]) — a malformed or
 * mismatched signal is dropped, never repaired.
 *
 * Signaling order (the offer is sent only AFTER the callee accepts, so a
 * callee who cold-starts from a push never misses it):
 * ring → accept(REST, then frame) → offer → answer → ICE trickle.
 */
@Suppress("TooManyFunctions", "LongParameterList")
class CallSessionManager(
    private val repository: CallRepository,
    private val session: ChatSessionManager,
    private val engineFactory: CallEngineFactory,
    private val notifier: CallNotifier,
    private val audio: CallAudioController,
    private val scope: CoroutineScope,
    private val ringTimeoutMillis: Long = RING_TIMEOUT_MILLIS,
    private val signalingReadyTimeoutMillis: Long = SIGNALING_READY_TIMEOUT_MILLIS,
    private val reconcileIntervalMillis: Long = RECONCILE_INTERVAL_MILLIS,
    private val iceRetryDelayMillis: Long = ICE_RETRY_DELAY_MILLIS,
) {

    private val json = Json { ignoreUnknownKeys = true }
    private val lock = Mutex()

    private val _state = MutableStateFlow<CallState>(CallState.Idle)
    val state: StateFlow<CallState> = _state.asStateFlow()

    private var engine: CallEngine? = null
    private var engineJob: Job? = null
    private var ringTimeoutJob: Job? = null
    private var reconcileJob: Job? = null

    /** Local ICE whose socket send failed, awaiting a bounded retry. */
    private val pendingIceRetries = mutableListOf<CallEngineEvent.LocalIceCandidate>()
    private var iceRetryJob: Job? = null

    /** Remote ICE arriving before the SDP exchange completes is buffered. */
    private val pendingRemoteIce = mutableListOf<CallSignal.Ice>()
    private var sdpExchanged = false

    /** Local ICE is held until the pair authorization is ACTIVE server-side. */
    private val pendingLocalIce = mutableListOf<CallEngineEvent.LocalIceCandidate>()
    private var localIceReleased = false

    init {
        scope.launch {
            session.events.collect { event ->
                if (event is ChatSocketEvent.CallSignal) {
                    parseCallSignal(event.type, event.frame)?.let { onSignal(it) }
                }
            }
        }
    }

    // ── Caller ──────────────────────────────────────────────────────────

    /**
     * Rings [peerId]. Returns null on success or a USER-FACING refusal line —
     * generic for policy denials, because the server's answer is generic.
     *
     * Order matters, and each step is truthful (CALL-LB-2/5):
     *  1. RESERVE the one call slot inside the mutex BEFORE any network I/O —
     *     a concurrent second tap loses here, not after two server calls;
     *  2. await the shared socket's readiness — no server call is ever made
     *     for a ring whose signaling cannot be delivered;
     *  3. REST create + the caller's own join (ICE servers; the backend
     *     activates only on the CALLEE's join);
     *  4. send the ring frame — a FAILED send ends the call server-side and
     *     reports failure instead of ringing nobody silently.
     */
    suspend fun startOutgoing(peerId: String, video: Boolean, conversationId: String?): String? {
        lock.withLock {
            if (_state.value !is CallState.Idle && _state.value !is CallState.Ended) {
                return "You're already in a call."
            }
            resetSignalingBuffers()
            // The RESERVATION: the slot is taken before the mutex releases.
            _state.value = CallState.Outgoing(callId = "", peerId = peerId, video = video)
        }

        if (!awaitSignalingReady()) {
            lock.withLock { _state.value = CallState.Idle }
            return NO_CONNECTION
        }
        return placeOutgoingCall(peerId, video, conversationId)
    }

    /** Steps 3–4 of [startOutgoing]: create, join, ring — each truthful. */
    private suspend fun placeOutgoingCall(
        peerId: String,
        video: Boolean,
        conversationId: String?,
    ): String? {
        val created = repository.createDirectCall(peerId, video, conversationId)
        val call = when (created) {
            is AppResult.Failure -> {
                lock.withLock { _state.value = CallState.Idle }
                return created.error.toStartRefusal()
            }
            is AppResult.Success -> created.data
        }
        val grant = repository.join(call.id)
        if (grant is AppResult.Failure) {
            // CALL-LB-2: without the caller's own join there is no usable
            // media grant — ringing anyway creates a call that cannot
            // succeed. End the just-created call server-side and refuse
            // truthfully instead of pretending an empty grant is fine.
            repository.end(call.id)
            lock.withLock { _state.value = CallState.Idle }
            return GENERIC_FAILURE
        }
        val iceServers = (grant as AppResult.Success).data.iceServers

        lock.withLock {
            _state.value = CallState.Outgoing(call.id, peerId, video)
            startEngine(video = video, iceServers = iceServers, peerId = peerId, callId = call.id)
        }
        if (!session.sendCallFrame(CallSignals.ring(json, peerId, call.id, video))) {
            // The callee can never learn about this call: undo it truthfully.
            repository.end(call.id)
            endLocally(CallEndReason.Failed)
            lock.withLock { _state.value = CallState.Idle }
            return NO_CONNECTION
        }
        armRingTimeout(call.id) {
            repository.end(call.id)
            session.sendCallFrame(CallSignals.end(json, peerId, call.id))
            endLocally(CallEndReason.NoAnswer)
        }
        armReconciliation(call.id)
        return null
    }

    /**
     * Bounded server reconciliation (CALL-LB-2): terminal frames are
     * best-effort, so a peer whose REST decline/end/leave succeeded but
     * whose socket frame was lost would otherwise leave THIS side ringing
     * or in-call forever. While a call is live, the SERVER state is polled
     * every [reconcileIntervalMillis]; a terminal server state converges
     * this side within one interval — the server stays authoritative even
     * through a WebSocket outage.
     */
    private fun armReconciliation(callId: String) {
        reconcileJob?.cancel()
        reconcileJob = scope.launch {
            while (true) {
                delay(reconcileIntervalMillis)
                val current = _state.value
                if (activeCallId(current) != callId) return@launch
                val server = (repository.call(callId) as? AppResult.Success)?.data ?: continue
                if (server.state in TERMINAL_CALL_STATES) {
                    val reason = if (current is CallState.Incoming) {
                        CallEndReason.Missed
                    } else {
                        CallEndReason.RemoteEnded
                    }
                    endLocally(reason)
                    return@launch
                }
            }
        }
    }

    private fun cancelReconciliation() {
        reconcileJob?.cancel()
        reconcileJob = null
    }

    /**
     * Starts the shared session socket (idempotent) and awaits its readiness.
     * Cold starts — a push tap with no chat screen ever opened — arrive here
     * with the socket never started; without this, every signal send returned
     * false and was ignored (CALL-LB-2).
     */
    private suspend fun awaitSignalingReady(): Boolean {
        session.start()
        return withTimeoutOrNull(signalingReadyTimeoutMillis) {
            session.connection.first { it == ChatSessionManager.ConnectionState.Connected }
        } != null
    }

    // ── Callee ──────────────────────────────────────────────────────────

    /**
     * Re-checks the SERVER for a pending invite — the cold-start path (push
     * tap with no live socket history) and the ring-frame verification path
     * share it: an invite the server does not confirm never rings the UI.
     */
    suspend fun refreshIncoming() {
        val invites = (repository.pendingInvites() as? AppResult.Success)?.data ?: return
        val invite = invites.firstOrNull { it.callState == "ringing" || it.callState == "initiated" }
            ?: return
        lock.withLock {
            if (_state.value !is CallState.Idle) return
            resetSignalingBuffers()
            _state.value = CallState.Incoming(
                callId = invite.callId,
                inviteId = invite.inviteId,
                callerId = invite.inviterUserId,
                video = invite.video,
            )
        }
        notifier.showIncoming(invite.callId, invite.inviterUserId, invite.video)
        armRingTimeout(invite.callId) {
            notifier.clearIncoming()
            endLocally(CallEndReason.Missed)
        }
        armReconciliation(invite.callId)
    }

    /**
     * Accepts the ringing call.
     *
     * [cameraGranted] is the CALL-LB-6 gate: a VIDEO invite may not proceed
     * to accept — let alone start a camera engine — without the camera
     * permission; the surface requests it first and passes the result here.
     * Refusal keeps the call ringing so the user can grant and retry.
     *
     * Signaling readiness is awaited BEFORE the REST accept (CALL-LB-2): the
     * reviewed hang was exactly "REST accept succeeded, accept frame lost" —
     * so nothing server-visible happens until the frame can be delivered,
     * and a send that still fails afterwards leaves the call truthfully
     * instead of hanging both sides.
     */
    suspend fun acceptIncoming(cameraGranted: Boolean = true): String? {
        val incoming = _state.value as? CallState.Incoming ?: return null
        if (incoming.video && !cameraGranted) {
            return "Video calls need camera access."
        }
        if (!awaitSignalingReady()) {
            // Still Incoming: the user may retry once connectivity returns.
            return NO_CONNECTION
        }
        cancelRingTimeout()
        notifier.clearIncoming()
        val accepted = repository.acceptInvite(incoming.callId, incoming.inviteId)
        if (accepted is AppResult.Failure) {
            endLocally(CallEndReason.Failed)
            return "The call could not be answered."
        }
        val grant = repository.join(incoming.callId)
        if (grant is AppResult.Failure) {
            // CALL-LB-2: the accept already changed server state, but with
            // no join grant this side can never carry media — a failed join
            // IS a failed answer. Converge the SERVER first (leave ends a
            // direct call for both, CALL-LB-3), then fail locally; never
            // start an engine or claim call_accept on an unusable grant.
            repository.leave(incoming.callId)
            endLocally(CallEndReason.Failed)
            return "The call could not be answered."
        }
        val iceServers = (grant as AppResult.Success).data.iceServers
        lock.withLock {
            _state.value = CallState.Connecting(
                callId = incoming.callId,
                peerId = incoming.callerId,
                video = incoming.video,
                incoming = true,
            )
            startEngine(
                video = incoming.video,
                iceServers = iceServers,
                peerId = incoming.callerId,
                callId = incoming.callId,
            )
            // Our own REST accept moved the pair authorization to ACTIVE —
            // local ICE may relay from here on.
            localIceReleased = true
        }
        if (!session.sendCallFrame(CallSignals.accept(json, incoming.callerId, incoming.callId))) {
            // REST accepted but the caller can never learn: leave truthfully
            // (the server end is idempotent) rather than hang both sides.
            repository.leave(incoming.callId)
            endLocally(CallEndReason.Failed)
            return NO_CONNECTION
        }
        return null
    }

    suspend fun declineIncoming() {
        val incoming = _state.value as? CallState.Incoming ?: return
        cancelRingTimeout()
        notifier.clearIncoming()
        // The REST decline is AUTHORITATIVE and needs no socket: the server
        // ends the direct call for both sides (CALL-LB-3), and the caller's
        // own reconciliation converges it even if this frame is lost.
        repository.declineInvite(incoming.callId, incoming.inviteId)
        session.sendCallFrame(CallSignals.decline(json, incoming.callerId, incoming.callId))
        cancelReconciliation()
        lock.withLock { _state.value = CallState.Idle }
    }

    // ── In-call controls ────────────────────────────────────────────────

    suspend fun hangUp() {
        when (val current = _state.value) {
            is CallState.Outgoing -> {
                cancelRingTimeout()
                repository.end(current.callId)
                session.sendCallFrame(CallSignals.end(json, current.peerId, current.callId))
                endLocally(CallEndReason.HungUp)
            }
            is CallState.Connecting -> {
                repository.leave(current.callId)
                session.sendCallFrame(CallSignals.end(json, current.peerId, current.callId))
                endLocally(CallEndReason.HungUp)
            }
            is CallState.Active -> {
                repository.leave(current.callId)
                session.sendCallFrame(CallSignals.end(json, current.peerId, current.callId))
                endLocally(CallEndReason.HungUp)
            }
            else -> Unit
        }
    }

    fun toggleMute() = updateActive { active ->
        val muted = !active.muted
        engine?.setMuted(muted)
        active.copy(muted = muted)
    }

    fun toggleVideo() = updateActive { active ->
        val enabled = !active.videoEnabled
        engine?.setVideoEnabled(enabled)
        active.copy(videoEnabled = enabled)
    }

    fun toggleSpeaker() = updateActive { active ->
        val on = !active.speakerOn
        audio.setSpeaker(on)
        active.copy(speakerOn = on)
    }

    fun switchCamera() {
        engine?.switchCamera()
    }

    /** Rendering hooks for :feature:call. */
    fun currentEngine(): CallEngine? = engine

    /** The surface dismissed the Ended card. */
    fun reset() {
        if (_state.value is CallState.Ended) _state.value = CallState.Idle
    }

    /** Logout teardown: end whatever is live, quietly and completely. */
    suspend fun teardown() {
        when (val current = _state.value) {
            is CallState.Outgoing -> repository.end(current.callId)
            is CallState.Connecting -> repository.leave(current.callId)
            is CallState.Active -> repository.leave(current.callId)
            is CallState.Incoming -> repository.declineInvite(current.callId, current.inviteId)
            else -> Unit
        }
        cancelRingTimeout()
        cancelReconciliation()
        cancelIceRetries()
        notifier.clearIncoming()
        releaseEngine()
        lock.withLock { _state.value = CallState.Idle }
    }

    // ── Signals ─────────────────────────────────────────────────────────

    private suspend fun onSignal(signal: CallSignal) {
        val current = _state.value
        when (signal) {
            is CallSignal.Ring -> onRing(signal, current)
            is CallSignal.Accept -> onAccept(signal, current)
            is CallSignal.Offer -> onOffer(signal, current)
            is CallSignal.Answer -> onAnswer(signal, current)
            is CallSignal.Ice -> onRemoteIce(signal, current)
            // Terminal signals also converge SERVER state (CALL-LB-3): the
            // peer's own REST call may have failed or raced, and the
            // backend's direct-call termination is idempotent, so a second
            // leave/end costs nothing and un-strands both sides.
            is CallSignal.Decline ->
                ifOurPeer(signal, current) {
                    scope.launch { repository.end(signal.callId) }
                    endLocally(CallEndReason.Declined)
                }
            is CallSignal.Busy ->
                ifOurPeer(signal, current) {
                    scope.launch { repository.end(signal.callId) }
                    endLocally(CallEndReason.Busy)
                }
            is CallSignal.End ->
                ifOurPeer(signal, current) {
                    scope.launch { repository.leave(signal.callId) }
                    endLocally(CallEndReason.RemoteEnded)
                }
        }
    }

    private suspend fun onRing(signal: CallSignal.Ring, current: CallState) {
        if (current is CallState.Idle || current is CallState.Ended) {
            // The frame only triggers a SERVER lookup; the invite the server
            // does not confirm never rings (fail-closed).
            refreshIncoming()
        } else if (activeCallId(current) != signal.callId) {
            // Already busy with another call: tell the second caller.
            if (!session.sendCallFrame(CallSignals.busy(json, signal.senderId, signal.callId))) {
                // CALL-LB-2: the busy frame was lost — converge the SECOND
                // call server-side over authenticated REST so its caller
                // is not left ringing into the void. The current call is
                // untouched either way.
                scope.launch { declineForeignInvite(signal.callId) }
            }
        }
    }

    /** REST-declines an invite for a call this device is NOT in. */
    private suspend fun declineForeignInvite(callId: String) {
        val invites = (repository.pendingInvites() as? AppResult.Success)?.data ?: return
        invites.firstOrNull { it.callId == callId }?.let {
            repository.declineInvite(it.callId, it.inviteId)
        }
    }

    private suspend fun onAccept(signal: CallSignal.Accept, current: CallState) {
        val outgoing = current as? CallState.Outgoing ?: return
        if (outgoing.callId != signal.callId || outgoing.peerId != signal.senderId) return
        cancelRingTimeout()
        lock.withLock {
            _state.value = CallState.Connecting(
                callId = outgoing.callId,
                peerId = outgoing.peerId,
                video = outgoing.video,
                incoming = false,
            )
            // The callee's accept moved the pair to ACTIVE server-side.
            localIceReleased = true
            flushLocalIce(outgoing.peerId, outgoing.callId)
        }
        val sdp = runCatching { engine?.createOffer() }.getOrNull() ?: run {
            repository.end(outgoing.callId)
            endLocally(CallEndReason.Failed)
            return
        }
        if (!session.sendCallFrame(CallSignals.offer(json, outgoing.peerId, outgoing.callId, sdp))) {
            // A lost offer hangs both sides forever — end truthfully.
            repository.end(outgoing.callId)
            endLocally(CallEndReason.Failed)
        }
    }

    private suspend fun onOffer(signal: CallSignal.Offer, current: CallState) {
        val connecting = current as? CallState.Connecting ?: return
        if (!connecting.incoming || connecting.callId != signal.callId ||
            connecting.peerId != signal.senderId
        ) {
            return
        }
        val answer = runCatching { engine?.acceptOffer(signal.sdp) }.getOrNull() ?: run {
            repository.leave(connecting.callId)
            endLocally(CallEndReason.Failed)
            return
        }
        if (!session.sendCallFrame(CallSignals.answer(json, connecting.peerId, connecting.callId, answer))) {
            repository.leave(connecting.callId)
            endLocally(CallEndReason.Failed)
            return
        }
        lock.withLock {
            sdpExchanged = true
            flushRemoteIce()
        }
    }

    private suspend fun onAnswer(signal: CallSignal.Answer, current: CallState) {
        val connecting = current as? CallState.Connecting ?: return
        if (connecting.incoming || connecting.callId != signal.callId ||
            connecting.peerId != signal.senderId
        ) {
            return
        }
        val applied = runCatching { engine?.completeWithAnswer(signal.sdp) }.isSuccess
        if (!applied) {
            // CALL-LB-2: converge the SERVER before ending locally — an
            // unapplied answer otherwise leaves the call and the peer live.
            repository.end(connecting.callId)
            endLocally(CallEndReason.Failed)
            return
        }
        lock.withLock {
            sdpExchanged = true
            flushRemoteIce()
        }
    }

    private suspend fun onRemoteIce(signal: CallSignal.Ice, current: CallState) {
        if (activeCallId(current) != signal.callId) return
        if (peerOf(current) != signal.senderId) return
        lock.withLock {
            if (sdpExchanged) {
                engine?.addRemoteIceCandidate(signal.candidate, signal.sdpMid, signal.sdpMLineIndex)
            } else {
                pendingRemoteIce += signal
            }
        }
    }

    private inline fun ifOurPeer(signal: CallSignal, current: CallState, block: () -> Unit) {
        if (activeCallId(current) == signal.callId && peerOf(current) == signal.senderId) block()
    }

    // ── Engine ──────────────────────────────────────────────────────────

    private fun startEngine(
        video: Boolean,
        iceServers: List<com.us.android.core.call.data.IceServerConfig>,
        peerId: String,
        callId: String,
    ) {
        val created = engineFactory.create()
        engine = created
        created.start(video, iceServers)
        audio.onCallStarted(video)
        engineJob = scope.launch {
            created.events.collect { event -> onEngineEvent(event, peerId, callId) }
        }
    }

    private suspend fun onEngineEvent(event: CallEngineEvent, peerId: String, callId: String) {
        when (event) {
            is CallEngineEvent.LocalIceCandidate -> lock.withLock {
                if (localIceReleased) {
                    sendLocalIce(event, peerId, callId)
                } else {
                    pendingLocalIce += event
                }
            }
            CallEngineEvent.Connected -> {
                val connecting = _state.value as? CallState.Connecting ?: return
                if (connecting.callId != callId) return
                _state.value = CallState.Active(
                    callId = connecting.callId,
                    peerId = connecting.peerId,
                    video = connecting.video,
                    startedAtMillis = System.currentTimeMillis(),
                    videoEnabled = connecting.video,
                )
                notifier.startOngoing(connecting.peerId, connecting.video)
            }
            CallEngineEvent.Failed -> {
                val active = activeCallId(_state.value)
                if (active == callId && _state.value !is CallState.Ended) {
                    repository.leave(callId)
                    endLocally(CallEndReason.Failed)
                }
            }
            is CallEngineEvent.RemoteVideo -> updateActive {
                it.copy(remoteVideoAvailable = event.available)
            }
        }
    }

    /** Called under [lock]. A failed send QUEUES the candidate (CALL-LB-2). */
    private fun sendLocalIce(candidate: CallEngineEvent.LocalIceCandidate, peerId: String, callId: String) {
        if (!sendIceFrame(candidate, peerId, callId)) {
            // A silently discarded candidate can kill the media path while
            // the UI still says connecting/active. Queue it for a bounded
            // retry through the live socket; if the socket never recovers,
            // the call FAILS truthfully instead of hanging.
            pendingIceRetries += candidate
            if (iceRetryJob?.isActive != true) {
                iceRetryJob = scope.launch { retryLocalIce(peerId, callId) }
            }
        }
    }

    private fun sendIceFrame(
        candidate: CallEngineEvent.LocalIceCandidate,
        peerId: String,
        callId: String,
    ): Boolean = session.sendCallFrame(
        CallSignals.iceCandidate(
            json = json,
            targetUserId = peerId,
            callId = callId,
            candidate = candidate.candidate,
            sdpMid = candidate.sdpMid,
            sdpMLineIndex = candidate.sdpMLineIndex,
        ),
    )

    private suspend fun retryLocalIce(peerId: String, callId: String) {
        repeat(ICE_RETRY_MAX_ATTEMPTS) {
            delay(iceRetryDelayMillis)
            if (activeCallId(_state.value) != callId) {
                lock.withLock { pendingIceRetries.clear() }
                return
            }
            val flushed = lock.withLock {
                val queued = pendingIceRetries.toList()
                pendingIceRetries.clear()
                var ok = true
                for (candidate in queued) {
                    if (ok && sendIceFrame(candidate, peerId, callId)) continue
                    ok = false
                    pendingIceRetries += candidate
                }
                ok && pendingIceRetries.isEmpty()
            }
            if (flushed) return
        }
        // The socket never carried the candidates: required media signaling
        // is undeliverable, so the call must not stay up as a husk.
        if (activeCallId(_state.value) == callId) {
            repository.leave(callId)
            endLocally(CallEndReason.Failed)
        }
    }

    private fun flushLocalIce(peerId: String, callId: String) {
        pendingLocalIce.forEach { sendLocalIce(it, peerId, callId) }
        pendingLocalIce.clear()
    }

    private fun flushRemoteIce() {
        pendingRemoteIce.forEach {
            engine?.addRemoteIceCandidate(it.candidate, it.sdpMid, it.sdpMLineIndex)
        }
        pendingRemoteIce.clear()
    }

    // ── Shared teardown ─────────────────────────────────────────────────

    private fun endLocally(reason: CallEndReason) {
        val current = _state.value
        val peerId = peerOf(current).orEmpty()
        val video = when (current) {
            is CallState.Outgoing -> current.video
            is CallState.Incoming -> current.video
            is CallState.Connecting -> current.video
            is CallState.Active -> current.video
            else -> false
        }
        val duration = (current as? CallState.Active)
            ?.let { System.currentTimeMillis() - it.startedAtMillis } ?: 0L
        cancelRingTimeout()
        cancelReconciliation()
        cancelIceRetries()
        notifier.clearIncoming()
        notifier.stopOngoing()
        releaseEngine()
        _state.value = CallState.Ended(reason, peerId, video, duration)
    }

    private fun releaseEngine() {
        engineJob?.cancel()
        engineJob = null
        engine?.release()
        engine = null
        audio.onCallEnded()
    }

    private fun resetSignalingBuffers() {
        pendingRemoteIce.clear()
        pendingLocalIce.clear()
        pendingIceRetries.clear()
        sdpExchanged = false
        localIceReleased = false
    }

    private fun cancelIceRetries() {
        iceRetryJob?.cancel()
        iceRetryJob = null
        pendingIceRetries.clear()
    }

    private fun armRingTimeout(callId: String, onTimeout: suspend () -> Unit) {
        cancelRingTimeout()
        ringTimeoutJob = scope.launch {
            delay(ringTimeoutMillis)
            if (activeCallId(_state.value) == callId &&
                (_state.value is CallState.Outgoing || _state.value is CallState.Incoming)
            ) {
                onTimeout()
            }
        }
    }

    private fun cancelRingTimeout() {
        ringTimeoutJob?.cancel()
        ringTimeoutJob = null
    }

    private fun updateActive(block: (CallState.Active) -> CallState.Active) {
        val active = _state.value as? CallState.Active ?: return
        _state.value = block(active)
    }

    private fun activeCallId(state: CallState): String? = when (state) {
        is CallState.Outgoing -> state.callId
        is CallState.Incoming -> state.callId
        is CallState.Connecting -> state.callId
        is CallState.Active -> state.callId
        else -> null
    }

    private fun peerOf(state: CallState): String? = when (state) {
        is CallState.Outgoing -> state.peerId
        is CallState.Incoming -> state.callerId
        is CallState.Connecting -> state.peerId
        is CallState.Active -> state.peerId
        else -> null
    }

    private fun AppError.toStartRefusal(): String = when (this) {
        is AppError.Forbidden -> GENERIC_REFUSAL
        is AppError.Unknown -> if (statusCode == HTTP_FORBIDDEN) GENERIC_REFUSAL else GENERIC_FAILURE
        is AppError.RateLimited -> "Too many calls right now. Try again in a moment."
        is AppError.NoNetwork, is AppError.Timeout -> "No connection. Try again."
        else -> GENERIC_FAILURE
    }

    private companion object {
        const val RING_TIMEOUT_MILLIS = 60_000L
        const val SIGNALING_READY_TIMEOUT_MILLIS = 10_000L
        const val RECONCILE_INTERVAL_MILLIS = 5_000L
        const val ICE_RETRY_DELAY_MILLIS = 500L
        const val ICE_RETRY_MAX_ATTEMPTS = 10
        const val HTTP_FORBIDDEN = 403
        const val NO_CONNECTION = "No connection. Try again."
        val TERMINAL_CALL_STATES = setOf("ended", "canceled", "failed", "expired")

        // ONE line for every policy refusal — block, privacy, circle. The
        // server answers generically so the client cannot leak block state.
        const val GENERIC_REFUSAL = "This person can't be called right now."
        const val GENERIC_FAILURE = "The call couldn't be started. Try again."
    }
}
