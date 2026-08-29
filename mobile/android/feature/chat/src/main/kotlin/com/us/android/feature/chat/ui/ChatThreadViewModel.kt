package com.us.android.feature.chat.ui

import android.net.Uri
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.DurableSendResult
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.PendingSend
import com.us.android.core.chat.data.TYPING_TTL_MILLIS
import com.us.android.core.chat.data.ThreadController
import com.us.android.core.chat.data.ThreadUiState
import com.us.android.core.chat.data.isValidMessage
import com.us.android.core.chat.data.sendDurably
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.ChatAttachmentUploader
import com.us.android.core.model.SessionState
import com.us.android.core.notifications.NotificationPresenter
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The screen state: the controller's thread plus the DURABLE outbox rows.
 *
 * Pending rows come from Room, not from an in-memory list — that is the whole
 * point. A send the process died on is still here when the process returns,
 * still ordered, still carrying its original idempotency key (CH-LB-4.4).
 */
data class ThreadRenderState(
    val thread: ThreadUiState,
    val pendingSends: List<PendingSend> = emptyList(),
    val offline: Boolean = false,
    /** Non-null while an attachment is uploading; renders a progress row. */
    val attachmentUploading: Boolean = false,
    val attachmentError: String? = null,
    /** 0..100 while an attachment PUT is in flight. */
    val attachmentProgressPercent: Int = 0,
    /** The signed-in user — own messages align right and carry send state. */
    val viewerId: String = "",
    /** The loaded conversation's display title (deep links arrive without one). */
    val loadedTitle: String = "",
    /** True when the loaded conversation is a group. */
    val loadedIsGroup: Boolean = false,
    /**
     * The OTHER member of a direct conversation — who the call buttons ring.
     * Blank for groups and until the roster loads. Display only ever comes
     * from the roster; whether the peer may actually be called is the
     * server's decision at create time.
     */
    val peerUserId: String = "",
    /**
     * True when the last send was REFUSED because chat is unavailable (an
     * owed security cleanup is still being repaid). The draft is retained;
     * the user retries by tapping Send again.
     */
    val sendUnavailable: Boolean = false,
    /**
     * True while ONE send is being durably enqueued. The Send button is
     * disabled for its duration — a second tap must not create a second
     * outbox row for the same text.
     */
    val sendInFlight: Boolean = false,
)

@HiltViewModel
// Constructor injection of the thread's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class ChatThreadViewModel @Inject constructor(
    private val repository: ChatRepository,
    private val store: ChatStore,
    private val session: ChatSessionManager,
    private val attachmentUploader: ChatAttachmentUploader,
    private val notificationPresenter: NotificationPresenter,
    sessionState: SessionStateProvider,
    savedStateHandle: SavedStateHandle,
) : ViewModel() {

    private val conversationId: String =
        savedStateHandle.get<String>(CONVERSATION_ID_KEY).orEmpty()

    private val viewerId: String =
        (sessionState.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    private val controller = ThreadController(conversationId, repository, viewerId)

    private val _state = MutableStateFlow(
        ThreadRenderState(controller.snapshot(), viewerId = viewerId),
    )
    val state: StateFlow<ThreadRenderState> = _state.asStateFlow()

    private val typingTimers = mutableMapOf<String, Job>()
    private var attachmentJob: Job? = null

    init {
        // ONE session socket for the whole app (CH-LB-4.1); this screen only
        // collects. start() is idempotent, so calling it defensively here
        // covers cold process starts straight into a deep-linked thread.
        session.start()
        // Scaled room path: an OPEN thread subscribes its conversation room
        // with an owner-issued entitlement; the personal channel remains the
        // delivery guarantee, the room is the acceleration.
        session.subscribeRoom(conversationId)
        // A handled conversation's notification leaves the shade.
        notificationPresenter.cancelForSubject(conversationId)
        loadMembers()
        refresh()
        observeSession()
        observePendingSends()
        viewModelScope.launch { store.clearUnread(conversationId) }
    }

    override fun onCleared() {
        session.unsubscribeRoom(conversationId)
        super.onCleared()
    }

    fun refresh() = viewModelScope.launch {
        // Lease BEFORE the fetch (F2-LB-1): a history response that lands
        // after logout must not cache old-account plaintext.
        val lease = store.acquireWriteLease() ?: return@launch
        val refreshed = controller.refresh()
        if (refreshed.refreshError != null && refreshed.messages.isEmpty()) {
            // Offline fallback: the cached tail renders instead of a blank
            // error screen; the banner says why it may be stale.
            val cached = store.recentCachedMessages(conversationId)
            if (cached.isNotEmpty()) {
                cached.forEach { controller.onRealtimeMessage(it) }
                _state.value = _state.value.copy(
                    thread = controller.snapshot(),
                    offline = true,
                )
                return@launch
            }
        }
        store.cacheMessages(refreshed.messages, lease)
        // The controller's CURRENT state, not the snapshot captured before
        // these awaits: a draft typed while the fetch was in flight must
        // survive the write-back (same draft-loss family as the send path).
        _state.value = _state.value.copy(thread = controller.snapshot(), offline = false)
        controller.markRead()
        store.clearUnread(conversationId)
    }

    fun loadMore() = viewModelScope.launch {
        val lease = store.acquireWriteLease() ?: return@launch
        val next = controller.loadMore()
        store.cacheMessages(next.messages, lease)
        _state.value = _state.value.copy(thread = controller.snapshot())
    }

    fun onDraftChange(text: String) {
        _state.value = _state.value.copy(thread = controller.onDraftChange(text))
    }

    /**
     * The DURABLE send: outbox row first, network second.
     *
     * Two rules the review's adverse interleavings demanded (F2-LB-3):
     *
     *  - ONE send at a time. A second tap while the enqueue awaits Room is
     *    a no-op (and the button is disabled via [ThreadRenderState
     *    .sendInFlight]) — otherwise each tap minted a fresh idempotency
     *    key and the same text was delivered twice.
     *  - Clear only the EXACT draft revision that was queued. If the user
     *    typed while the enqueue was in flight, the newer draft stays —
     *    the composer never erases text that was not sent.
     */
    fun send() {
        if (_state.value.sendInFlight) return
        val draftAtSend = _state.value.thread.draft
        val text = draftAtSend.trim()
        if (!text.isValidMessage()) return
        _state.value = _state.value.copy(sendInFlight = true)
        viewModelScope.launch {
            try {
                when (store.sendDurably(conversationId, text)) {
                    is DurableSendResult.Queued -> {
                        val current = _state.value
                        _state.value = if (current.thread.draft == draftAtSend) {
                            current.copy(
                                thread = controller.onDraftChange(""),
                                sendUnavailable = false,
                            )
                        } else {
                            // The user typed while the enqueue was in
                            // flight: the queued revision is on its way,
                            // the NEWER draft is preserved untouched.
                            current.copy(sendUnavailable = false)
                        }
                    }
                    DurableSendResult.ChatUnavailable -> _state.value = _state.value.copy(
                        sendUnavailable = true,
                    )
                }
            } finally {
                _state.value = _state.value.copy(sendInFlight = false)
            }
        }
    }

    fun retrySend(idempotencyKey: String) = viewModelScope.launch {
        store.retrySend(idempotencyKey)
    }

    fun abandonSend(idempotencyKey: String) = viewModelScope.launch {
        store.abandonSend(idempotencyKey)
    }

    /**
     * Uploads a picked image through the media authority (reserve → PUT →
     * confirm → ready+passed) and queues a durable MEDIA send. Progress and
     * cancellation ride the job: [cancelAttachment] aborts before the message
     * ever references the asset, so nothing dangles server-side.
     */
    fun sendAttachment(uri: Uri) {
        attachmentJob?.cancel()
        attachmentJob = viewModelScope.launch {
            // Lease BEFORE the upload (F2-LB-1): an upload finishing after
            // logout must not enqueue an old-account media message into the
            // next session's outbox.
            val lease = store.acquireWriteLease() ?: return@launch
            _state.value = _state.value.copy(
                attachmentUploading = true,
                attachmentError = null,
                attachmentProgressPercent = 0,
            )
            val result = attachmentUploader.uploadImage(uri) { sent, total ->
                if (total > 0) {
                    _state.value = _state.value.copy(
                        attachmentProgressPercent = ((sent * PERCENT) / total).toInt(),
                    )
                }
            }
            when (result) {
                is AppResult.Success -> {
                    val key = store.enqueueSend(
                        conversationId,
                        text = "",
                        mediaId = result.data,
                        lease = lease,
                    )
                    _state.value = _state.value.copy(
                        attachmentUploading = false,
                        // A refused enqueue (logout raced the upload) must
                        // not pretend the photo was queued.
                        attachmentError = if (key == null) {
                            "The photo couldn't be queued. Try again."
                        } else {
                            null
                        },
                    )
                }
                is AppResult.Failure -> _state.value = _state.value.copy(
                    attachmentUploading = false,
                    attachmentError = (result.error as? AppError.InvalidRequest)?.message
                        ?: "The photo couldn't be uploaded. Try again.",
                )
            }
        }
    }

    /** Cancels an in-flight attachment upload. */
    fun cancelAttachment() {
        attachmentJob?.cancel()
        attachmentJob = null
        _state.value = _state.value.copy(
            attachmentUploading = false,
            attachmentProgressPercent = 0,
        )
    }

    /** Toggles the viewer's [emoji] reaction on [messageId]. */
    fun toggleReaction(messageId: String, emoji: String) = viewModelScope.launch {
        _state.value = _state.value.copy(thread = controller.toggleReaction(messageId, emoji))
    }

    /** Deletes one message (the server enforces who may) and drops the cache row. */
    fun deleteMessage(messageId: String) = viewModelScope.launch {
        if (controller.deleteMessage(messageId) is AppResult.Success) {
            store.removeCachedMessage(messageId)
        }
        _state.value = _state.value.copy(thread = controller.snapshot())
    }

    fun sendTyping() = viewModelScope.launch {
        // Server-side the sender's privacy toggle decides whether anything is
        // published; a denial is silent by design.
        repository.setTyping(conversationId, typing = true)
    }

    private fun loadMembers() = viewModelScope.launch {
        when (val result = repository.conversation(conversationId)) {
            is AppResult.Success -> _state.value = _state.value.copy(
                thread = controller.setMembers(result.data.members),
                // Deep links navigate with a blank title; the loaded
                // conversation supplies the real one.
                loadedTitle = result.data.displayTitle(viewerId),
                loadedIsGroup = result.data.type == "group",
                peerUserId = if (result.data.type == "group") {
                    ""
                } else {
                    result.data.members.firstOrNull { it.userId != viewerId }?.userId.orEmpty()
                },
            )
            is AppResult.Failure -> Unit
        }
    }

    private fun observeSession() = viewModelScope.launch {
        session.events.collect { event ->
            when (event) {
                is ChatSocketEvent.Connected -> {
                    // Reconnect reconciliation: HTTP repairs whatever the
                    // socket missed; the controller de-duplicates by id.
                    // Same lease-before-fetch rule as refresh().
                    val lease = store.acquireWriteLease()
                    if (lease != null) {
                        val refreshed = controller.refresh()
                        store.cacheMessages(refreshed.messages, lease)
                        _state.value = _state.value.copy(
                            thread = controller.snapshot(),
                            offline = false,
                        )
                    }
                }
                is ChatSocketEvent.Disconnected ->
                    _state.value = _state.value.copy(offline = true)
                else -> {
                    controller.onSocketEvent(event)?.let {
                        _state.value = _state.value.copy(thread = it)
                    }
                    if (event is ChatSocketEvent.Typing && event.isTyping &&
                        event.conversationId == conversationId
                    ) {
                        restartTypingTimer(event.userId)
                    }
                    if (event is ChatSocketEvent.MessageReceived &&
                        event.message.conversationId == conversationId
                    ) {
                        // Reading an open thread marks it read.
                        controller.markRead()
                        store.clearUnread(conversationId)
                    }
                }
            }
        }
    }

    /**
     * Watches the outbox. When a row completes, its confirmed server message
     * is already in the Room cache — merge any cached rows the controller
     * does not know yet, so the sent message appears exactly once.
     */
    private fun observePendingSends() = viewModelScope.launch {
        var previous = emptySet<String>()
        store.pendingSendsFor(conversationId).collect { rows ->
            val current = rows.mapTo(mutableSetOf()) { it.idempotencyKey }
            val completed = previous - current
            if (completed.isNotEmpty()) {
                mergeConfirmed()
            }
            previous = current
            _state.value = _state.value.copy(pendingSends = rows)
        }
    }

    private suspend fun mergeConfirmed() {
        store.recentCachedMessages(conversationId, limit = MERGE_WINDOW).forEach { cached: Message ->
            controller.onRealtimeMessage(cached)
        }
        _state.value = _state.value.copy(thread = controller.snapshot())
    }

    private fun restartTypingTimer(userId: String) {
        typingTimers.remove(userId)?.cancel()
        typingTimers[userId] = viewModelScope.launch {
            delay(TYPING_TTL_MILLIS)
            controller.onTypingStopped(userId)?.let {
                _state.value = _state.value.copy(thread = it)
            }
            typingTimers.remove(userId)
        }
    }

    private companion object {
        const val CONVERSATION_ID_KEY = "conversationId"
        const val MERGE_WINDOW = 10
        const val PERCENT = 100L
    }
}
