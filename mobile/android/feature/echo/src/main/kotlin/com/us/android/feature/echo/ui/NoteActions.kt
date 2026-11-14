package com.us.android.feature.echo.ui

import androidx.compose.runtime.Immutable
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.PostShareChats
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.ReportOutcome
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.UsShareTarget
import com.us.android.feature.echo.data.BookmarkState
import com.us.android.feature.echo.data.EchoPeople
import com.us.android.feature.echo.data.EchoRepository
import com.us.android.feature.echo.data.EchoViews
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.canEditText
import com.us.android.feature.echo.data.echoErrorMessage
import com.us.android.feature.echo.data.isAuthor
import com.us.android.feature.echo.di.EchoClock
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Clock
import javax.inject.Inject
import javax.inject.Singleton

/** What every Echo screen's note actions need, in one injection. */
@Singleton
class EchoActionDeps @Inject constructor(
    val repository: EchoRepository,
    val people: EchoPeople,
    val chats: PostShareChats,
    val views: EchoViews,
    @EchoClock val clock: Clock,
)

/** The `···` menu open on [note]: X's rows, for this viewer. */
@Immutable
data class NoteMenuState(
    val note: Note,
    val own: Boolean,
    val canEdit: Boolean,
    /** Null while the graph is read; then whether the viewer follows the author. */
    val follows: Boolean? = null,
    val confirmBlock: Boolean = false,
    val confirmDelete: Boolean = false,
    val reporting: Boolean = false,
)

/** "Send via Chat": the viewer's chats to pick from. */
@Immutable
data class SharePickState(
    val targets: List<UsShareTarget>? = null,
    val failed: Boolean = false,
    val sending: Set<String> = emptySet(),
    val error: String? = null,
    val query: String = "",
)

@Immutable
data class NoteActionsState(
    val menu: NoteMenuState? = null,
    /** The repost sheet (Repost / Undo repost, Quote) is open on this note. */
    val repostFor: Note? = null,
    /** The share sheet is open on this note. */
    val shareFor: Note? = null,
    val picker: SharePickState? = null,
    val report: UsPostReportState = UsPostReportState.Idle,
    /** The author's Edit: the screen opens the composer and clears it. */
    val openComposer: ComposeRequest? = null,
    val message: String? = null,
)

/**
 * The note actions every Echo list shares: heart, repost, bookmark, vote,
 * share (system sheet, copy link, Send via Chat), and X's `···` menu (Not
 * interested, Follow / Unfollow, Mute, Block, Report; Edit and Delete on the
 * viewer's own). Writes are optimistic where X's are, and put back if the
 * server refuses.
 *
 * The host list hands in [apply] (change every copy of a note) and [remove]
 * (take notes off the list); the composer gates (reply, quote, post) stay
 * with the host's ViewModel.
 */
@Suppress("TooManyFunctions") // one function per row of X's menus
class EchoNoteActions(
    private val scope: CoroutineScope,
    private val deps: EchoActionDeps,
    private val apply: (id: String, transform: (Note) -> Note) -> Unit,
    private val remove: (predicate: (Note) -> Boolean) -> Unit,
) {
    private val repository get() = deps.repository
    private val _state = MutableStateFlow(NoteActionsState())
    val state: StateFlow<NoteActionsState> = _state.asStateFlow()

    /** Notes on screen are counted as seen, in batches. */
    fun seen(notes: List<Note>) = deps.views.seen(notes)

    val viewerId: String get() = deps.people.viewerId

    /** The first replies of the cards on screen, for their reply bubbles (read lazily, never blocking). */
    val replyPreviews = ReplyPreviewStore(scope) { id -> repository.replyPreview(id, ReplyPreviewStore.LIMIT) }

    private val _follows = MutableStateFlow<Map<String, Boolean>>(emptyMap())

    /** Whether the viewer follows each author asked about with [readFollow]: the note page's Follow pill. */
    val follows: StateFlow<Map<String, Boolean>> = _follows.asStateFlow()

    /** Reads whether the viewer follows [authorId], once; never for the viewer's own notes. */
    fun readFollow(authorId: String) {
        if (authorId.isBlank() || authorId == viewerId) return
        if (viewerId.isBlank() || authorId in _follows.value) return
        scope.launch {
            val follows = (deps.people.follows(authorId) as? AppResult.Success)?.data ?: return@launch
            _follows.update { it + (authorId to follows) }
        }
    }

    /** The Follow pill: Follow or Unfollow, by what [readFollow] read. */
    fun toggleFollowAuthor(note: Note) {
        val follows = _follows.value[note.authorId] ?: return
        toggleFollow(note, follows)
    }

    // ── heart, repost, bookmark, vote ─────────────────────────────────

    fun toggleLike(note: Note) {
        scope.launch { toggleLike(repository, note, apply)?.let(::say) }
    }

    fun openRepost(note: Note) = _state.update { it.copy(repostFor = note) }

    fun closeRepost() = _state.update { it.copy(repostFor = null) }

    fun toggleRepost(note: Note) {
        closeRepost()
        scope.launch { toggleRepost(repository, note, apply)?.let(::say) }
    }

    /**
     * The bookmark, optimistic: the ribbon fills at once; the server's answer
     * then sets both the state and the public count, and a refusal puts the
     * note back exactly as it was.
     */
    fun toggleBookmark(note: Note) {
        val target = !note.bookmarked
        apply(note.id) { it.withBookmarked(target) }
        scope.launch {
            when (val result = repository.setBookmarked(note.id, target)) {
                is AppResult.Success -> {
                    apply(note.id) { it.withBookmark(result.data) }
                    say(if (result.data.bookmarked) BOOKMARK_ADDED else BOOKMARK_REMOVED)
                }
                is AppResult.Failure -> {
                    apply(note.id) { it.copy(bookmarked = note.bookmarked, bookmarkCount = note.bookmarkCount) }
                    say(echoErrorMessage(result.error))
                }
            }
        }
    }

    /** One final vote; the server answers the poll as the viewer may now see it. */
    fun vote(note: Note, optionIndex: Int) {
        val poll = note.poll ?: return
        if (poll.viewerVote != null || isAuthor(note, viewerId)) return
        scope.launch {
            when (val result = repository.vote(note.id, optionIndex)) {
                is AppResult.Success -> apply(note.id) { it.copy(poll = result.data.poll ?: it.poll) }
                is AppResult.Failure -> say(echoErrorMessage(result.error))
            }
        }
    }

    // ── share ─────────────────────────────────────────────────────────

    fun openShare(note: Note) = _state.update { it.copy(shareFor = note, picker = null) }

    fun closeShare() = _state.update { it.copy(shareFor = null, picker = null) }

    /** The system chooser or Copy link went out: counted once, as an external share. */
    fun shared(note: Note) {
        scope.launch { repository.recordShare(note.id) }
    }

    fun linkCopied(note: Note) {
        shared(note)
        say(LINK_COPIED)
    }

    fun openPicker() {
        _state.update { it.copy(picker = SharePickState()) }
        scope.launch {
            val result = deps.chats.conversations()
            _state.update { s ->
                val picker = s.picker ?: return@update s
                s.copy(
                    picker = when (result) {
                        is AppResult.Success -> picker.copy(targets = chatTargets(result.data, viewerId))
                        is AppResult.Failure -> picker.copy(failed = true)
                    },
                )
            }
        }
    }

    fun closePicker() = _state.update { it.copy(picker = null) }

    fun pickerQuery(query: String) = _state.update { s -> s.copy(picker = s.picker?.copy(query = query)) }

    /** Sends the note to one chat as a post card (notes are posts), then records the share. */
    fun sendTo(target: UsShareTarget) {
        val note = _state.value.shareFor ?: return
        if (target.id in _state.value.picker?.sending.orEmpty()) return
        _state.update { s -> s.copy(picker = s.picker?.copy(sending = s.picker.sending + target.id, error = null)) }
        scope.launch {
            when (val result = deps.chats.sendPost(target.id, note.id, ChatRepository.newIdempotencyKey())) {
                is AppResult.Success -> {
                    repository.recordShare(note.id)
                    _state.update { it.copy(shareFor = null, picker = null, message = SENT) }
                }
                is AppResult.Failure -> _state.update { s ->
                    s.copy(
                        picker = s.picker?.copy(
                            sending = s.picker.sending - target.id,
                            error = echoErrorMessage(result.error),
                        ),
                    )
                }
            }
        }
    }

    // ── the ··· menu ──────────────────────────────────────────────────

    fun openMenu(note: Note) {
        val own = isAuthor(note, viewerId)
        _state.update {
            it.copy(
                menu = NoteMenuState(
                    note = note,
                    own = own,
                    canEdit = canEditText(note, viewerId, deps.clock.instant()),
                ),
                report = UsPostReportState.Idle,
            )
        }
        if (own || viewerId.isBlank()) return
        scope.launch {
            val follows = (deps.people.follows(note.authorId) as? AppResult.Success)?.data ?: return@launch
            _state.update { s ->
                s.copy(
                    menu = s.menu?.takeIf { it.note.id == note.id }?.copy(follows = follows) ?: s.menu
                )
            }
        }
    }

    fun closeMenu() = _state.update { it.copy(menu = null, report = UsPostReportState.Idle) }

    /** X's "Not interested in this post": it leaves this list. */
    fun notInterested(note: Note) {
        closeMenu()
        remove { it.id == note.id }
        say(NOT_INTERESTED)
    }

    fun toggleFollow(note: Note, follows: Boolean) {
        closeMenu()
        val handle = note.author?.handle?.ifBlank { null } ?: "them"
        scope.launch {
            when (val result = deps.people.setFollowing(note.authorId, !follows)) {
                is AppResult.Success -> {
                    _follows.update { it + (note.authorId to !follows) }
                    say(if (follows) "You unfollowed $handle" else "You followed $handle")
                }
                is AppResult.Failure -> say(echoErrorMessage(result.error))
            }
        }
    }

    /** Mute: their notes leave this list at once. */
    fun mute(note: Note) {
        closeMenu()
        scope.launch {
            when (val result = deps.people.mute(note.authorId)) {
                is AppResult.Success -> {
                    remove { it.authorId == note.authorId }
                    say("You muted ${note.author?.handle?.ifBlank { null } ?: "this account"}")
                }
                is AppResult.Failure -> say(echoErrorMessage(result.error))
            }
        }
    }

    fun askBlock() = _state.update { s -> s.copy(menu = s.menu?.copy(confirmBlock = true)) }

    fun block(note: Note) {
        closeMenu()
        scope.launch {
            when (val result = deps.people.block(note.authorId)) {
                is AppResult.Success -> {
                    remove { it.authorId == note.authorId }
                    say("You blocked ${note.author?.handle?.ifBlank { null } ?: "this account"}")
                }
                is AppResult.Failure -> say(echoErrorMessage(result.error))
            }
        }
    }

    fun startReport() = _state.update { s -> s.copy(menu = s.menu?.copy(reporting = true)) }

    fun report(note: Note, reason: String, details: String) {
        _state.update { it.copy(report = UsPostReportState.Sending) }
        scope.launch {
            val outcome = repository.report(note.id, reason, details)
            _state.update {
                it.copy(
                    report = when (outcome) {
                        ReportOutcome.Filed -> UsPostReportState.Sent
                        ReportOutcome.AlreadyReported -> UsPostReportState.AlreadyReported
                        is ReportOutcome.Failed -> UsPostReportState.Failed
                    },
                )
            }
        }
    }

    fun edit(note: Note) {
        if (!canEditText(note, viewerId, deps.clock.instant())) return
        _state.update { it.copy(menu = null, openComposer = ComposeRequest(ComposeMode.EDIT, note.id)) }
    }

    fun composerOpened() = _state.update { it.copy(openComposer = null) }

    fun askDelete() = _state.update { s -> s.copy(menu = s.menu?.copy(confirmDelete = true)) }

    fun delete(note: Note) {
        closeMenu()
        if (!isAuthor(note, viewerId)) return
        scope.launch {
            when (val result = repository.delete(note.id)) {
                is AppResult.Success -> {
                    remove { it.id == note.id }
                    say(DELETED)
                }
                is AppResult.Failure -> say(echoErrorMessage(result.error))
            }
        }
    }

    fun say(message: String) = _state.update { it.copy(message = message) }

    fun messageShown() = _state.update { it.copy(message = null) }

    companion object {
        const val BOOKMARK_ADDED = "Added to your Bookmarks"
        const val BOOKMARK_REMOVED = "Removed from your Bookmarks"
        const val LINK_COPIED = "Copied to clipboard"
        const val SENT = "Sent"
        const val NOT_INTERESTED = "You'll see fewer posts like this."
        const val DELETED = "Your post was deleted"
    }
}

/** The bookmark on or off, its count moved with it (the optimistic half). */
fun Note.withBookmarked(bookmarked: Boolean): Note =
    if (this.bookmarked == bookmarked) {
        this
    } else {
        copy(bookmarked = bookmarked, bookmarkCount = (bookmarkCount + if (bookmarked) 1 else -1).coerceAtLeast(0))
    }

/** The server's own answer: its state and its count. */
fun Note.withBookmark(state: BookmarkState): Note = copy(bookmarked = state.bookmarked, bookmarkCount = state.count)

/**
 * The chats a note can be sent into, most recent first: direct and group
 * chats only, never a message request or a dating chat (the same rules the
 * post share sheet keeps).
 */
fun chatTargets(conversations: List<Conversation>, viewerId: String): List<UsShareTarget> = conversations
    .asSequence()
    .filter { it.id.isNotBlank() && !it.isRequest && !it.isDatingChat }
    .filter { it.type == CHAT_DIRECT || it.type == CHAT_GROUP }
    .distinctBy { it.id }
    .sortedByDescending { it.lastMessageAt?.takeIf { at -> at.isNotBlank() } ?: it.updatedAt }
    .map {
        UsShareTarget(
            id = it.id,
            name = it.displayTitle(viewerId),
            isGroup = it.type == CHAT_GROUP,
            avatarUrl = it.avatarUrl?.takeIf { url -> url.isNotBlank() },
        )
    }
    .toList()

private const val CHAT_DIRECT = "direct"
private const val CHAT_GROUP = "group"
