package com.us.android.core.feed.ui.share

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.PostShareChats
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.engagement.data.EngagementAction
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.QUOTE_TEXT_MAX
import com.us.android.core.engagement.data.QuoteReposts
import com.us.android.core.engagement.data.REPOST_NOT_ALLOWED
import com.us.android.core.engagement.data.RepostReads
import com.us.android.core.engagement.data.postActionRefusalText
import com.us.android.core.engagement.data.repostedOr
import com.us.android.core.model.SessionState
import com.us.android.core.ui.UsPostShareState
import com.us.android.core.ui.UsQuoteState
import com.us.android.core.ui.UsRepostRow
import com.us.android.core.ui.UsSharePickerState
import com.us.android.core.ui.UsShareTarget
import com.us.android.core.ui.postShareLink
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The share sheet's own state for one post: what the server says of the
 * viewer's repost, and the "Send in Messages" picker. The repost the sheet
 * SHOWS is this server value with the viewer's taps laid over it by
 * [EngagementStore], the same overlay every card and the post page read.
 */
data class PostShareModel(
    val postId: String = "",
    /** The viewer's repost per `GET /v1/posts/:id/repost/me` (the feed's own flag if that failed); null until known. */
    val serverReposted: Boolean? = null,
    val repostBusy: Boolean = false,
    val picking: Boolean = false,
    /** The inbox, read when the picker first opens; null until then. */
    val conversations: List<Conversation>? = null,
    val conversationsFailed: Boolean = false,
    val query: String = "",
    val sending: Set<String> = emptySet(),
    val pickerError: String? = null,
    /** Chat parity (2026-10-18): the chats picked for the one Send, in tap order (≤ [MAX_SHARE_TARGETS]). */
    val selected: List<String> = emptyList(),
    val note: String = "",
    /** The quote step is open (2026-10-24), with what the viewer has written. */
    val quoting: Boolean = false,
    val quoteText: String = "",
    val quoteBusy: Boolean = false,
    val quoteError: String? = null,
    /**
     * The post may be reposted and quoted (founder, 24 Oct 2026): false for a
     * reel, whose Repost and Quote rows are never offered ([allowsRepost]).
     */
    val allowRepost: Boolean = true,
)

/** At most five chats per share (CONTRACT §9), groups included. */
const val MAX_SHARE_TARGETS = 5

/** Selecting toggles; a sixth pick is refused, not swapped in. */
fun List<String>.toggledTarget(id: String, cap: Int = MAX_SHARE_TARGETS): List<String> = when {
    id in this -> this - id
    size >= cap -> this
    else -> this + id
}

/** What the sheet draws for [this] model, for [viewerId], with the repost [overlay] for the post. */
fun PostShareModel.toUiState(viewerId: String, overlay: EngagementOverlay): UsPostShareState = UsPostShareState(
    postId = postId,
    link = postShareLink(postId),
    repost = when (val server = serverReposted?.takeIf { allowRepost }) {
        null -> UsRepostRow.HIDDEN
        else -> if (overlay.repostedOr(server)) UsRepostRow.REMOVE else UsRepostRow.REPOST
    },
    repostBusy = repostBusy,
    // Quote (2026-10-24): offered while the viewer has not reposted it; a repost is one per post.
    canQuote = allowRepost && serverReposted != null && !overlay.repostedOr(serverReposted),
    quote = if (!quoting) null else UsQuoteState(text = quoteText, busy = quoteBusy, error = quoteError, max = QUOTE_TEXT_MAX),
    picker = if (!picking) {
        null
    } else {
        UsSharePickerState(
            query = query,
            targets = conversations?.let { shareTargets(it, viewerId, query) }.orEmpty(),
            loading = conversations == null && !conversationsFailed,
            failed = conversations == null && conversationsFailed,
            sending = sending,
            error = pickerError,
            selected = selected,
            note = note,
            maxSelected = MAX_SHARE_TARGETS,
        )
    },
)

/**
 * A post's Share sheet (2026-10-17): Send in Messages, Copy link, Repost /
 * Remove repost and "More…". One per screen; [open] points it at the post
 * the Share action was tapped on.
 */
@HiltViewModel
class PostShareViewModel @Inject constructor(
    private val chats: PostShareChats,
    private val reposts: RepostReads,
    private val engagement: EngagementStore,
    private val session: SessionStateProvider,
    /** Quote repost (2026-10-24): the post with the viewer's words over it. */
    private val quotes: QuoteReposts,
) : ViewModel() {

    private val _state = MutableStateFlow(PostShareModel())
    val state: StateFlow<PostShareModel> = _state.asStateFlow()

    /** The viewer's taps per post, so the Repost row follows a repost made on a card or the post page. */
    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays

    private val _message = MutableStateFlow<UsMessage?>(null)

    /** "Sent", "Link copied", "Reposted", or why not — read by the host screen's message line. */
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    private val _sent = MutableSharedFlow<String>(extraBufferCapacity = 1)

    /** A post card went out (the conversation's id); the host closes the sheet. */
    val sent: SharedFlow<String> = _sent.asSharedFlow()

    val viewerId: String
        get() = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    /**
     * One send intent per (conversation, post): a retry after a lost answer
     * presents the same key, so message-service replays the card instead of
     * sending a second one. Dropped once the send lands.
     */
    private val sendKeys = mutableMapOf<Pair<String, String>, String>()

    private var repostJob: Job? = null

    /**
     * Points the sheet at [postId], on the main menu. The repost state is
     * read from the server; until it answers the row is withheld, and if it
     * fails the feed's own [feedReposted] stands in.
     */
    fun open(postId: String, feedReposted: Boolean, allowRepost: Boolean = true) {
        val conversations = _state.value.conversations
        _state.value = PostShareModel(postId = postId, conversations = conversations, allowRepost = allowRepost)
        repostJob?.cancel()
        // A reel has no Repost row to fill, so its repost state is never asked for.
        if (!allowRepost) return
        repostJob = viewModelScope.launch {
            val known = (reposts.hasReposted(postId) as? AppResult.Success)?.data ?: feedReposted
            _state.update { if (it.postId == postId) it.copy(serverReposted = known) else it }
        }
    }

    fun openPicker() {
        _state.update { it.copy(picking = true, pickerError = null) }
        if (_state.value.conversations == null) loadConversations()
    }

    fun closePicker() =
        _state.update { it.copy(picking = false, query = "", pickerError = null, selected = emptyList(), note = "") }

    fun toggleTarget(target: UsShareTarget) = _state.update { it.copy(selected = it.selected.toggledTarget(target.id)) }

    fun onNoteChange(note: String) = _state.update { it.copy(note = note.take(NOTE_MAX)) }

    /**
     * One Send for every picked chat: the card goes to each, the note as its
     * caption. Each chat keeps its own idempotency key, so a retry after a
     * partial failure re-sends only what did not land, never a second card.
     */
    fun sendSelected() {
        val current = _state.value
        val postId = current.postId
        val targets = current.selected
        if (postId.isBlank() || targets.isEmpty() || current.sending.isNotEmpty()) return
        val note = current.note.trim()
        _state.update { it.copy(sending = targets.toSet(), pickerError = null) }
        viewModelScope.launch {
            val failed = mutableListOf<String>()
            var lastError: AppError? = null
            for (target in targets) {
                val intent = target to postId
                val key = sendKeys.getOrPut(intent) { ChatRepository.newIdempotencyKey() }
                val result = if (note.isEmpty()) {
                    chats.sendPost(target, postId, key)
                } else {
                    chats.sendPostWithNote(target, postId, key, note)
                }
                when (result) {
                    is AppResult.Success -> sendKeys.remove(intent)
                    is AppResult.Failure -> {
                        failed += target
                        lastError = result.error
                    }
                }
                _state.update { it.copy(sending = it.sending - target) }
            }
            if (failed.isEmpty()) {
                _message.value = UsMessage(SENT, UsMessageType.Success)
                _state.update { it.copy(selected = emptyList(), note = "") }
                _sent.tryEmit(targets.last())
            } else {
                // The ones that went are dropped from the selection; what is left is what failed.
                _state.update {
                    it.copy(
                        selected = failed,
                        pickerError = lastError?.let(::sendPostErrorText) ?: "Couldn't send the post. Try again.",
                    )
                }
            }
        }
    }

    fun onQueryChange(query: String) = _state.update { it.copy(query = query) }

    fun retryConversations() = loadConversations()

    fun send(target: UsShareTarget) {
        val postId = _state.value.postId
        if (postId.isBlank() || target.id in _state.value.sending) return
        val intent = target.id to postId
        val key = sendKeys.getOrPut(intent) { ChatRepository.newIdempotencyKey() }
        _state.update { it.copy(sending = it.sending + target.id, pickerError = null) }
        viewModelScope.launch {
            when (val result = chats.sendPost(target.id, postId, key)) {
                is AppResult.Success -> {
                    sendKeys.remove(intent)
                    _state.update { it.copy(sending = it.sending - target.id) }
                    _message.value = UsMessage(SENT, UsMessageType.Success)
                    _sent.tryEmit(target.id)
                }
                is AppResult.Failure -> _state.update {
                    it.copy(sending = it.sending - target.id, pickerError = sendPostErrorText(result.error))
                }
            }
        }
    }

    /**
     * Repost or remove it, through [EngagementStore] — optimistic, rolled
     * back if the server says no — then one line saying what happened.
     */
    fun toggleRepost() {
        val current = _state.value
        if (!current.allowRepost) return
        val server = current.serverReposted ?: return
        if (current.repostBusy) return
        val postId = current.postId
        val wanted = !engagement.overlayFor(postId).repostedOr(server)
        _state.update { it.copy(repostBusy = true) }
        viewModelScope.launch {
            engagement.toggleRepost(postId, server)
            val failure = engagement.failures.value.firstOrNull { it.postId == postId && it.action == EngagementAction.REPOST }
            _message.value = if (failure != null) {
                // Said here, so no other screen raises a second banner for it.
                engagement.clearFailure(postId, EngagementAction.REPOST)
                UsMessage(
                    postActionRefusalText(failure.error) ?: if (wanted) REPOST_FAILED else UNREPOST_FAILED,
                    UsMessageType.Error,
                )
            } else {
                UsMessage(if (wanted) REPOSTED else UNREPOSTED, UsMessageType.Success)
            }
            _state.update { if (it.postId == postId) it.copy(repostBusy = false) else it }
        }
    }

    // ── Quote repost (2026-10-24) ───────────────────────────────────────

    fun openQuote() = _state.update { if (!it.allowRepost) it else it.copy(quoting = true, quoteError = null) }

    fun closeQuote() = _state.update { it.copy(quoting = false, quoteText = "", quoteError = null) }

    fun onQuoteChange(text: String) = _state.update { it.copy(quoteText = text, quoteError = null) }

    /**
     * Posts the quote. Checked first — words are required, 500 at most — then
     * WAITS for the server: the words are the viewer's, and a refusal must
     * leave them in the field to fix rather than vanish with the sheet.
     */
    fun sendQuote() {
        val current = _state.value
        if (current.quoteBusy) return
        quoteProblem(current.quoteText)?.let { problem ->
            _state.update { it.copy(quoteError = problem) }
            return
        }
        val postId = current.postId
        _state.update { it.copy(quoteBusy = true, quoteError = null) }
        viewModelScope.launch {
            when (val result = quotes.quote(postId, current.quoteText)) {
                is AppResult.Success -> {
                    _state.update {
                        if (it.postId != postId) it else it.copy(quoting = false, quoteText = "", quoteBusy = false, serverReposted = true)
                    }
                    _message.value = UsMessage(QUOTED, UsMessageType.Success)
                }
                is AppResult.Failure -> _state.update {
                    if (it.postId != postId) it else it.copy(quoteBusy = false, quoteError = quoteRefusal(result.error))
                }
            }
        }
    }

    /** Copy link landed on the clipboard. */
    fun onLinkCopied() {
        _message.value = UsMessage(LINK_COPIED, UsMessageType.Success)
    }

    fun dismissMessage() {
        _message.value = null
    }

    private fun loadConversations() {
        _state.update { it.copy(conversationsFailed = false) }
        viewModelScope.launch {
            when (val result = chats.conversations()) {
                is AppResult.Success -> _state.update { it.copy(conversations = result.data, conversationsFailed = false) }
                is AppResult.Failure -> _state.update { it.copy(conversationsFailed = true) }
            }
        }
    }

    internal companion object {
        const val SENT = "Sent"
        const val LINK_COPIED = "Link copied"
        const val REPOSTED = "Reposted"
        const val UNREPOSTED = "Repost removed"
        const val REPOST_FAILED = "Couldn't repost. Try again."
        const val UNREPOST_FAILED = "Couldn't remove the repost. Try again."
        const val NOTE_MAX = 500
        const val QUOTED = "Quoted to your profile"
    }
}

/** What stops a quote before it is sent; null when it may go. Code points, as the server counts. */
fun quoteProblem(text: String): String? {
    val trimmed = text.trim()
    return when {
        trimmed.isEmpty() -> "Add your thoughts to quote this post."
        trimmed.codePointCount(0, trimmed.length) > QUOTE_TEXT_MAX -> "A quote can be up to 500 characters."
        else -> null
    }
}

/** A refused quote, by post-service's code (handler.go:3455-3468). */
fun quoteRefusal(error: AppError): String {
    val code = when (error) {
        is AppError.Unknown -> error.code
        is AppError.Forbidden -> error.code
        else -> null
    }
    return when {
        error is AppError.NoNetwork || error is AppError.Timeout ->
            "You're offline. Your words are still here — try again when you're connected."
        error is AppError.NotFound -> "This post no longer exists."
        error is AppError.RateLimited -> "Too many reposts at once. Wait a moment and try again."
        code == "ALREADY_REPOSTED" -> "You already reposted this post."
        code == "NOT_ELIGIBLE" -> "This post can't be reposted."
        code == REPOST_NOT_ALLOWED -> postActionRefusalText(error) ?: "This post can't be reposted."
        code == "QUOTE_TEXT_REQUIRED" -> "Add your thoughts to quote this post."
        code == "QUOTE_TEXT_TOO_LONG" -> "A quote can be up to 500 characters."
        else -> "Couldn't post your quote. Try again."
    }
}
