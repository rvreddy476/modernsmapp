package com.us.android.core.chat.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult

/** Everything one chat thread renders. */
data class ThreadUiState(
    val conversationId: String,
    val messages: List<Message> = emptyList(),
    val loading: Boolean = false,
    val appending: Boolean = false,
    val refreshError: AppError? = null,
    val appendError: AppError? = null,
    val sendError: AppError? = null,
    val sending: Boolean = false,
    val draft: String = "",
    val nextCursor: String? = null,
    /**
     * Who is currently typing, excluding the viewer.
     *
     * A set rather than a boolean because a group can have two people typing
     * and one of them stopping must not clear the indicator for the other.
     */
    val typingUserIds: Set<String> = emptySet(),
    /**
     * The newest of the viewer's messages the DIRECT peer has read, from the
     * privacy-gated `read_receipt` frame. Null until one arrives — the UI
     * shows nothing rather than guessing, because absence of a receipt is
     * exactly what a `no_one` receipts setting looks like.
     */
    val peerLastReadMessageId: String? = null,
) {
    val canLoadMore: Boolean get() = nextCursor != null && !appending && appendError == null
    val canSend: Boolean get() = draft.isValidMessage() && !sending

    /** True when the draft is over the server's limit, so the UI can say so. */
    val draftTooLong: Boolean get() = draft.trim().length > MAX_MESSAGE_LENGTH
}

/**
 * Composer validation.
 *
 * Blank-only text is rejected locally because it is not a message. The length
 * cap is the SERVER's, not a guess.
 */
fun String.isValidMessage(): Boolean {
    val trimmed = trim()
    return trimmed.isNotEmpty() && trimmed.length <= MAX_MESSAGE_LENGTH
}

/**
 * The longest message the server will accept.
 *
 * `SendMessageRequest.Text` is bound `max=2000`
 * (`message-service/internal/http/handler.go:552`), and gin answers a longer
 * body with 400 before the handler runs.
 *
 * This was 4,000 — a made-up number, chosen when no server limit had been
 * observed. The cost of guessing high is the worst version of this failure:
 * the composer accepts the text, the user watches it send, and the request is
 * rejected on a validation error with no field name in it. Guessing low would
 * merely have been annoying. Mirror the server or do not cap at all.
 */
const val MAX_MESSAGE_LENGTH = 2_000

/**
 * Loads and sends one conversation's messages.
 *
 * WHY THIS IS NOT CommentsController
 *
 * The two look alike and are not alike. Comments are durable, cursor-paginated
 * records owned by post-service; messages are realtime, socket-delivered and
 * arrive unbidden from other people. Reusing the comments controller would put
 * a live transport behind a pagination API, and every future change to one
 * product would have to be reasoned about for the other.
 *
 * What IS shared is the design system underneath the screens, which is where
 * the architecture decision puts the reuse boundary.
 *
 * Plain class, not a ViewModel: the same logic backs a full-screen thread and,
 * later, a chat tab beside a live video, and a ViewModel would tie it to
 * whichever of those owns the lifecycle.
 */
class ThreadController(
    private val conversationId: String,
    private val repository: ChatRepository,
    /** The signed-in user, for own-message affordances (react/delete/read). */
    val viewerId: String = "",
) {

    private var state = ThreadUiState(conversationId = conversationId)

    /**
     * Sender id to display name, from the conversation's member list.
     *
     * This is the whole of chat's sender attribution, and it is why chat does
     * not have the comment list's author problem. `GET /conversations/{id}`
     * returns every member WITH `display_name`, so one request names every
     * message in the thread — including messages that arrive later over the
     * socket, whose frames carry no name at all, and the viewer's own sends,
     * whose HTTP response omits `sender_display_name` even though the list
     * response includes it.
     *
     * The alternative is a profile request per message, which is the pattern
     * the architecture decision exists to prevent.
     */
    private var memberNames: Map<String, String> = emptyMap()

    /**
     * The key for the message currently being sent, and the exact text it was
     * minted for.
     *
     * Same rule as comments, for the same reason: an unchanged retry must
     * reuse the key so a lost response replays instead of sending twice, and
     * an edited draft must mint a new one so the server cannot answer with the
     * earlier text. Chat makes this load-bearing rather than optional — the
     * server REQUIRES the header.
     */
    private var sendKey: String? = null
    private var sendKeyText: String? = null

    fun snapshot(): ThreadUiState = state

    /**
     * Supplies the member roster used to name senders.
     *
     * Re-applied to messages already loaded, because history usually arrives
     * before the roster does and the rows would otherwise stay anonymous until
     * the next refresh.
     */
    fun setMembers(members: List<ConversationMember>): ThreadUiState {
        memberNames = members
            .filter { it.displayName.isNotBlank() }
            .associate { it.userId to it.displayName }
        return state.copy(messages = state.messages.map { it.named() }).also { state = it }
    }

    suspend fun refresh(): ThreadUiState {
        state = state.copy(loading = true, refreshError = null)
        return when (val result = repository.messages(conversationId)) {
            is AppResult.Success -> state.copy(
                messages = result.data.items.map { it.named() },
                nextCursor = result.data.nextCursor,
                loading = false,
                refreshError = null,
            ).also { state = it }

            is AppResult.Failure -> state.copy(
                loading = false,
                // Messages are deliberately preserved. A failed refresh over a
                // populated thread must not blank someone's conversation.
                refreshError = result.error,
            ).also { state = it }
        }
    }

    /**
     * Loads older history.
     *
     * De-duplicates by id: a message sent while the page boundary is being
     * read can legitimately appear twice, and a keyed list crashes on a
     * duplicate.
     */
    suspend fun loadMore(): ThreadUiState {
        val cursor = state.nextCursor ?: return state
        if (state.appending) return state
        state = state.copy(appending = true, appendError = null)

        return when (val result = repository.messages(conversationId, cursor = cursor)) {
            is AppResult.Success -> {
                val known = state.messages.mapTo(mutableSetOf()) { it.id }
                val older = result.data.items.filterNot { it.id in known }.map { it.named() }
                state.copy(
                    messages = state.messages + older,
                    nextCursor = result.data.nextCursor,
                    appending = false,
                    appendError = null,
                ).also { state = it }
            }

            // Loaded history survives; a retry re-runs the SAME cursor.
            is AppResult.Failure -> state.copy(
                appending = false,
                appendError = result.error,
            ).also { state = it }
        }
    }

    fun onDraftChange(text: String): ThreadUiState =
        state.copy(draft = text, sendError = null).also { state = it }

    /**
     * Sends the draft.
     *
     * The optimistic row is REPLACED by the server's message rather than left
     * in place: the server assigns `msg_id`, and a locally invented id would
     * collide with the real row when history is next loaded.
     */
    suspend fun send(): ThreadUiState {
        val text = state.draft.trim()
        if (!text.isValidMessage() || state.sending) return state

        val key = sendKey?.takeIf { sendKeyText == text }
            ?: ChatRepository.newIdempotencyKey().also {
                sendKey = it
                sendKeyText = text
            }
        val pendingId = "pending:$key"
        state = state.copy(
            sending = true,
            sendError = null,
            messages = listOf(
                Message(
                    id = pendingId,
                    conversationId = conversationId,
                    senderId = "",
                    senderDisplayName = null,
                    text = text,
                    createdAt = "",
                    pending = true,
                ),
            ) + state.messages,
        )

        return when (val result = repository.send(conversationId, text, key)) {
            is AppResult.Success -> {
                sendKey = null
                sendKeyText = null
                // The send response omits sender_display_name while the list
                // response includes it, so without this the message the user
                // just sent is the one row in the thread with no name on it.
                val confirmed = result.data.named()
                state.copy(
                    sending = false,
                    draft = "",
                    sendError = null,
                    // Drop the placeholder AND any row the server already
                    // returned with the same id — a replayed idempotent
                    // response must not appear twice.
                    messages = listOf(confirmed) +
                        state.messages.filterNot { it.id == pendingId || it.id == confirmed.id },
                ).also { state = it }
            }

            is AppResult.Failure -> state.copy(
                sending = false,
                // The draft is put back so retry has something to send and the
                // user does not retype it.
                draft = text,
                sendError = result.error,
                messages = state.messages.filterNot { it.id == pendingId },
            ).also { state = it }
        }
    }

    /**
     * Applies one socket event to this thread.
     *
     * The event-to-state path lives HERE rather than in the ViewModel so it can
     * be tested without Android. That distinction is not cosmetic: the previous
     * implementation's realtime tests called [onRealtimeMessage] directly, the
     * ViewModel never called it, and the two facts together meant a thread that
     * received nothing while every test passed.
     *
     * Returns null when the event changes nothing, so a caller can skip a
     * pointless state emission.
     */
    fun onSocketEvent(event: ChatSocketEvent): ThreadUiState? = when (event) {
        is ChatSocketEvent.MessageReceived -> onRealtimeMessage(event.message)

        is ChatSocketEvent.Typing -> when {
            event.conversationId != conversationId -> null
            event.isTyping -> onTypingStarted(event.userId)
            else -> onTypingStopped(event.userId)
        }

        // The peer read up to a message: server-gated disclosure, rendered
        // only for THIS conversation and only when the identifiers are whole.
        is ChatSocketEvent.ReadReceipt -> when {
            event.conversationId != conversationId -> null
            event.messageId.isBlank() -> null
            else -> state.copy(peerLastReadMessageId = event.messageId).also { state = it }
        }

        // Malformed is listed explicitly rather than swept up by an `else`:
        // an exhaustive `when` is what forces the next person who adds a frame
        // type to decide what a thread does with it, instead of inheriting
        // "ignore" by default. SubscriptionRevoked belongs to the session
        // manager (room bookkeeping); a removed member's next thread action
        // surfaces the server's refusal.
        is ChatSocketEvent.Connected,
        is ChatSocketEvent.Disconnected,
        is ChatSocketEvent.SubscriptionRevoked,
        is ChatSocketEvent.Unknown,
        is ChatSocketEvent.Malformed,
        -> null
    }

    /**
     * Applies a message that arrived over the socket.
     *
     * Three rejections, all load-bearing:
     *
     *  - a message whose conversation is not EXACTLY this one is dropped. One
     *    socket carries every conversation the user belongs to (the gateway
     *    subscribes the connection to `chat:<user_id>`, not to a room), so an
     *    unfiltered thread shows messages from other threads;
     *  - a message missing an id or a sender is dropped. A blank id collides
     *    with every other blank id, which turns de-duplication into silent
     *    message loss and hands a keyed list duplicate keys;
     *  - a message already present is dropped. The server does not echo to the
     *    sender today, so this guards the reconnect case where history and a
     *    live frame overlap.
     *
     * ## WHY THE FIRST CHECK IS `!=` AND NOT `isNotBlank() && !=`
     *
     * It used to be the second form, and that is the whole bug: a frame with a
     * BLANK conversation id skipped the comparison entirely and was accepted
     * into whichever thread was open. Combined with the parser's `.orEmpty()`,
     * any truncated or foreign frame rendered as a message in a conversation
     * it did not belong to.
     *
     * The parser now refuses such frames too ([parseChatFrame] returns
     * [ChatSocketEvent.Malformed]). This check is not redundant with it:
     * [onRealtimeMessage] is public and is also reachable from tests and from
     * any future non-socket source, so the thread defends its own boundary
     * rather than trusting its caller to have done it.
     *
     * Returns null when nothing changed.
     */
    fun onRealtimeMessage(message: Message): ThreadUiState? {
        if (message.conversationId != conversationId) return null
        if (message.id.isBlank() || message.senderId.isBlank()) return null
        if (state.messages.any { it.id == message.id }) return null
        return state.copy(
            messages = listOf(message.named()) + state.messages,
            // Whatever they were typing, they have now sent it.
            typingUserIds = state.typingUserIds - message.senderId,
        ).also { state = it }
    }

    /** Marks [userId] as typing. Returns null when it was already known. */
    fun onTypingStarted(userId: String): ThreadUiState? {
        if (userId.isBlank() || userId in state.typingUserIds) return null
        return state.copy(typingUserIds = state.typingUserIds + userId).also { state = it }
    }

    /**
     * Clears [userId]'s typing indicator.
     *
     * There is no stop frame on the wire — message-service publishes only
     * `is_typing: true` and sets a 3-second Redis key. The caller is therefore
     * expected to call this on [TYPING_TTL_MILLIS] after each start, which is
     * what keeps the indicator from sticking on forever.
     */
    fun onTypingStopped(userId: String): ThreadUiState? {
        if (userId !in state.typingUserIds) return null
        return state.copy(typingUserIds = state.typingUserIds - userId).also { state = it }
    }

    /**
     * Toggles the viewer's [emoji] on one message: server first, then the
     * local summary is updated from the server's added/removed answer. No
     * optimistic write — a reaction the server refused must never linger.
     */
    suspend fun toggleReaction(messageId: String, emoji: String): ThreadUiState {
        val message = state.messages.firstOrNull { it.id == messageId } ?: return state
        if (!message.addressable || viewerId.isBlank()) return state
        return when (val result = repository.toggleReaction(conversationId, message, emoji)) {
            is AppResult.Success -> {
                val updated = message.copy(
                    reactions = message.reactions.applyToggle(
                        emoji = emoji,
                        userId = viewerId,
                        added = result.data.added,
                    ),
                )
                state.copy(
                    messages = state.messages.map { if (it.id == messageId) updated else it },
                ).also { state = it }
            }
            is AppResult.Failure -> state
        }
    }

    /**
     * Deletes one message. The server decides who may (sender, or a group
     * owner/admin moderating); a refusal leaves the row untouched.
     */
    suspend fun deleteMessage(messageId: String): AppResult<Unit>? {
        val message = state.messages.firstOrNull { it.id == messageId } ?: return null
        if (!message.addressable) return null
        val result = repository.deleteMessage(conversationId, message)
        if (result is AppResult.Success) {
            state = state.copy(messages = state.messages.filterNot { it.id == messageId })
        }
        return result
    }

    /** Marks the newest message read; a no-op on an empty thread. */
    suspend fun markRead(): AppResult<Unit>? {
        val newest = state.messages.firstOrNull { !it.pending } ?: return null
        return repository.markRead(conversationId, newest.id)
    }

    /** Fills in a sender name from the member roster when the wire had none. */
    private fun Message.named(): Message =
        if (!senderDisplayName.isNullOrBlank()) {
            this
        } else {
            memberNames[senderId]?.let { copy(senderDisplayName = it) } ?: this
        }
}

/**
 * Applies one confirmed toggle to a reaction-summary list: the viewer joins
 * or leaves the [emoji] group; an emptied group disappears. Top-level so the
 * summary arithmetic is testable without a controller.
 */
fun List<ReactionSummary>.applyToggle(
    emoji: String,
    userId: String,
    added: Boolean,
): List<ReactionSummary> {
    val existing = firstOrNull { it.emoji == emoji }
    return when {
        added && existing == null -> this + ReactionSummary(emoji, listOf(userId))
        added -> map {
            if (it.emoji == emoji && userId !in it.userIds) it.copy(userIds = it.userIds + userId) else it
        }
        existing == null -> this
        else -> mapNotNull {
            if (it.emoji != emoji) {
                it
            } else {
                val remaining = it.userIds - userId
                if (remaining.isEmpty()) null else it.copy(userIds = remaining)
            }
        }
    }
}

/**
 * How long a typing indicator survives without a new frame.
 *
 * Mirrors the server's key TTL — message-service writes `typing:<conv>:<user>`
 * with a 3-second expiry (`message.go:1414`). Holding it longer would show
 * someone typing after the server has already forgotten they were.
 */
const val TYPING_TTL_MILLIS = 3_000L
