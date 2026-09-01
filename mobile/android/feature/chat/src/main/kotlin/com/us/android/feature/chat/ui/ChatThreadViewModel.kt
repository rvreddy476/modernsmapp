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
import com.us.android.core.chat.data.ReplyRef
import com.us.android.core.chat.data.TYPING_TTL_MILLIS
import com.us.android.core.chat.data.ThreadController
import com.us.android.core.chat.data.ThreadUiState
import com.us.android.core.chat.data.isValidMessage
import com.us.android.core.chat.data.sendDurably
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
import kotlinx.coroutines.flow.update
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
    /**
     * Photos CHOSEN but not yet sent.
     *
     * Picking used to upload immediately and queue the message on the spot,
     * so there was no moment between "I tapped a photo" and "it is gone".
     * Nothing leaves the device until Send, which is what makes picking
     * several and changing your mind possible.
     */
    val staged: List<StagedAttachment> = emptyList(),
    val attachmentError: String? = null,
    /** The message the next send answers; a banner on the composer until sent. */
    val replyingTo: Message? = null,
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
) {
    /** Send is offered for text OR photos — a photo alone is a message. */
    val canSend: Boolean
        get() = (thread.canSend || staged.isNotEmpty()) && !sendInFlight && !thread.draftTooLong
}

/**
 * One photo waiting on the composer.
 *
 * [uploading] drives the ring drawn over its thumbnail. [failed] keeps a
 * photo whose upload was refused ON the composer rather than dropping it —
 * the user picked it, so it stays until they send it or remove it.
 */
data class StagedAttachment(
    val uri: Uri,
    val uploading: Boolean = false,
    val failed: Boolean = false,
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
        if (_state.value.staged.isNotEmpty()) {
            sendStagedAttachments()
            return
        }
        val draftAtSend = _state.value.thread.draft
        val text = draftAtSend.trim()
        if (!text.isValidMessage()) return
        val replyTo = _state.value.replyingTo?.toReplyRef()
        _state.value = _state.value.copy(sendInFlight = true)
        viewModelScope.launch {
            try {
                when (store.sendDurably(conversationId, text, replyTo)) {
                    is DurableSendResult.Queued -> {
                        val current = _state.value
                        _state.value = if (current.thread.draft == draftAtSend) {
                            current.copy(
                                thread = controller.onDraftChange(""),
                                sendUnavailable = false,
                                replyingTo = null,
                            )
                        } else {
                            // The user typed while the enqueue was in
                            // flight: the queued revision is on its way,
                            // the NEWER draft is preserved untouched.
                            current.copy(sendUnavailable = false, replyingTo = null)
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
     * Send, for a composer carrying photos: upload each, then queue it.
     *
     * The caption rides the FIRST photo rather than becoming a seventh
     * message of its own, which is what a caption means to the person
     * writing it. A photo whose upload is refused STAYS on the composer
     * marked failed — the others still go, and the user keeps the one that
     * did not so they can retry it rather than re-picking from the gallery.
     */
    private fun sendStagedAttachments() {
        attachmentJob?.cancel()
        attachmentJob = viewModelScope.launch {
            val draftAtSend = _state.value.thread.draft
            val caption = draftAtSend.trim()
            val replyTo = _state.value.replyingTo?.toReplyRef()
            _state.value = _state.value.copy(
                sendInFlight = true,
                attachmentError = null,
                staged = _state.value.staged.map { it.copy(uploading = true, failed = false) },
            )
            try {
                var captionUsed = false
                val refused = mutableListOf<Uri>()
                for (item in _state.value.staged) {
                    // Lease per photo (F2-LB-1): an upload that finishes
                    // after logout must not enqueue into the next session.
                    val lease = store.acquireWriteLease() ?: break
                    val uploaded = attachmentUploader.uploadImage(item.uri)
                    if (uploaded is AppResult.Success) {
                        val queued = store.enqueueSend(
                            conversationId,
                            text = if (captionUsed) "" else caption,
                            mediaId = uploaded.data,
                            // The quote rides the first photo, same as the
                            // caption — one reply, not one per photo.
                            replyTo = if (captionUsed) null else replyTo,
                            lease = lease,
                        )
                        if (queued == null) refused += item.uri else captionUsed = true
                    } else {
                        refused += item.uri
                    }
                }
                applyAttachmentOutcome(refused, captionUsed, draftAtSend)
            } finally {
                _state.value = _state.value.copy(sendInFlight = false)
            }
        }
    }

    /** Keeps the refused photos staged and clears the draft only if it was sent. */
    private fun applyAttachmentOutcome(
        refused: List<Uri>,
        captionUsed: Boolean,
        draftAtSend: String,
    ) {
        _state.update { state ->
            val remaining = state.staged
                .filter { it.uri in refused }
                .map { it.copy(uploading = false, failed = true) }
            state.copy(
                staged = remaining,
                replyingTo = if (captionUsed) null else state.replyingTo,
                attachmentError = if (remaining.isEmpty()) {
                    null
                } else {
                    "${remaining.size} photo(s) couldn't be sent. Tap send to try again."
                },
                // The draft goes only when it actually rode a photo out, and
                // only if the user has not typed something newer since.
                thread = if (captionUsed && state.thread.draft == draftAtSend) {
                    controller.onDraftChange("")
                } else {
                    state.thread
                },
            )
        }
    }

    /** Arms the composer to answer [message]; the banner shows until sent. */
    fun startReply(message: Message) = _state.update { it.copy(replyingTo = message) }

    fun cancelReply() = _state.update { it.copy(replyingTo = null) }

    /**
     * The wire quote for a message: its id plus the display snapshot. A photo
     * with no caption quotes as "Photo" — the preview must say something.
     */
    private fun Message.toReplyRef() = ReplyRef(
        messageId = id,
        preview = text.ifBlank { if (mediaId != null) "Photo" else "" },
        senderId = senderId,
    )

    /** The roster name for a quoted author — "You" when it is the viewer. */
    fun quoteAuthorName(userId: String?): String = when {
        userId.isNullOrBlank() -> ""
        userId == viewerId -> "You"
        else -> controller.memberName(userId).orEmpty()
    }

    /**
     * Puts picked photos ON the composer. NOTHING is uploaded here.
     *
     * Picking used to upload and queue in one motion, so a mis-tap was
     * already sent. Staging separates choosing from sending: the user can
     * pick several, drop one, type a caption, and only then commit.
     */
    fun stageAttachments(uris: List<Uri>) {
        if (uris.isEmpty()) return
        _state.update { state ->
            val existing = state.staged.map { it.uri }.toSet()
            val room = MAX_ATTACHMENTS - state.staged.size
            val added = uris.filterNot { it in existing }.take(room.coerceAtLeast(0))
            state.copy(
                staged = state.staged + added.map { StagedAttachment(uri = it) },
                attachmentError = if (added.size < uris.filterNot { it in existing }.size) {
                    "You can attach up to $MAX_ATTACHMENTS photos at once."
                } else {
                    null
                },
            )
        }
    }

    /** Takes one photo back off the composer before it is sent. */
    fun unstageAttachment(uri: Uri) = _state.update { state ->
        state.copy(staged = state.staged.filterNot { it.uri == uri }, attachmentError = null)
    }

    fun dismissAttachmentError() = _state.update { it.copy(attachmentError = null) }

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

    internal companion object {
        const val CONVERSATION_ID_KEY = "conversationId"
        const val MERGE_WINDOW = 10
        const val MAX_ATTACHMENTS = 10
    }
}
