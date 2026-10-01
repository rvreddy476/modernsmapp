package com.us.android.feature.live.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.network.noContentApiCall
import com.us.android.core.ui.UsPostReportState
import com.us.android.feature.live.data.BanUserRequest
import com.us.android.feature.live.data.ChatLog
import com.us.android.feature.live.data.EndedReason
import com.us.android.feature.live.data.LiveApi
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.LiveReportReason
import com.us.android.feature.live.data.LiveReportRequest
import com.us.android.feature.live.data.LiveRoomFactory
import com.us.android.feature.live.data.LiveRoomSession
import com.us.android.feature.live.data.LiveRoomSignal
import com.us.android.feature.live.data.LiveStatus
import com.us.android.feature.live.data.LiveStreamDto
import com.us.android.feature.live.data.SendChatRequest
import com.us.android.feature.live.data.ViewerMessageAction
import com.us.android.feature.live.data.endedReasonOf
import com.us.android.feature.live.data.liveStatusOf
import com.us.android.feature.live.data.viewerMessageActions
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
 * A viewer's session: stream status → viewer token → subscriber-only LiveKit
 * connect → the first remote video track is the show.
 *
 * 2026-10-01 (live-fix contract): what the screen says is the server's
 * status, polled with the chat every [POLL_MILLIS]. Starting, Live,
 * Reconnecting, Ended (with its reason) and Failed each have their own
 * words; the room's own events only decide whether there is a picture to
 * draw. The viewer count is the server's, which leaves the host out — the
 * room's participant list would count the host, so it is never used.
 *
 * Chat stays on REST polling: the app's only socket (`ChatSocket` in
 * :core:chat) is the messaging session's, and it neither subscribes to live
 * rooms nor parses their frames. A removal reaches this screen as the
 * message missing from the next poll; [ChatLog] keeps it from coming back.
 *
 * A viewer the host made a moderator gets `moderator_user_ids` on the
 * stream (the server shows it to the host and moderators only); that
 * presence unlocks Remove message and Ban user on their long-press menu.
 */
@Suppress("TooManyFunctions") // one public entry per viewer control
@HiltViewModel
class LiveWatchViewModel @Inject constructor(
    private val api: LiveApi,
    private val errorMapper: ErrorMapper,
    private val roomFactory: LiveRoomFactory,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    /** What a report is about: the whole stream, or one chat message. */
    sealed interface ReportTarget {
        data object Stream : ReportTarget
        data class Message(val message: LiveChatMessageDto) : ReportTarget
    }

    data class UiState(
        val title: String = "",
        val status: LiveStatus = LiveStatus.Unknown,
        val endedReason: EndedReason = EndedReason.Unknown,
        /** The first status read is in flight. */
        val loading: Boolean = true,
        val joinError: String? = null,
        /** A remote video track is subscribed and drawable. */
        val hasVideo: Boolean = false,
        /** Bumped when the remote video track changes so the UI re-attaches. */
        val videoVersion: Int = 0,
        /** The server's count; the host is not in it. */
        val viewerCount: Int = 0,
        val chat: ChatLog = ChatLog(),
        val draft: String = "",
        val sending: Boolean = false,
        val notice: String? = null,
        val reportTarget: ReportTarget? = null,
        val report: UsPostReportState = UsPostReportState.Idle,
        val hostId: String = "",
        /** The server listed this stream's moderators for us: we are one of them. */
        val canModerate: Boolean = false,
        /** The message whose moderator menu is open. */
        val selected: LiveChatMessageDto? = null,
    ) {
        val canChat: Boolean get() = joinError == null && !loading && !status.isOver
    }

    private val streamId: String = savedStateHandle.get<String>("streamId").orEmpty()

    private val _state = MutableStateFlow(UiState())
    val state: StateFlow<UiState> = _state.asStateFlow()

    private var session: LiveRoomSession? = null
    private var signalsJob: Job? = null
    private var pollJob: Job? = null

    /** The LiveKit room, for rendering. */
    val room: Room? get() = session?.room

    /** The broadcaster's video, once subscribed. */
    val remoteVideo: VideoTrack? get() = session?.remoteVideo()

    init {
        viewModelScope.launch { join() }
    }

    /** "Try again" after a failed join or a lost connection. */
    fun onRetry() {
        pollJob?.cancel()
        teardown()
        viewModelScope.launch { join() }
    }

    private suspend fun join() {
        _state.update { it.copy(loading = true, joinError = null, hasVideo = false) }
        val stream = when (val result = apiCall(errorMapper) { api.getStream(streamId) }) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> return failJoin(watchJoinFailure(result.error))
        }
        applyStream(stream)
        // An ended or failed stream has nothing to join: no token, no room.
        if (_state.value.status.isOver) {
            _state.update { it.copy(loading = false) }
            return
        }
        val grant = when (val result = apiCall(errorMapper) { api.viewerToken(streamId) }) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> return failJoin(watchJoinFailure(result.error))
        }
        val room = roomFactory.create()
        session = room
        signalsJob = viewModelScope.launch { room.signals.collect(::onSignal) }
        if (!connect(room, grant.serverUrl, grant.token)) {
            teardown()
            return failJoin(JOIN_FAILED_COPY)
        }
        _state.update { it.copy(loading = false) }
        refreshChat()
        startPolling()
    }

    @Suppress("TooGenericExceptionCaught") // the LiveKit SDK throws whatever it throws
    private suspend fun connect(room: LiveRoomSession, url: String, token: String): Boolean = try {
        room.connect(url, token)
        true
    } catch (e: CancellationException) {
        throw e
    } catch (ignored: Exception) {
        false
    }

    private fun failJoin(message: String) {
        _state.update { it.copy(loading = false, joinError = message) }
    }

    private fun onSignal(signal: LiveRoomSignal) {
        when (signal) {
            LiveRoomSignal.VideoReady -> _state.update {
                it.copy(hasVideo = true, videoVersion = it.videoVersion + 1)
            }
            LiveRoomSignal.VideoGone -> _state.update { it.copy(hasVideo = false) }
            // Our own connection dropped. Ask the server whether the stream
            // is still on before saying anything about it.
            LiveRoomSignal.Disconnected -> viewModelScope.launch {
                refreshStatus()
                if (!_state.value.status.isOver) {
                    pollJob?.cancel()
                    teardown()
                    failJoin(CONNECTION_LOST_COPY)
                }
            }
        }
    }

    private fun startPolling() {
        pollJob?.cancel()
        pollJob = viewModelScope.launch {
            while (isActive && !_state.value.status.isOver) {
                delay(POLL_MILLIS)
                refreshStatus()
                if (!_state.value.status.isOver) refreshChat()
            }
        }
    }

    private suspend fun refreshStatus() {
        // A failed poll keeps the last answer; it does not invent one.
        val stream = (apiCall(errorMapper) { api.getStream(streamId) } as? AppResult.Success)?.data ?: return
        applyStream(stream)
    }

    private suspend fun refreshChat() {
        val rows = (listApiCall(errorMapper) { api.listChat(streamId) } as? AppResult.Success)?.data ?: return
        _state.update { it.copy(chat = it.chat.withSnapshot(rows)) }
    }

    private fun applyStream(stream: LiveStreamDto) {
        val status = liveStatusOf(stream.status)
        _state.update {
            it.copy(
                title = stream.title.ifBlank { it.title },
                status = status,
                endedReason = endedReasonOf(stream.endedReason),
                viewerCount = stream.viewerCount,
                hostId = stream.creatorUserId.ifBlank { it.hostId },
                canModerate = stream.moderatorUserIds != null,
            )
        }
        if (status.isOver) {
            pollJob?.cancel()
            teardown()
            _state.update { it.copy(hasVideo = false) }
        }
    }

    // ── Chat ───────────────────────────────────────────────────────────

    fun onDraftChanged(draft: String) {
        _state.update { it.copy(draft = draft) }
    }

    fun onSendChat() {
        val current = _state.value
        val text = current.draft.trim()
        if (text.isEmpty() || current.sending || !current.canChat) return
        _state.update { it.copy(draft = "", sending = true) }
        viewModelScope.launch {
            when (val result = apiCall(errorMapper) { api.sendChat(streamId, SendChatRequest(text = text)) }) {
                is AppResult.Success -> _state.update {
                    it.copy(sending = false, chat = it.chat.withSent(result.data))
                }
                // Give the words back unless the viewer already started a new message.
                is AppResult.Failure -> _state.update {
                    it.copy(
                        sending = false,
                        draft = it.draft.ifEmpty { text },
                        notice = chatSendRefusal(result.error),
                    )
                }
            }
        }
    }

    fun onNoticeShown() {
        _state.update { it.copy(notice = null) }
    }

    // ── Moderating as a viewer ─────────────────────────────────────────

    /** Long press on a message: the report sheet, or the moderator's menu when there is more than Report. */
    fun onMessageLongPress(message: LiveChatMessageDto) {
        if (message.id.isBlank()) return
        val current = _state.value
        if (viewerMessageActions(message, current.hostId, current.canModerate).size > 1) {
            _state.update { it.copy(selected = message) }
        } else {
            onReportMessage(message)
        }
    }

    fun onDismissMessage() {
        _state.update { it.copy(selected = null) }
    }

    fun onViewerAction(message: LiveChatMessageDto, action: ViewerMessageAction) {
        _state.update { it.copy(selected = null) }
        when (action) {
            ViewerMessageAction.Report -> onReportMessage(message)
            ViewerMessageAction.RemoveMessage -> onRemoveMessage(message.id)
            ViewerMessageAction.Ban -> onBan(message.userId)
        }
    }

    /** A moderator's removal. A 404 means it is already gone: same outcome. */
    private fun onRemoveMessage(messageId: String) {
        if (!_state.value.canModerate || messageId.isBlank()) return
        viewModelScope.launch {
            val result = noContentApiCall(errorMapper) { api.removeChatMessage(streamId, messageId) }
            if (result is AppResult.Failure && result.error !is AppError.NotFound) {
                _state.update { it.copy(notice = moderationFailure(result.error)) }
            } else {
                _state.update { it.copy(chat = it.chat.withRemoved(messageId)) }
            }
        }
    }

    private fun onBan(userId: String) {
        val current = _state.value
        if (!current.canModerate || userId.isBlank() || userId == current.hostId) return
        viewModelScope.launch {
            val request = BanUserRequest(userId = userId)
            when (val result = noContentApiCall(errorMapper) { api.banUser(streamId, request) }) {
                is AppResult.Success -> _state.update { it.copy(notice = "Banned from this stream.") }
                is AppResult.Failure -> _state.update { it.copy(notice = moderationFailure(result.error)) }
            }
        }
    }

    // ── Report ─────────────────────────────────────────────────────────

    fun onReportStream() {
        _state.update { it.copy(reportTarget = ReportTarget.Stream, report = UsPostReportState.Idle) }
    }

    fun onReportMessage(message: LiveChatMessageDto) {
        if (message.id.isBlank()) return
        _state.update { it.copy(reportTarget = ReportTarget.Message(message), report = UsPostReportState.Idle) }
    }

    fun onDismissReport() {
        _state.update { it.copy(reportTarget = null, report = UsPostReportState.Idle) }
    }

    fun onSubmitReport(reason: LiveReportReason, note: String) {
        val current = _state.value
        val target = current.reportTarget ?: return
        if (current.report == UsPostReportState.Sending) return
        val request = LiveReportRequest(
            reason = reason.wire,
            messageId = (target as? ReportTarget.Message)?.message?.id,
            note = note.trim().takeIf { it.isNotEmpty() },
        )
        _state.update { it.copy(report = UsPostReportState.Sending) }
        viewModelScope.launch {
            val outcome = when (val result = noContentApiCall(errorMapper) { api.report(streamId, request) }) {
                is AppResult.Success -> UsPostReportState.Sent
                is AppResult.Failure -> reportStateFor(result.error)
            }
            _state.update { it.copy(report = outcome) }
        }
    }

    private fun teardown() {
        signalsJob?.cancel()
        signalsJob = null
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
        const val JOIN_FAILED_COPY = "Couldn't join the stream."
        const val CONNECTION_LOST_COPY = "Lost the connection to this stream."
    }
}
