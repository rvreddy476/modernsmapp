package com.us.android.feature.live.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.network.noContentApiCall
import com.us.android.feature.live.data.BanUserRequest
import com.us.android.feature.live.data.ChatLog
import com.us.android.feature.live.data.CreateStreamRequest
import com.us.android.feature.live.data.EndedReason
import com.us.android.feature.live.data.HostMessageAction
import com.us.android.feature.live.data.LiveApi
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.LiveRoomFactory
import com.us.android.feature.live.data.LiveRoomSession
import com.us.android.feature.live.data.LiveStatus
import com.us.android.feature.live.data.LiveStreamDto
import com.us.android.feature.live.data.MAX_STREAM_MODERATORS
import com.us.android.feature.live.data.SetModeratorsRequest
import com.us.android.feature.live.data.canAddModerator
import com.us.android.feature.live.data.endedReasonOf
import com.us.android.feature.live.data.hostStatusAfterStart
import dagger.hilt.android.lifecycle.HiltViewModel
import io.livekit.android.room.Room
import io.livekit.android.room.track.VideoTrack
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The broadcaster's lifecycle: title → create + start on live-service-v2 →
 * LiveKit publisher connect → camera and mic on → END.
 *
 * 2026-10-01 (live-fix contract): the badge is the SERVER's. Start leaves the
 * stream `starting`; this screen says "Starting…" until `GET …/streams/:id`
 * answers `live` (the host's track reached LiveKit), then follows
 * `reconnecting`, `ended` and `failed` with the server's reason. A
 * successful LiveKit connect is not "live" — that was the old lie.
 *
 * The host also moderates from here: remove a message, ban or unban its
 * author, and promote up to five moderators. Chat is polled (no live-room
 * socket subscription on Android yet).
 *
 * The room is owned HERE, not by the composable: a recomposition must never
 * cost a broadcast. onCleared is the backstop that stops the camera when the
 * screen dies any other way; the server then ends the stream as `host_lost`
 * once its reconnect grace runs out.
 */
@Suppress("TooManyFunctions") // one public entry per host control; each is a few lines
@HiltViewModel
class GoLiveViewModel @Inject constructor(
    private val api: LiveApi,
    private val errorMapper: ErrorMapper,
    private val roomFactory: LiveRoomFactory,
) : ViewModel() {

    sealed interface Phase {
        data object Setup : Phase

        /** Create, start and the LiveKit connect are in flight. */
        data object Preparing : Phase

        /** The server's word while the broadcast exists: Starting, Live or Reconnecting. */
        data class OnAir(val status: LiveStatus) : Phase

        /**
         * Ended or Failed, with the server's reason. [unconfirmed] is true when
         * the host pressed End but the server did not acknowledge it.
         */
        data class Over(
            val status: LiveStatus,
            val reason: EndedReason,
            val unconfirmed: Boolean = false,
        ) : Phase

        /** Create or start was refused (the pilot gate) or the camera never connected. */
        data class Refused(val refusal: GoLiveRefusal) : Phase
    }

    data class UiState(
        val title: String = "",
        val phase: Phase = Phase.Setup,
        /** The server's count; the host is not in it. */
        val viewerCount: Int = 0,
        val chat: ChatLog = ChatLog(),
        val hostId: String = "",
        val moderators: List<String> = emptyList(),
        /** Banned from this stream during this session (there is no list route). */
        val banned: Set<String> = emptySet(),
        /** A one-line message for the snackbar host, cleared once shown. */
        val notice: String? = null,
        /** Bumped when the local camera track appears so the UI re-attaches. */
        val videoVersion: Int = 0,
    ) {
        val canGoLive: Boolean
            get() = title.isNotBlank() &&
                (phase == Phase.Setup || (phase is Phase.Refused && phase.refusal.canRetry))

        val isOnAir: Boolean get() = phase is Phase.OnAir

        val status: LiveStatus?
            get() = when (phase) {
                is Phase.OnAir -> phase.status
                is Phase.Over -> phase.status
                else -> null
            }
    }

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state.asStateFlow()

    private var session: LiveRoomSession? = null
    private var streamId: String? = null
    private var pollJob: Job? = null

    /** The LiveKit room, for rendering. Null until connecting. */
    val room: Room? get() = session?.room

    /** The local camera track, once publishing. Null until then. */
    fun localVideoTrack(): VideoTrack? = session?.localVideo()

    fun onTitleChanged(title: String) {
        _state.update { it.copy(title = title) }
    }

    fun onGoLive() {
        val current = _state.value
        if (!current.canGoLive) return
        _state.update { it.copy(phase = Phase.Preparing, notice = null) }
        viewModelScope.launch { goLive(current.title.trim()) }
    }

    /** Back to the form after a retryable refusal. */
    fun onTryAgain() {
        val phase = _state.value.phase
        if (phase is Phase.Refused && phase.refusal.canRetry) _state.update { it.copy(phase = Phase.Setup) }
    }

    private suspend fun goLive(title: String) {
        val request = CreateStreamRequest(title = title)
        val created = when (val result = apiCall(errorMapper) { api.createStream(request) }) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> return refuse(goLiveRefusal(result.error))
        }
        val id = created.id
        if (id.isBlank()) return refuse(goLiveRefusal(AppError.Malformed("create returned no stream id")))
        streamId = id
        _state.update { it.copy(hostId = created.creatorUserId) }

        val started = when (val result = apiCall(errorMapper) { api.startStream(id) }) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> return refuse(goLiveRefusal(result.error))
        }
        applyStatus(started.stream.status, started.stream)
        if (!_state.value.isOnAir) return

        val room = roomFactory.create()
        session = room
        if (!connectAndPublish(room, started.serverUrl, started.publisherToken)) {
            teardown()
            // Best effort: do not leave a `starting` stream for the sweeper.
            apiCall(errorMapper) { api.endStream(id) }
            streamId = null
            return refuse(GoLiveRefusal(HOST_MEDIA_FAILED_COPY, canRetry = true))
        }
        _state.update { it.copy(videoVersion = it.videoVersion + 1) }
        startPolling(id)
    }

    @Suppress("TooGenericExceptionCaught") // the LiveKit SDK throws whatever it throws
    private suspend fun connectAndPublish(room: LiveRoomSession, url: String, token: String): Boolean = try {
        room.connect(url, token)
        room.publishCameraAndMicrophone()
        true
    } catch (e: CancellationException) {
        throw e
    } catch (ignored: Exception) {
        false
    }

    private fun refuse(refusal: GoLiveRefusal) {
        _state.update { it.copy(phase = Phase.Refused(refusal)) }
    }

    private fun startPolling(id: String) {
        pollJob?.cancel()
        pollJob = viewModelScope.launch {
            while (isActive && _state.value.isOnAir) {
                delay(POLL_MILLIS)
                refreshStatus(id)
                if (_state.value.isOnAir) refreshChat(id)
            }
        }
    }

    private suspend fun refreshStatus(id: String) {
        // A failed poll keeps the last answer; it does not invent one.
        val stream = (apiCall(errorMapper) { api.getStream(id) } as? AppResult.Success)?.data ?: return
        if (_state.value.isOnAir) applyStatus(stream.status, stream)
    }

    private suspend fun refreshChat(id: String) {
        val rows = (listApiCall(errorMapper) { api.listChat(id) } as? AppResult.Success)?.data ?: return
        _state.update { it.copy(chat = it.chat.withSnapshot(rows)) }
    }

    private fun applyStatus(wire: String, stream: LiveStreamDto) {
        val status = hostStatusAfterStart(wire)
        if (status.isOver) {
            pollJob?.cancel()
            teardown()
            _state.update {
                it.copy(phase = Phase.Over(status, endedReasonOf(stream.endedReason)), viewerCount = stream.viewerCount)
            }
        } else {
            _state.update { it.copy(phase = Phase.OnAir(status), viewerCount = stream.viewerCount) }
        }
    }

    /** The host pressed End (after confirming). Stays on screen showing the ended state. */
    fun onEndStream() {
        val id = streamId ?: return
        if (!_state.value.isOnAir) return
        pollJob?.cancel()
        viewModelScope.launch {
            val acknowledged = apiCall(errorMapper) { api.endStream(id) } is AppResult.Success
            teardown()
            _state.update {
                it.copy(phase = Phase.Over(LiveStatus.Ended, EndedReason.HostEnded, unconfirmed = !acknowledged))
            }
        }
    }

    // ── Host moderation ────────────────────────────────────────────────

    fun onHostAction(message: LiveChatMessageDto, action: HostMessageAction) {
        when (action) {
            HostMessageAction.RemoveMessage -> onRemoveMessage(message.id)
            HostMessageAction.Ban -> onBan(message.userId)
            HostMessageAction.Unban -> onUnban(message.userId)
            HostMessageAction.MakeModerator -> onMakeModerator(message.userId)
            HostMessageAction.RemoveModerator -> onRemoveModerator(message.userId)
        }
    }

    /** `DELETE …/chat/:messageId`. A 404 means it is already gone: same outcome. */
    fun onRemoveMessage(messageId: String) {
        if (messageId.isBlank()) return
        moderate(
            call = { id -> api.removeChatMessage(id, messageId) },
            notFoundIsDone = true,
        ) { it.copy(chat = it.chat.withRemoved(messageId)) }
    }

    fun onBan(userId: String, reason: String? = null) {
        if (userId.isBlank() || userId == _state.value.hostId) return
        val request = BanUserRequest(userId = userId, reason = reason?.trim()?.takeIf { it.isNotEmpty() })
        moderate(call = { id -> api.banUser(id, request) }) {
            it.copy(banned = it.banned + userId, notice = "Banned from this stream.")
        }
    }

    fun onUnban(userId: String) {
        if (userId.isBlank()) return
        moderate(call = { id -> api.unbanUser(id, userId) }, notFoundIsDone = true) {
            it.copy(banned = it.banned - userId, notice = "Ban lifted.")
        }
    }

    fun onMakeModerator(userId: String) {
        val current = _state.value
        if (!canAddModerator(current.moderators, userId, current.hostId)) {
            if (current.moderators.size >= MAX_STREAM_MODERATORS) {
                _state.update { it.copy(notice = "A stream can have at most $MAX_STREAM_MODERATORS moderators.") }
            }
            return
        }
        putModerators(current.moderators + userId)
    }

    fun onRemoveModerator(userId: String) {
        val current = _state.value.moderators
        if (userId !in current) return
        putModerators(current - userId)
    }

    /** PUT replaces the whole set, so the request always carries the full list. */
    private fun putModerators(next: List<String>) {
        moderate(call = { id -> api.setModerators(id, SetModeratorsRequest(userIds = next)) }) {
            it.copy(moderators = next)
        }
    }

    private fun moderate(
        call: suspend (streamId: String) -> Unit,
        notFoundIsDone: Boolean = false,
        onDone: (UiState) -> UiState,
    ) {
        val id = streamId ?: return
        viewModelScope.launch {
            when (val result = noContentApiCall(errorMapper) { call(id) }) {
                is AppResult.Success -> _state.update(onDone)
                is AppResult.Failure -> if (notFoundIsDone && result.error is AppError.NotFound) {
                    _state.update(onDone)
                } else {
                    _state.update { it.copy(notice = moderationFailure(result.error)) }
                }
            }
        }
    }

    fun onNoticeShown() {
        _state.update { it.copy(notice = null) }
    }

    private fun teardown() {
        session?.disconnect()
        session = null
    }

    override fun onCleared() {
        pollJob?.cancel()
        teardown()
    }

    internal companion object {
        /** Status, viewer count and chat, together. */
        const val POLL_MILLIS = 3_000L
    }
}
