package com.us.android.feature.live.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.offersFollow
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.WebLinks
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.network.noContentApiCall
import com.us.android.core.ui.UsPostReportState
import com.us.android.feature.live.data.BanUserRequest
import com.us.android.feature.live.data.CODE_CHAT_BANNED
import com.us.android.feature.live.data.CODE_CHAT_MUTED
import com.us.android.feature.live.data.ChatLog
import com.us.android.feature.live.data.EndedReason
import com.us.android.feature.live.data.HEART_FLUSH_MILLIS
import com.us.android.feature.live.data.HeartBlock
import com.us.android.feature.live.data.HeartsRequest
import com.us.android.feature.live.data.LiveApi
import com.us.android.feature.live.data.LiveChatAuthorDto
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.LiveCreatorCardDto
import com.us.android.feature.live.data.LiveFollows
import com.us.android.feature.live.data.LiveFrame
import com.us.android.feature.live.data.LiveJoin
import com.us.android.feature.live.data.LiveReportReason
import com.us.android.feature.live.data.LiveReportRequest
import com.us.android.feature.live.data.LiveRoomFactory
import com.us.android.feature.live.data.LiveRoomSession
import com.us.android.feature.live.data.LiveRoomSignal
import com.us.android.feature.live.data.LiveRoomSocket
import com.us.android.feature.live.data.LiveSocketEvent
import com.us.android.feature.live.data.LiveStatus
import com.us.android.feature.live.data.LiveStreamDto
import com.us.android.feature.live.data.SendChatRequest
import com.us.android.feature.live.data.ViewerMessageAction
import com.us.android.feature.live.data.addJoin
import com.us.android.feature.live.data.canSendChat
import com.us.android.feature.live.data.clampChatDraft
import com.us.android.feature.live.data.endedReasonOf
import com.us.android.feature.live.data.heartBlockFor
import com.us.android.feature.live.data.heartTotalAfterFrame
import com.us.android.feature.live.data.heartsBatch
import com.us.android.feature.live.data.heartsOpen
import com.us.android.feature.live.data.joinTime
import com.us.android.feature.live.data.liveCode
import com.us.android.feature.live.data.liveSocketRetryMillis
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
import kotlinx.coroutines.flow.transformWhile
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The pinned chat message, as the bar over the chat shows it. */
data class LivePinned(
    val messageId: String,
    val userId: String,
    val text: String,
    val author: LiveChatAuthorDto?,
)

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
 * T-30 (2026-10-15): the room's realtime frames arrive over ws-gateway
 * ([LiveRoomSocket]) ON TOP of the poll, which stays the truth. The frames
 * add what has no HTTP read — "<name> joined" system rows (`viewer.joined`,
 * never stored, the viewer's own skipped) — and make chat, removals, the
 * pinned message, mute and hearts arrive at once. Refused or dropped, the
 * screen simply keeps polling (and polls the pinned message too).
 *
 * A viewer the host made a moderator gets `moderator_user_ids` on the
 * stream (the server shows it to the host and moderators only); that
 * presence unlocks Remove message and Ban user on their long-press menu.
 *
 * Viewers comment and send free hearts; there are no gifts, coins or guests.
 */
@Suppress("TooManyFunctions", "LargeClass") // one public entry per viewer control
@HiltViewModel
class LiveWatchViewModel @Inject constructor(
    private val api: LiveApi,
    private val errorMapper: ErrorMapper,
    private val roomFactory: LiveRoomFactory,
    private val roomSocket: LiveRoomSocket,
    private val follows: LiveFollows,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    /** What a report is about: the whole stream, or one chat message. */
    sealed interface ReportTarget {
        data object Stream : ReportTarget
        data class Message(val message: LiveChatMessageDto) : ReportTarget
    }

    data class UiState(
        val streamId: String = "",
        /** The signed-in viewer; blank when unknown. */
        val meId: String = "",
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
        /** Join notices from `viewer.joined`: client-side system rows only. */
        val joins: List<LiveJoin> = emptyList(),
        val pinned: LivePinned? = null,
        /** The host or a moderator muted this viewer: the composer gives way to a notice. */
        val muted: Boolean = false,
        /** This viewer was banned from the chat. */
        val chatBanned: Boolean = false,
        val draft: String = "",
        val sending: Boolean = false,
        val notice: String? = null,
        val reportTarget: ReportTarget? = null,
        val report: UsPostReportState = UsPostReportState.Idle,
        val hostId: String = "",
        /** The host card from the stream row, when the server sent one. */
        val host: LiveCreatorCardDto? = null,
        /** The viewer's edge toward the host; null while unknown. */
        val hostEdge: FollowStatus? = null,
        val followBusy: Boolean = false,
        /** The server listed this stream's moderators for us: we are one of them. */
        val canModerate: Boolean = false,
        /** The stream's moderators, when the server lists them for us; marks their chat rows. */
        val moderators: List<String> = emptyList(),
        /** The message whose moderator menu is open. */
        val selected: LiveChatMessageDto? = null,
        /** The room socket is open and subscribed: frames are flowing. */
        val roomLive: Boolean = false,
        /** The stream's free-heart total as the server last said. */
        val heartTotal: Long = 0,
        val heartsQueued: Int = 0,
        val heartsInFlight: Int = 0,
        val heartBlock: HeartBlock? = null,
    ) {
        val canChat: Boolean get() = joinError == null && !loading && !status.isOver

        /** The composer is drawn; a muted or chat-banned viewer reads why instead. */
        val showsComposer: Boolean get() = canChat && !muted && !chatBanned

        /** The viewer is the host, watching their own stream from the list. */
        val isHost: Boolean get() = meId.isNotBlank() && meId == hostId

        /** Follow the host: never for the host, and only once the answer is known to be "not following". */
        val showsFollow: Boolean get() = !isHost && offersFollow(meId, hostId, hostEdge)

        /** Share the room's web address: viewers only. */
        val showsShare: Boolean get() = !isHost && hostId.isNotBlank() && WebLinks.liveUrl(streamId) != null

        /** Hearts are a viewer's reaction while the stream is on air. */
        val showsHearts: Boolean get() = !isHost && !loading && joinError == null && heartsOpen(status)

        /** What the heart button shows: the server's total plus own taps it has not counted yet. */
        val heartCount: Long get() = heartTotal + heartsQueued + heartsInFlight
    }

    private val streamId: String = savedStateHandle.get<String>("streamId").orEmpty()

    private val _state = MutableStateFlow(UiState(streamId = streamId, meId = follows.ownId))
    val state: StateFlow<UiState> = _state.asStateFlow()

    private var session: LiveRoomSession? = null
    private var signalsJob: Job? = null
    private var pollJob: Job? = null
    private var roomFeedJob: Job? = null
    private var heartJob: Job? = null

    /** The LiveKit room, for rendering. */
    val room: Room? get() = session?.room

    /** The broadcaster's video, once subscribed. */
    val remoteVideo: VideoTrack? get() = session?.remoteVideo()

    init {
        viewModelScope.launch {
            follows.edges.collect { edges -> _state.update { it.copy(hostEdge = edges[it.hostId]) } }
        }
        viewModelScope.launch { join() }
    }

    /** "Try again" after a failed join or a lost connection. */
    fun onRetry() {
        pollJob?.cancel()
        roomFeedJob?.cancel()
        teardown()
        viewModelScope.launch { join() }
    }

    private suspend fun join() {
        _state.update { it.copy(loading = true, joinError = null, hasVideo = false, meId = follows.ownId) }
        val stream = when (val result = apiCall(errorMapper) { api.getStream(streamId) }) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> return failJoin(watchJoinFailure(result.error))
        }
        applyStream(stream)
        learnHostEdge()
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
        refreshPinned()
        startRoomFeed()
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
                    roomFeedJob?.cancel()
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
                if (!_state.value.status.isOver) {
                    refreshChat()
                    // Frames carry pin and unpin; without them, read the pin.
                    if (!_state.value.roomLive) refreshPinned()
                }
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

    /** `GET …/chat/pinned`: `data: null` is "nothing pinned"; a failed read keeps what is shown. */
    @Suppress("TooGenericExceptionCaught", "SwallowedException") // apiCall reads a null `data` as malformed
    private suspend fun refreshPinned() {
        val envelope = try {
            api.pinnedMessage(streamId)
        } catch (e: CancellationException) {
            throw e
        } catch (ignored: Exception) {
            return
        }
        if (envelope.error != null) return
        val row = envelope.data
        _state.update { current ->
            current.copy(
                pinned = row?.takeIf { it.id.isNotBlank() && it.id !in current.chat.removed }?.let { message ->
                    LivePinned(message.id, message.userId, message.text, message.author)
                },
            )
        }
    }

    private fun applyStream(stream: LiveStreamDto) {
        val status = liveStatusOf(stream.status)
        _state.update {
            val hostId = stream.creatorUserId.ifBlank { stream.creator?.userId.orEmpty() }.ifBlank { it.hostId }
            it.copy(
                title = stream.title.ifBlank { it.title },
                status = status,
                endedReason = endedReasonOf(stream.endedReason),
                viewerCount = stream.viewerCount,
                hostId = hostId,
                host = stream.creator ?: it.host,
                hostEdge = follows.edges.value[hostId],
                canModerate = stream.moderatorUserIds != null,
                moderators = stream.moderatorUserIds.orEmpty(),
                heartTotal = maxOf(it.heartTotal, stream.heartCount),
                // Back on air: hearts may be sent again.
                heartBlock = it.heartBlock.takeUnless { block -> block == HeartBlock.NotLive && heartsOpen(status) },
            )
        }
        if (status.isOver) stopEverything()
    }

    private fun stopEverything() {
        pollJob?.cancel()
        roomFeedJob?.cancel()
        teardown()
        _state.update { it.copy(hasVideo = false, roomLive = false) }
    }

    /** Asks the graph whether the viewer follows the host; Follow appears only once that is known. */
    private suspend fun learnHostEdge() {
        val current = _state.value
        if (current.hostId.isBlank() || current.isHost || current.meId.isBlank()) return
        follows.ensureKnown(listOf(current.hostId))
        _state.update { it.copy(hostEdge = follows.edges.value[it.hostId]) }
    }

    // ── The room's realtime frames ─────────────────────────────────────

    /**
     * Opens the room socket and keeps it open while the stream is on: a
     * dropped socket is reopened after [liveSocketRetryMillis]; a refused
     * room, a missing session or a refused credential ends it for this visit.
     */
    private fun startRoomFeed() {
        roomFeedJob?.cancel()
        roomFeedJob = viewModelScope.launch {
            var attempt = 0
            while (isActive && !_state.value.status.isOver) {
                var stop = false
                roomSocket.room(streamId)
                    .transformWhile { event ->
                        emit(event)
                        event !is LiveSocketEvent.Closed &&
                            !(event is LiveSocketEvent.Frame && event.frame is LiveFrame.Refused)
                    }
                    .collect { event ->
                        when (event) {
                            LiveSocketEvent.Open -> {
                                attempt = 0
                                _state.update { it.copy(roomLive = true) }
                            }
                            is LiveSocketEvent.Frame ->
                                if (event.frame is LiveFrame.Refused) stop = true else onFrame(event.frame)
                            is LiveSocketEvent.Closed -> stop = event.permanent
                        }
                    }
                _state.update { it.copy(roomLive = false) }
                if (stop) break
                attempt++
                delay(liveSocketRetryMillis(attempt))
            }
        }
    }

    /** One room frame. Frames for another stream never reach here (the socket filters them). */
    @Suppress("CyclomaticComplexMethod")
    internal fun onFrame(frame: LiveFrame) {
        if (frame.streamId != streamId) return
        val me = _state.value.meId
        when (frame) {
            is LiveFrame.Status -> applyStatusFrame(frame)
            is LiveFrame.Chat -> _state.update { it.copy(chat = it.chat.withSent(frame.message)) }
            is LiveFrame.Removed -> _state.update {
                it.copy(
                    chat = it.chat.withRemoved(frame.messageId),
                    pinned = it.pinned.takeUnless { pin -> pin?.messageId == frame.messageId },
                )
            }
            is LiveFrame.Viewers -> _state.update { it.copy(viewerCount = frame.viewerCount) }
            is LiveFrame.Joined -> onJoined(frame)
            is LiveFrame.Muted -> if (frame.userId == me) _state.update { it.copy(muted = true) }
            is LiveFrame.Unmuted -> if (frame.userId == me) _state.update { it.copy(muted = false) }
            is LiveFrame.Pinned -> _state.update {
                it.copy(pinned = LivePinned(frame.messageId, frame.userId, frame.text, authorFor(it, frame)))
            }
            is LiveFrame.Unpinned -> _state.update {
                it.copy(pinned = it.pinned.takeUnless { pin -> pin?.messageId == frame.messageId })
            }
            is LiveFrame.Banned -> if (frame.userId == me) _state.update { it.copy(chatBanned = true) }
            is LiveFrame.Unbanned -> if (frame.userId == me) _state.update { it.copy(chatBanned = false) }
            is LiveFrame.Moderators -> _state.update {
                if (me.isNotBlank() && me in frame.userIds) {
                    it.copy(canModerate = true, moderators = frame.userIds)
                } else if (it.canModerate) {
                    it.copy(moderators = frame.userIds)
                } else {
                    it
                }
            }
            is LiveFrame.Hearts -> _state.update { it.copy(heartTotal = heartTotalAfterFrame(it.heartTotal, frame)) }
            is LiveFrame.Refused -> Unit
        }
    }

    private fun applyStatusFrame(frame: LiveFrame.Status) {
        val status = liveStatusOf(frame.status)
        _state.update {
            it.copy(
                status = status,
                endedReason = endedReasonOf(frame.endedReason),
                viewerCount = frame.viewerCount ?: it.viewerCount,
            )
        }
        if (status.isOver) stopEverything()
    }

    /** A join row: never the viewer's own, never the host's, once per person per burst. */
    private fun onJoined(frame: LiveFrame.Joined) {
        _state.update {
            if (frame.userId == it.meId || frame.userId == it.hostId) return@update it
            val join = LiveJoin(frame.userId, frame.author, joinTime(frame.joinedAt, System.currentTimeMillis()))
            it.copy(joins = addJoin(it.joins, join))
        }
    }

    /** `moderation.pin` has no author card: the pinned row's, else any row by the same person. */
    private fun authorFor(state: UiState, frame: LiveFrame.Pinned): LiveChatAuthorDto? =
        state.chat.messages.firstOrNull { it.id == frame.messageId }?.author
            ?: state.chat.messages.firstOrNull { it.userId == frame.userId && it.author != null }?.author

    // ── Chat ───────────────────────────────────────────────────────────

    /** Held to what the server accepts, counted the way it counts: code points, never half an emoji. */
    fun onDraftChanged(draft: String) {
        _state.update { it.copy(draft = clampChatDraft(draft)) }
    }

    fun onSendChat() {
        val current = _state.value
        val text = current.draft.trim()
        if (!canSendChat(text) || current.sending || !current.showsComposer) return
        _state.update { it.copy(draft = "", sending = true) }
        viewModelScope.launch {
            when (val result = apiCall(errorMapper) { api.sendChat(streamId, SendChatRequest(text = text)) }) {
                is AppResult.Success -> _state.update {
                    it.copy(sending = false, chat = it.chat.withSent(result.data))
                }
                // Give the words back unless the viewer already started a new
                // message: a blocked word is edited, not retyped.
                is AppResult.Failure -> _state.update {
                    val code = result.error.liveCode()
                    it.copy(
                        sending = false,
                        draft = it.draft.ifEmpty { text },
                        notice = chatSendRefusal(result.error),
                        muted = it.muted || code == CODE_CHAT_MUTED,
                        chatBanned = it.chatBanned || code == CODE_CHAT_BANNED,
                    )
                }
            }
        }
    }

    fun onNoticeShown() {
        _state.update { it.copy(notice = null) }
    }

    // ── Follow and hearts ──────────────────────────────────────────────

    /** Follow the host through the shared graph. The edge flips at once and is put back on a refusal. */
    fun onFollowHost() {
        val current = _state.value
        if (!current.showsFollow || current.followBusy) return
        _state.update { it.copy(followBusy = true) }
        viewModelScope.launch {
            val result = follows.follow(current.hostId)
            _state.update {
                it.copy(
                    followBusy = false,
                    hostEdge = follows.edges.value[it.hostId],
                    notice = if (result is AppResult.Failure) FOLLOW_FAILED_COPY else it.notice,
                )
            }
        }
    }

    /** One tap on the heart. Taps are gathered for [HEART_FLUSH_MILLIS] and sent at most 20 at a time. */
    fun onHeart() {
        val current = _state.value
        if (!current.showsHearts || current.heartBlock != null) return
        _state.update { it.copy(heartsQueued = it.heartsQueued + 1) }
        scheduleHeartFlush()
    }

    private fun scheduleHeartFlush() {
        if (heartJob?.isActive == true) return
        heartJob = viewModelScope.launch {
            delay(HEART_FLUSH_MILLIS)
            flushHearts()
        }
    }

    private suspend fun flushHearts() {
        val batch = heartsBatch(_state.value.heartsQueued)
        if (batch == 0) return
        _state.update { it.copy(heartsQueued = it.heartsQueued - batch, heartsInFlight = batch) }
        val result = apiCall(errorMapper) { api.sendHearts(streamId, HeartsRequest(count = batch)) }
        _state.update {
            when (result) {
                is AppResult.Success -> it.copy(
                    heartsInFlight = 0,
                    heartTotal = if (result.data.heartCount > 0) {
                        maxOf(it.heartTotal, result.data.heartCount)
                    } else {
                        it.heartTotal + batch
                    },
                )
                // The batch is gone either way: never re-sent, so a refusal cannot loop.
                is AppResult.Failure -> {
                    val block = heartBlockFor(result.error)
                    if (block != null) {
                        it.copy(heartsInFlight = 0, heartsQueued = 0, heartBlock = block)
                    } else {
                        it.copy(heartsInFlight = 0)
                    }
                }
            }
        }
        if (_state.value.heartsQueued > 0) {
            delay(HEART_FLUSH_MILLIS)
            flushHearts()
        }
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
        roomFeedJob?.cancel()
        heartJob?.cancel()
        teardown()
    }

    internal companion object {
        /** Status, viewer count and chat, together. */
        const val POLL_MILLIS = 3_000L
        const val JOIN_FAILED_COPY = "Couldn't join the stream."
        const val CONNECTION_LOST_COPY = "Lost the connection to this stream."
        const val FOLLOW_FAILED_COPY = "Couldn't follow. Try again."
    }
}
