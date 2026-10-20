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
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.DELETE_SCOPE_EVERYONE
import com.us.android.core.chat.data.DisappearTimer
import com.us.android.core.chat.data.DELETE_SCOPE_ME
import com.us.android.core.chat.data.ForwardOutcome
import com.us.android.core.chat.data.GifInfo
import com.us.android.core.chat.data.MAX_FORWARD_TARGETS
import com.us.android.core.chat.data.MessageReportReason
import com.us.android.core.chat.data.StickerInfo
import com.us.android.core.chat.data.StickerPack
import com.us.android.core.chat.data.TypedSend
import com.us.android.core.chat.data.downsampleWaveform
import com.us.android.core.chat.data.forwardSummary
import com.us.android.core.chat.data.isMissingRoute
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import kotlinx.coroutines.flow.first
import com.us.android.core.chat.data.DurableSendResult
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.PendingSend
import com.us.android.core.chat.data.ReplyRef
import com.us.android.core.chat.data.TYPING_TTL_MILLIS
import com.us.android.core.chat.data.ThreadController
import com.us.android.core.chat.data.ThreadUiState
import com.us.android.core.chat.data.isValidMessage
import com.us.android.core.chat.data.sendDurably
import com.us.android.core.common.chat.ConversationKindness
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
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
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
    /**
     * Kind messages ([ConversationKindness]): the send is held because the text
     * might come across as unkind. The viewer chooses Edit or Send anyway.
     */
    val kindPrompt: Boolean = false,
    /** Kind messages: received messages covered until tapped, by message id. */
    val covers: Map<String, KindCover> = emptyMap(),
    // ── Chat parity (2026-10-18) ────────────────────────────────────────
    /** The loaded conversation (peer, streak, mute switches). */
    val conversation: Conversation? = null,
    /** The direct peer's presence, from the row and then `presence_update` frames. */
    val peerOnline: Boolean? = null,
    val peerLastActiveAt: String? = null,
    /** `chat_bubble_style`, the viewer's own bubble colour; null = default. */
    val bubbleStyle: String? = null,
    /** `dm_sensitive_media_filter`. */
    val sensitiveFilter: Boolean = false,
    /** Sensitive media the viewer tapped to see. */
    val revealed: Set<String> = emptySet(),
    val stickerPacks: List<StickerPack> = emptyList(),
    val intro: ThreadIntro? = null,
    val followingPeer: Boolean = false,
    val reactionSheet: ReactionSheetState? = null,
    val sheet: MessageSheet? = null,
    /** Group "Seen by N" for the viewer's newest message. */
    val seenByCount: Int? = null,
    /** A quote tapped: the list scrolls to this message once it is loaded. */
    val jumpToId: String? = null,
    val voiceSending: Boolean = false,
    val notice: UsMessage? = null,
    /** False in a build without the parity seam (tests): every new affordance hides. */
    val parityEnabled: Boolean = false,
    /** The chat's disappearing-messages timer; null until the settings read answered. */
    val disappearing: DisappearTimer? = null,
) {
    /** Send is offered for text OR photos — a photo alone is a message. */
    val canSend: Boolean
        get() = (thread.canSend || staged.isNotEmpty()) && !sendInFlight && !thread.draftTooLong
}

/**
 * True when [message] could be the server's copy of this still-sending
 * outbox row: same kind and the same content. A parked (failed) row never
 * matches — it is not on its way, so nothing is waiting for its echo.
 */
internal fun PendingSend.couldBe(message: Message): Boolean {
    if (failed) return false
    val kind = typed?.type
    val media = mediaId
    return when {
        kind != null -> kind == message.type
        media != null -> media == message.mediaId
        else -> message.mediaId == null && text.trim() == message.text.trim()
    }
}

/**
 * A received message the conversation's owner asked to cover (kind messages).
 * [revealed]: tapped to read. [answered]: "Did this bother you?" was answered
 * Yes. [offerReport]: the owner then offered the report flow for the sender.
 */
data class KindCover(
    val revealed: Boolean = false,
    val answered: Boolean = false,
    val offerReport: Boolean = false,
)

/**
 * One photo waiting on the composer.
 *
 * [failed] keeps a photo whose upload was refused ON the composer rather
 * than dropping it — the user picked it, so it stays until they send it or
 * remove it. In-flight state is screen-wide (sendInFlight), drawn as ONE
 * indicator, not one ring per photo.
 */
data class StagedAttachment(
    val uri: Uri,
    val failed: Boolean = false,
    /** Chat parity: a video from the gallery (photos and videos both stage). */
    val isVideo: Boolean = false,
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
    // Kind messages: a product's check over ITS conversations (a Pulse chat);
    // nothing to do for every other conversation.
    private val kindness: ConversationKindness = ConversationKindness.None,
    // Chat parity (2026-10-18): null in tests, which keeps the thread as it was.
    private val extras: ThreadExtras? = null,
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

    /**
     * The viewer's own messages echoed by the socket while the outbox row
     * that sent them is still on screen, by message id. Declared before
     * init, whose collectors use it.
     *
     * The server echoes every send to the sender, often before the HTTP
     * response. Applied at once, the echo drew a second bubble beside the
     * still-sending one, and it is the copy WITHOUT the server address. Held
     * here instead, it is released when its row leaves the outbox — after the
     * confirmed copy is merged.
     */
    private val heldEchoes = linkedMapOf<String, Message>()

    /** True once the conversation's owner said it checks this conversation's messages. Declared before init, which sets it. */
    private var kindChecks = false

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
        if (extras != null) {
            _state.update { it.copy(parityEnabled = true) }
            loadParity(extras)
        }
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
     *
     * In a conversation with kind-message checks the text is asked about
     * first; "might be unkind" holds the send for Edit / Send anyway, and
     * anything else — including any failure — sends it.
     */
    fun send() {
        val current = _state.value
        if (current.sendInFlight || current.kindPrompt) return
        val text = current.thread.draft.trim()
        val checkable = text.isNotEmpty() && (current.staged.isNotEmpty() || text.isValidMessage())
        if (!kindChecks || !checkable) {
            sendNow()
            return
        }
        _state.update { it.copy(sendInFlight = true) }
        viewModelScope.launch {
            val unkind = try {
                kindness.mightBeUnkind(conversationId, text)
            } finally {
                _state.update { it.copy(sendInFlight = false) }
            }
            if (unkind) _state.update { it.copy(kindPrompt = true) } else sendNow()
        }
    }

    /** "Send anyway": the held message goes as it is. */
    fun sendAnyway() {
        if (!_state.value.kindPrompt) return
        _state.update { it.copy(kindPrompt = false) }
        sendNow()
    }

    /** "Edit": the held message stays in the composer, unsent. */
    fun editHeldMessage() = _state.update { it.copy(kindPrompt = false) }

    /** Tapped to read a covered message. */
    fun revealCovered(messageId: String) = _state.update { s ->
        val cover = s.covers[messageId] ?: return@update s
        s.copy(covers = s.covers + (messageId to cover.copy(revealed = true)))
    }

    /**
     * "Did this bother you?" about a covered message. No uncovers it at once;
     * Yes keeps it covered and, when the owner offers it, the report flow.
     */
    fun answerBothered(messageId: String, bothered: Boolean) {
        val cover = _state.value.covers[messageId] ?: return
        if (cover.answered) return
        _state.update { s ->
            if (bothered) {
                s.copy(covers = s.covers + (messageId to cover.copy(answered = true)))
            } else {
                s.copy(covers = s.covers - messageId)
            }
        }
        viewModelScope.launch {
            val offerReport = kindness.bothered(conversationId, messageId, bothered)
            if (bothered && offerReport) {
                _state.update { s ->
                    val now = s.covers[messageId] ?: return@update s
                    s.copy(covers = s.covers + (messageId to now.copy(offerReport = true)))
                }
            }
        }
    }

    /**
     * Each text someone else sent is asked about once; a "cover" answer covers
     * it. Only for a conversation whose owner checks it.
     */
    private fun observeKindness() = viewModelScope.launch {
        if (!kindness.appliesTo(conversationId)) return@launch
        kindChecks = true
        val asked = mutableSetOf<String>()
        _state.map { it.thread.messages }.distinctUntilChanged().collect { messages ->
            for (message in messages) {
                val theirs = message.senderId.isNotBlank() && message.senderId != viewerId
                if (!theirs || message.pending || message.text.isBlank() || !asked.add(message.id)) continue
                if (kindness.shouldCover(conversationId, message.id, message.text)) {
                    val answered = kindness.alreadyAnswered(conversationId, message.id)
                    _state.update { it.copy(covers = it.covers + (message.id to KindCover(answered = answered))) }
                }
            }
        }
    }

    private fun sendNow() {
        if (_state.value.sendInFlight) return
        stopTyping()
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
                staged = _state.value.staged.map { it.copy(failed = false) },
            )
            try {
                var captionUsed = false
                val refused = mutableListOf<Uri>()
                for (item in _state.value.staged) {
                    // Lease per photo (F2-LB-1): an upload that finishes
                    // after logout must not enqueue into the next session.
                    val lease = store.acquireWriteLease() ?: break
                    val uploaded = attachmentUploader.uploadPhotoOrVideo(item.uri)
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
                .map { it.copy(failed = true) }
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
                staged = state.staged + added.map { StagedAttachment(uri = it, isVideo = isVideoUri(it)) },
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

    private var lastTypingStartMillis = 0L
    private var typingStopJob: Job? = null

    /**
     * Typing (CONTRACT §4): one `typing.start` per [TYPING_RESEND_MILLIS]
     * while the draft keeps changing, a `typing.stop` after
     * [TYPING_IDLE_MILLIS] of quiet or on send. The socket frame goes first;
     * REST is the fallback when there is no live room. Server-side the
     * sender's privacy toggle decides whether anything is published.
     */
    fun sendTyping() {
        val now = System.currentTimeMillis()
        if (now - lastTypingStartMillis >= TYPING_RESEND_MILLIS) {
            lastTypingStartMillis = now
            if (!session.sendTyping(conversationId, typing = true)) {
                viewModelScope.launch { repository.setTyping(conversationId, typing = true) }
            }
        }
        typingStopJob?.cancel()
        typingStopJob = viewModelScope.launch {
            delay(TYPING_IDLE_MILLIS)
            stopTyping()
        }
    }

    private fun stopTyping() {
        if (lastTypingStartMillis == 0L) return
        lastTypingStartMillis = 0L
        typingStopJob?.cancel()
        if (!session.sendTyping(conversationId, typing = false)) {
            viewModelScope.launch { repository.setTyping(conversationId, typing = false) }
        }
    }

    private fun isVideoUri(uri: Uri): Boolean =
        runCatching { attachmentUploader.mimeOf(uri) }.getOrNull()?.startsWith("video/") == true

    // ── Chat parity (2026-10-18) ────────────────────────────────────────

    private fun loadParity(extras: ThreadExtras) {
        viewModelScope.launch {
            val settings = extras.privacy.get()
            if (settings is AppResult.Success) {
                _state.update {
                    it.copy(
                        bubbleStyle = settings.data.chatBubbleStyle,
                        sensitiveFilter = settings.data.dmSensitiveMediaFilter == true,
                    )
                }
            }
        }
        viewModelScope.launch {
            // The disappearing timer: one read seeds it, and the shared map
            // carries any change saved on the chat settings page while this
            // thread stays open beneath it.
            extras.parity.prefs(conversationId)
            extras.parity.disappearing
                .map { it[conversationId] }
                .distinctUntilChanged()
                .collect { timer -> if (timer != null) _state.update { it.copy(disappearing = timer) } }
        }
        viewModelScope.launch {
            val packs = extras.parity.stickers()
            if (packs is AppResult.Success) _state.update { it.copy(stickerPacks = packs.data) }
        }
        viewModelScope.launch {
            // The intro card and Follow need the peer, which the roster read names.
            _state.map { it.peerUserId }.distinctUntilChanged().collect { peer ->
                if (peer.isNotBlank()) loadIntro(extras, peer)
            }
        }
        viewModelScope.launch {
            _state.map { s -> s.thread.messages.firstOrNull { it.senderId == viewerId && !it.pending }?.id }
                .distinctUntilChanged()
                .collect { newestOwn -> if (newestOwn != null && _state.value.loadedIsGroup) loadSeenCount(extras, newestOwn) }
        }
        viewModelScope.launch {
            extras.followGraph.edges.collect { edges ->
                val peer = _state.value.peerUserId
                if (peer.isNotBlank()) {
                    _state.update {
                        it.copy(followingPeer = edges[peer] == com.us.android.core.model.FollowStatus.FOLLOWING)
                    }
                }
            }
        }
    }

    private suspend fun loadIntro(extras: ThreadExtras, peerId: String) {
        val conversation = _state.value.conversation
        val peer = conversation?.peer
        val profile = (extras.profiles.getProfile(peerId) as? AppResult.Success)?.data
        _state.update {
            it.copy(
                intro = ThreadIntro(
                    userId = peerId,
                    name = profile?.displayName?.ifBlank { null } ?: peer?.displayName?.ifBlank { null } ?: it.loadedTitle,
                    username = profile?.username?.ifBlank { null } ?: peer?.username.orEmpty(),
                    avatarUrl = peer?.avatarUrl,
                    followers = profile?.counts?.followers,
                    posts = profile?.counts?.posts,
                ),
            )
        }
        extras.followGraph.ensureKnown(listOf(peerId))
    }

    private suspend fun loadSeenCount(extras: ThreadExtras, messageId: String) {
        val summary = extras.parity.readSummary(conversationId, messageId)
        if (summary is AppResult.Success) _state.update { it.copy(seenByCount = summary.data.count) }
    }

    fun followPeer() {
        val extras = extras ?: return
        val peer = _state.value.peerUserId.takeIf { it.isNotBlank() } ?: return
        viewModelScope.launch {
            if (extras.followGraph.follow(peer) is AppResult.Failure) notice("Couldn't follow right now.")
        }
    }

    fun revealSensitive(messageId: String) = _state.update { it.copy(revealed = it.revealed + messageId) }

    fun dismissNotice() = _state.update { it.copy(notice = null) }

    private fun notice(text: String, type: UsMessageType = UsMessageType.Error) =
        _state.update { it.copy(notice = UsMessage(text, type)) }

    /** A recorded voice message: upload as audio, then the durable outbox (`type: voice`). */
    fun sendVoice(file: java.io.File, durationMs: Long, amplitudes: List<Int>) {
        if (durationMs < MIN_VOICE_MILLIS) {
            file.delete()
            notice("Hold to record a voice message.", UsMessageType.Info)
            return
        }
        val replyTo = _state.value.replyingTo?.toReplyRef()
        _state.update { it.copy(voiceSending = true) }
        viewModelScope.launch {
            try {
                val lease = store.acquireWriteLease() ?: return@launch
                when (val uploaded = attachmentUploader.uploadVoice(file)) {
                    is AppResult.Success -> {
                        val queued = store.enqueueTypedSend(
                            conversationId,
                            TypedSend.voice(uploaded.data, durationMs, downsampleWaveform(amplitudes), replyTo),
                            lease,
                        )
                        if (queued == null) notice("Chat is finishing a security cleanup. Try again.")
                        else _state.update { it.copy(replyingTo = null) }
                    }
                    is AppResult.Failure -> notice("Your voice message couldn't be sent.")
                }
            } finally {
                file.delete()
                _state.update { it.copy(voiceSending = false) }
            }
        }
    }

    fun sendSticker(sticker: StickerInfo) = sendTyped(TypedSend.sticker(sticker))

    /** A GIF picked from Giphy's SDK: its id and url go EXACTLY as the SDK returned them. */
    fun sendGif(gif: GifInfo) = sendTyped(TypedSend.gif(gif))

    private fun sendTyped(typed: TypedSend) = viewModelScope.launch {
        if (store.enqueueTypedSend(conversationId, typed) == null) {
            notice("Chat is finishing a security cleanup. Try again.")
        }
    }

    /** Unsend (CONTRACT §2.3 `everyone`): the author's, or a group owner/admin moderating. */
    fun unsend(messageId: String) = deleteWithScope(messageId, DELETE_SCOPE_EVERYONE)

    /** Delete for me (`me`): anyone, for their own view only. */
    fun deleteForMe(messageId: String) = deleteWithScope(messageId, DELETE_SCOPE_ME)

    private fun deleteWithScope(messageId: String, scope: String) = viewModelScope.launch {
        when (val result = controller.deleteMessage(messageId, scope)) {
            is AppResult.Success -> store.removeCachedMessage(messageId)
            is AppResult.Failure -> notice(
                if (scope == DELETE_SCOPE_EVERYONE) "Couldn't unsend that message." else "Couldn't delete that message.",
            )
            null -> Unit
        }
        _state.update { it.copy(thread = controller.snapshot()) }
    }

    fun openReactions(messageId: String) {
        val extras = extras ?: return
        _state.update { it.copy(reactionSheet = ReactionSheetState(messageId)) }
        viewModelScope.launch {
            val result = extras.parity.reactions(conversationId, messageId)
            _state.update { s ->
                if (s.reactionSheet?.messageId != messageId) return@update s
                s.copy(
                    reactionSheet = when (result) {
                        is AppResult.Success -> ReactionSheetState(messageId, result.data)
                        is AppResult.Failure -> ReactionSheetState(messageId, failed = true)
                    },
                )
            }
        }
    }

    fun closeReactions() = _state.update { it.copy(reactionSheet = null) }

    fun closeSheet() = _state.update { it.copy(sheet = null) }

    fun openSeenBy() {
        val extras = extras ?: return
        val newestOwn = _state.value.thread.messages.firstOrNull { it.senderId == viewerId && !it.pending } ?: return
        _state.update { it.copy(sheet = MessageSheet.SeenBy()) }
        viewModelScope.launch {
            val result = extras.parity.readSummary(conversationId, newestOwn.id)
            _state.update { s ->
                if (s.sheet !is MessageSheet.SeenBy) return@update s
                s.copy(
                    sheet = when (result) {
                        is AppResult.Success -> MessageSheet.SeenBy(result.data)
                        is AppResult.Failure -> MessageSheet.SeenBy(failed = true)
                    },
                )
            }
        }
    }

    fun openForward(message: Message) {
        if (!message.canForward()) return
        _state.update { it.copy(sheet = MessageSheet.Forward(message)) }
        viewModelScope.launch {
            val rows = store.conversationsFlow().first()
            _state.update { s ->
                val sheet = s.sheet as? MessageSheet.Forward ?: return@update s
                s.copy(sheet = sheet.copy(targets = forwardTargets(rows, conversationId)))
            }
        }
    }

    /** Forward to at most five chats; one summary line says how it went. */
    fun forward(targetIds: List<String>) {
        val extras = extras ?: return
        val sheet = _state.value.sheet as? MessageSheet.Forward ?: return
        if (sheet.sending || targetIds.isEmpty()) return
        _state.update { it.copy(sheet = sheet.copy(sending = true)) }
        viewModelScope.launch {
            val result = forwardOnce(extras.parity, conversationId, sheet.message, targetIds.take(MAX_FORWARD_TARGETS))
            _state.update { it.copy(sheet = null) }
            when (result) {
                is AppResult.Success -> {
                    val summary = forwardSummary(result.data)
                    val allSent = result.data.isNotEmpty() && result.data.all { it.outcome == ForwardOutcome.SENT }
                    notice(summary, if (allSent) UsMessageType.Success else UsMessageType.Warning)
                }
                is AppResult.Failure -> notice(
                    if (result.error.isMissingRoute()) "Forwarding isn't available yet." else "Couldn't forward. Try again.",
                )
            }
        }
    }

    fun openReport(message: Message) = _state.update { it.copy(sheet = MessageSheet.Report(message)) }

    fun report(reason: MessageReportReason, note: String?) {
        val extras = extras ?: return
        val sheet = _state.value.sheet as? MessageSheet.Report ?: return
        if (sheet.sending) return
        _state.update { it.copy(sheet = sheet.copy(sending = true)) }
        viewModelScope.launch {
            val result = extras.parity.report(conversationId, sheet.message.id, reason, note)
            _state.update { it.copy(sheet = null) }
            when (result) {
                is AppResult.Success -> notice("Thanks — we'll review it. They won't know it was you.", UsMessageType.Success)
                is AppResult.Failure -> notice("Couldn't send the report. Try again.")
            }
        }
    }

    fun openEdit(message: Message) {
        if (!message.canEdit(viewerId)) return
        _state.update { it.copy(sheet = MessageSheet.Edit(message, message.text)) }
    }

    fun onEditChange(text: String) = _state.update { s ->
        val sheet = s.sheet as? MessageSheet.Edit ?: return@update s
        s.copy(sheet = sheet.copy(text = text))
    }

    fun saveEdit() {
        val extras = extras ?: return
        val sheet = _state.value.sheet as? MessageSheet.Edit ?: return
        val text = sheet.text.trim()
        if (sheet.saving || !text.isValidMessage() || text == sheet.message.text) {
            if (text == sheet.message.text) closeSheet()
            return
        }
        _state.update { it.copy(sheet = sheet.copy(saving = true)) }
        viewModelScope.launch {
            when (val result = extras.parity.edit(conversationId, sheet.message, text)) {
                is AppResult.Success -> {
                    controller.replaceText(sheet.message.id, text)?.let { thread -> _state.update { it.copy(thread = thread) } }
                    _state.update { it.copy(sheet = null) }
                }
                is AppResult.Failure -> {
                    _state.update { it.copy(sheet = sheet.copy(saving = false)) }
                    notice("Couldn't edit that message.")
                }
            }
        }
    }

    /**
     * Tapping a quote: scroll to the original. Older history is read page by
     * page (at most [JUMP_PAGES]) until it is found; a note reply has no
     * original in the thread and does nothing.
     */
    fun jumpTo(messageId: String?) {
        if (messageId.isNullOrBlank()) return
        viewModelScope.launch {
            var pages = 0
            while (controller.snapshot().messages.none { it.id == messageId } && controller.snapshot().canLoadMore &&
                pages < JUMP_PAGES
            ) {
                val lease = store.acquireWriteLease() ?: break
                store.cacheMessages(controller.loadMore().messages, lease)
                pages++
            }
            val found = controller.snapshot().messages.any { it.id == messageId }
            _state.update { it.copy(thread = controller.snapshot(), jumpToId = if (found) messageId else null) }
            if (!found) notice("That message is too far back to show.", UsMessageType.Info)
        }
    }

    fun consumeJump() = _state.update { it.copy(jumpToId = null) }

    private fun loadMembers() = viewModelScope.launch {
        when (val result = repository.conversation(conversationId)) {
            is AppResult.Success -> _state.value = _state.value.copy(
                conversation = result.data,
                peerOnline = result.data.peer?.isOnline ?: _state.value.peerOnline,
                peerLastActiveAt = result.data.peer?.lastActiveAt ?: _state.value.peerLastActiveAt,
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
            ).also {
                // Kind messages: the server names the conversation's product
                // (a Pulse match's chat) — told before the owner is asked.
                kindness.conversationSource(conversationId, result.data.sourceApp, result.data.matchId)
            }
            is AppResult.Failure -> Unit
        }
        observeKindness()
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
                is ChatSocketEvent.MessageReceived -> if (isEchoOfInFlightSend(event.message)) {
                    // Our own send coming back before its outbox row cleared:
                    // held, so it never draws a second bubble (see heldEchoes).
                    heldEchoes[event.message.id] = event.message
                    store.clearUnread(conversationId)
                } else {
                    applySocketEvent(event)
                }
                else -> applySocketEvent(event)
            }
        }
    }

    private suspend fun applySocketEvent(event: ChatSocketEvent) {
        // The shared disappearing timer changed: through the parity map, so
        // this header and a settings page over it both follow.
        if (event is ChatSocketEvent.DisappearingTimerUpdated && event.conversationId == conversationId) {
            extras?.parity?.applyRemoteDisappearing(event.conversationId, event.disappearAfterMs)
        }
        controller.onSocketEvent(event)?.let {
            _state.value = _state.value.copy(thread = it)
        }
        if (event is ChatSocketEvent.Typing && event.isTyping &&
            event.conversationId == conversationId
        ) {
            restartTypingTimer(event.userId)
        }
        // Chat parity: the header's "Active now" follows the peer live.
        if (event is ChatSocketEvent.PresenceUpdate && event.userId == _state.value.peerUserId) {
            _state.update {
                it.copy(peerOnline = event.isOnline, peerLastActiveAt = event.lastActiveAt ?: it.peerLastActiveAt)
            }
        }
        if (event is ChatSocketEvent.MessageReceived &&
            event.message.conversationId == conversationId
        ) {
            // Reading an open thread marks it read.
            controller.markRead()
            store.clearUnread(conversationId)
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
            // Echoes held back while their send was in flight go in AFTER the
            // confirmed copy, so they are duplicates by id and add nothing.
            releaseHeldEchoes(rows)
            // ONE emission: the outbox row leaves in the same frame the
            // confirmed bubble arrives — no gap, no double.
            _state.value = _state.value.copy(thread = controller.snapshot(), pendingSends = rows)
        }
    }

    /**
     * Merges the confirmed server copies the outbox wrote to Room. They carry
     * the server address (bucket, ts), so the bubble offers Edit, Delete for
     * me and Unsend at once; a row the socket echo already put on screen
     * adopts that address in place ([ThreadController.onRealtimeMessage]).
     *
     * Oldest first: each unknown row is prepended, so walking the newest-first
     * cache read in order would turn a burst of deliveries upside down.
     */
    private suspend fun mergeConfirmed() {
        store.recentCachedMessages(conversationId, limit = MERGE_WINDOW).asReversed().forEach { cached: Message ->
            controller.onRealtimeMessage(cached)
        }
    }

    /** True when [message] is the socket echo of a send this thread still shows as sending. */
    private fun isEchoOfInFlightSend(message: Message): Boolean =
        message.conversationId == conversationId &&
            viewerId.isNotBlank() &&
            message.senderId == viewerId &&
            controller.snapshot().messages.none { it.id == message.id } &&
            _state.value.pendingSends.any { it.couldBe(message) }

    private fun releaseHeldEchoes(rows: List<PendingSend>) {
        if (heldEchoes.isEmpty()) return
        val released = heldEchoes.values.filter { held -> rows.none { it.couldBe(held) } }
        released.forEach { held ->
            heldEchoes.remove(held.id)
            controller.onRealtimeMessage(held)
        }
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
        const val TYPING_RESEND_MILLIS = 2_500L
        const val TYPING_IDLE_MILLIS = 4_000L
        const val JUMP_PAGES = 5

        /** Shorter than this is a tap, not a recording. */
        const val MIN_VOICE_MILLIS = 800L
    }
}
