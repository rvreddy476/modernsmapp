package com.us.android.core.engagement.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult

/**
 * One comment (or reply) as a screen renders it.
 *
 * [authorName] comes from the server's batch author hydration; it is empty
 * when hydration failed or the account is unknown, and the UI then renders
 * its minimal fallback rather than a name invented on the client.
 *
 * [pending] marks a comment shown optimistically, before the server has
 * confirmed it.
 *
 * [likeCount] is the server's `reaction_count` (every reaction on the
 * comment — `like_count` is the same number computed at read time) and
 * [viewerLiked] is "the viewer has a reaction on it", so a reaction set on
 * the web with another emoji still reads as liked here and a tap removes it.
 */
data class CommentRow(
    val id: String,
    val authorId: String,
    val body: String,
    val createdAt: String,
    val authorName: String = "",
    val authorUsername: String = "",
    val likeCount: Int = 0,
    val replyCount: Int = 0,
    val pending: Boolean = false,
    /** The top-level comment this replies to; null on a top-level comment. */
    val parentId: String? = null,
    val viewerLiked: Boolean = false,
    /** Pinned by the post's creator: one per post, shown first. */
    val pinned: Boolean = false,
    /** Hearted by the post's creator. */
    val heartedByAuthor: Boolean = false,
    /**
     * The oldest reply, inline in the list response — shown under a
     * collapsed comment as a preview. See [CommentDto.reply].
     */
    val reply: CommentRow? = null,
) {
    val isReply: Boolean get() = parentId != null
}

/**
 * The replies under one top-level comment, once someone opened them.
 *
 * Kept beside the rows rather than inside them so paging the top-level list
 * never has to carry thread state along, and a refresh can drop every thread
 * in one assignment.
 */
data class ReplyThread(
    val expanded: Boolean = false,
    /** Oldest first, as `GET /v1/comments/:id/replies` answers. */
    val replies: List<CommentRow> = emptyList(),
    val nextCursor: String? = null,
    /** The first page has been fetched. */
    val loaded: Boolean = false,
    val loading: Boolean = false,
    val error: AppError? = null,
)

/**
 * Who the composer is replying to.
 *
 * [parentId] is always the TOP-LEVEL comment: a reply to a reply is filed
 * under the same parent (post-service keeps one level of threading), as the
 * web sends it. [name] is what "Replying to …" shows: `@handle` when the
 * author has one, else their display name.
 */
data class ReplyTarget(
    val parentId: String,
    val targetId: String,
    val name: String,
)

/** A row action that failed and was rolled back; the sheet words it once. */
enum class CommentNotice { LIKE, DELETE, PIN, HEART }

/** A comment report's progress, mirrored by the shared report step. */
enum class CommentReportStatus { IDLE, SENDING, SENT, ALREADY_REPORTED, FAILED }

/**
 * The person typing — what the composer draws beside the field.
 *
 * Resolved OUTSIDE this module (profile + media delivery live elsewhere) and
 * handed in through [CommentsController.setViewer]; a null viewer renders the
 * initial-disc fallback. [avatarUrl] is null when the account has no avatar
 * or the asset is still processing.
 */
data class CommentsViewer(
    val id: String,
    val name: String,
    val avatarUrl: String? = null,
)

/**
 * Where a screen gets the [CommentsViewer] from.
 *
 * An interface here so the controller's owners (the feed's sheet, post
 * detail's sheet) can inject it without this module learning about profiles
 * or media. Bound in `:app`, which already owns cross-module wiring.
 */
fun interface CommentsViewerSource {
    /** Null when there is no session or the profile could not be loaded. */
    suspend fun current(): CommentsViewer?
}

/** Everything the comments UI renders. */
data class CommentsUiState(
    val rows: List<CommentRow> = emptyList(),
    val viewer: CommentsViewer? = null,
    /** The post's author — the only person offered pin and heart. Blank while unknown. */
    val postAuthorId: String = "",
    val loading: Boolean = false,
    val appending: Boolean = false,
    val refreshError: AppError? = null,
    val appendError: AppError? = null,
    val submitError: AppError? = null,
    val submitting: Boolean = false,
    /**
     * The composer's text.
     *
     * Held in state rather than only in the text field so a failed submit can
     * put it back. Losing what someone typed because a request timed out is
     * the single most annoying failure in a comment box.
     */
    val draft: String = "",
    val nextCursor: String? = null,
    /** Opened reply threads, by top-level comment id. */
    val threads: Map<String, ReplyThread> = emptyMap(),
    val replyingTo: ReplyTarget? = null,
    val notice: CommentNotice? = null,
    val report: CommentReportStatus = CommentReportStatus.IDLE,
    /** The `@handle` being typed at the end of the draft, if any. */
    val mentionQuery: String? = null,
    val mentions: List<MentionCandidate> = emptyList(),
) {
    val canLoadMore: Boolean get() = nextCursor != null && !appending && appendError == null
    val canSubmit: Boolean get() = draft.isValidComment() && !submitting

    fun thread(parentId: String): ReplyThread = threads[parentId] ?: ReplyThread()

    /** What the row's ⋮ offers this viewer. */
    fun menuFor(row: CommentRow): List<CommentMenuAction> =
        commentMenuActions(row, viewer?.id, postAuthorId.ifBlank { null })
}

/**
 * Composer validation.
 *
 * Blank-only input is rejected client-side because the server rejects it too
 * (`strings.TrimSpace(text) == ""` -> HTTP 400); catching it here turns a
 * round trip and an error into a disabled button. The length cap is a guard
 * against pasting a novel into a comment box, not a mirror of a known server
 * limit — no maximum was observed in the captures, so this is deliberately
 * generous.
 *
 * The cap counts CODE POINTS, not UTF-16 units (2026-10-02): an emoji is one
 * character to the person typing it and to the server, and two or more to
 * `String.length`, which made a comment of emoji run out at half the cap.
 */
fun String.isValidComment(): Boolean {
    val trimmed = trim()
    return trimmed.isNotEmpty() && trimmed.codePointCount(0, trimmed.length) <= MAX_COMMENT_LENGTH
}

const val MAX_COMMENT_LENGTH = 2_000

/**
 * Loads and mutates one post's comments.
 *
 * Plain class, not a ViewModel: the same logic backs a bottom sheet over the
 * feed and a full screen from post detail, and a ViewModel would tie it to
 * whichever of those owns the lifecycle. [CommentsSession] is the thin
 * coroutine wrapper both hosts share.
 *
 * Row actions (2026-10-14, audit T-17) are OPTIMISTIC and roll back on
 * failure: like, heart, pin and delete show at once and are undone, with a
 * one-line [CommentNotice], when the server refuses.
 */
@Suppress("TooManyFunctions", "LargeClass")
class CommentsController(
    private val postId: String,
    private val repository: EngagementRepository,
    private val actions: CommentActionsRepository,
    private val reports: ReportRepository,
    private val mentionSource: CommentMentionSource = CommentMentionSource.None,
) {

    /**
     * Told of EVERY state change, including the optimistic ones made before
     * a request is sent. The suspend functions answer only once the server
     * has; without this, an optimistic like would not show until the round
     * trip it exists to hide was over.
     */
    var onChange: ((CommentsUiState) -> Unit)? = null

    private var state = CommentsUiState()
        set(value) {
            field = value
            onChange?.invoke(value)
        }

    /**
     * The key for the comment currently being submitted.
     *
     * Held across retries ON PURPOSE. post-service wraps the create and reply
     * routes in `middleware.Idempotency(rdb)`, which replays the first
     * response for a repeated key. Minting a new key per attempt would post
     * the comment twice whenever the first response was merely lost in
     * transit, and the user cannot tell which duplicate to delete.
     *
     * Cleared only once a submission actually succeeds, so the NEXT comment
     * gets its own key.
     */
    private var submitKey: String? = null

    /**
     * The exact intent [submitKey] was minted for: the reply target and the
     * normalized text.
     *
     * A key alone is not enough. The server replays the FIRST response for a
     * repeated key, so if the original request actually succeeded and only its
     * response was lost, the user could edit the draft, submit under the same
     * key, and get the earlier comment back — their edited text silently
     * discarded, the old text shown as though they had written it. The target
     * is part of the intent for the same reason: the same words sent to a
     * different thread are a different comment.
     *
     * Comparing at submit time, rather than invalidating on every keystroke,
     * also handles someone editing and then typing the original text back:
     * that is still the same intent and must still reuse the key.
     */
    private var submitKeyIntent: String? = null

    /** Rows whose like is on the wire; a second tap waits for the first. */
    private val likesInFlight = mutableSetOf<String>()

    /** Rows whose pin or heart is on the wire. */
    private val creatorInFlight = mutableSetOf<String>()

    fun snapshot(): CommentsUiState = state

    /** Who is commenting. Also names the optimistic row, instead of "You". */
    fun setViewer(viewer: CommentsViewer?): CommentsUiState =
        state.copy(viewer = viewer).also { state = it }

    /** The post's author, so the creator's menu rows appear for them alone. */
    fun setPostAuthor(authorId: String): CommentsUiState =
        state.copy(postAuthorId = authorId).also { state = it }

    /**
     * A quick-reaction emoji tap.
     *
     * Instagram's rule: with nothing typed the emoji IS the comment and goes
     * straight out; with a draft in progress it is appended so the tap never
     * discards what the user was writing. The sent emoji reuses [submit] and
     * so inherits its idempotency-key handling.
     */
    suspend fun quickReaction(emoji: String): CommentsUiState =
        if (state.draft.isBlank()) {
            onDraftChange(emoji)
            submit()
        } else {
            onDraftChange(state.draft + emoji)
        }

    suspend fun refresh(): CommentsUiState {
        state = state.copy(loading = true, refreshError = null)
        return when (val result = repository.comments(postId)) {
            is AppResult.Success -> state.copy(
                rows = result.data.items.map { it.toRow() },
                nextCursor = result.data.nextCursor,
                loading = false,
                refreshError = null,
                // A fresh list starts with every thread closed: the replies
                // loaded before may no longer match the counts just fetched.
                threads = emptyMap(),
            ).also { state = it }

            is AppResult.Failure -> state.copy(
                loading = false,
                // Rows are deliberately preserved. A failed refresh over an
                // already-populated list must not blank it.
                refreshError = result.error,
            ).also { state = it }
        }
    }

    /**
     * Loads the next page.
     *
     * Consumes `meta.next_cursor` and de-duplicates by id: a cursor page can
     * legitimately repeat a row when a comment is inserted at the boundary
     * between two requests, and appending it twice produces a duplicate key
     * crash in a keyed list.
     */
    suspend fun loadMore(): CommentsUiState {
        val cursor = state.nextCursor ?: return state
        if (state.appending) return state
        state = state.copy(appending = true, appendError = null)

        return when (val result = repository.comments(postId, cursor = cursor)) {
            is AppResult.Success -> {
                val known = state.rows.mapTo(mutableSetOf()) { it.id }
                val fresh = result.data.items.map { it.toRow() }.filterNot { it.id in known }
                state.copy(
                    rows = state.rows + fresh,
                    nextCursor = result.data.nextCursor,
                    appending = false,
                    appendError = null,
                ).also { state = it }
            }

            // The loaded pages survive. Retry re-runs this with the SAME
            // cursor, so nothing already fetched is discarded or re-fetched.
            is AppResult.Failure -> state.copy(
                appending = false,
                appendError = result.error,
            ).also { state = it }
        }
    }

    fun onDraftChange(text: String): CommentsUiState {
        val query = activeMentionQuery(text)
        return state.copy(
            draft = text,
            submitError = null,
            mentionQuery = query,
            // Suggestions for a handle no longer being typed are stale.
            mentions = if (query == null || query != state.mentionQuery) emptyList() else state.mentions,
        ).also { state = it }
    }

    // ── Replies ─────────────────────────────────────────────────────────

    /** "View N replies" / "Hide replies". Opening fetches the first page once. */
    suspend fun toggleReplies(parentId: String): CommentsUiState {
        val thread = state.thread(parentId)
        if (thread.expanded) return putThread(parentId, thread.copy(expanded = false))
        putThread(parentId, thread.copy(expanded = true))
        return if (thread.loaded) state else loadReplies(parentId)
    }

    /**
     * The next page of a thread (the first, when none was fetched yet).
     * De-duplicated by id: a reply the viewer just posted is already in the
     * list and comes back from the server too.
     */
    suspend fun loadReplies(parentId: String): CommentsUiState {
        val thread = state.thread(parentId)
        if (thread.loading) return state
        if (thread.loaded && thread.nextCursor == null) return state
        val cursor = if (thread.loaded) thread.nextCursor else null
        putThread(parentId, thread.copy(expanded = true, loading = true, error = null))

        return when (val result = actions.replies(parentId, cursor)) {
            is AppResult.Success -> {
                val current = state.thread(parentId)
                val fetched = result.data.items.map { it.toRow(parentHint = parentId) }
                val fetchedIds = fetched.mapTo(mutableSetOf()) { it.id }
                // A first page leads with the server's order, then whatever
                // the viewer posted locally that the page did not include;
                // a later page appends whatever is new.
                val merged = if (current.loaded) {
                    val known = current.replies.mapTo(mutableSetOf()) { it.id }
                    current.replies + fetched.filterNot { it.id in known }
                } else {
                    fetched + current.replies.filterNot { it.id in fetchedIds }
                }
                putThread(
                    parentId,
                    current.copy(
                        replies = merged,
                        nextCursor = result.data.nextCursor,
                        loaded = true,
                        loading = false,
                        error = null,
                    ),
                )
            }

            is AppResult.Failure -> putThread(
                parentId,
                state.thread(parentId).copy(loading = false, error = result.error),
            )
        }
    }

    /**
     * Tapping "Reply" on a row. On a reply, the answer goes to the same
     * thread and the draft starts with `@username `, as on the web, so the
     * person answered is named in the text and not only in the header.
     */
    fun startReply(commentId: String): CommentsUiState {
        val row = findRow(commentId) ?: return state
        if (row.pending) return state
        val parentId = row.parentId ?: row.id
        val handle = row.authorUsername
        val name = if (handle.isNotBlank()) "@$handle" else row.authorName
        val draft = if (row.isReply && handle.isNotBlank() && state.draft.isBlank()) "@$handle " else state.draft
        return state.copy(
            replyingTo = ReplyTarget(parentId = parentId, targetId = row.id, name = name),
            draft = draft,
            submitError = null,
            mentionQuery = activeMentionQuery(draft),
        ).also { state = it }
    }

    /** "Replying to @name" × — back to a top-level comment; an untouched prefill goes too. */
    fun cancelReply(): CommentsUiState {
        val target = state.replyingTo ?: return state
        val prefill = prefillFor(target)
        val draft = if (prefill != null && state.draft == prefill) "" else state.draft
        return state.copy(replyingTo = null, draft = draft, submitError = null).also { state = it }
    }

    private fun prefillFor(target: ReplyTarget): String? {
        val row = findRow(target.targetId) ?: return null
        return row.authorUsername.takeIf { row.isReply && it.isNotBlank() }?.let { "@$it " }
    }

    // ── Submit ──────────────────────────────────────────────────────────

    /**
     * Posts the draft — as a top-level comment, or as a reply when
     * [CommentsUiState.replyingTo] is set.
     *
     * The optimistic row is inserted first and REPLACED by the server's row on
     * success rather than left in place — the server assigns the id, and a
     * locally-invented one would collide with the real row on the next page
     * load.
     */
    suspend fun submit(): CommentsUiState {
        val text = state.draft.trim()
        if (!text.isValidComment() || state.submitting) return state
        val target = state.replyingTo

        // Reuse the key only for the SAME intent. A retry of an unchanged draft
        // is the same intent and must replay; an edited draft is a different
        // intent and must not be answered with the previous one.
        val intent = "${target?.parentId.orEmpty()}\u0000$text"
        val key = submitKey?.takeIf { submitKeyIntent == intent }
            ?: EngagementRepository.newIdempotencyKey().also {
                submitKey = it
                submitKeyIntent = intent
            }
        val optimistic = CommentRow(
            id = "pending:$key",
            authorId = state.viewer?.id.orEmpty(),
            body = text,
            createdAt = "",
            authorName = state.viewer?.name?.ifBlank { null } ?: "You",
            pending = true,
            parentId = target?.parentId,
        )
        return if (target == null) {
            submitComment(text, key, optimistic)
        } else {
            submitReply(target.parentId, text, key, optimistic)
        }
    }

    private suspend fun submitComment(text: String, key: String, optimistic: CommentRow): CommentsUiState {
        state = state.copy(submitting = true, submitError = null, rows = insertAfterPinned(state.rows, optimistic))

        return when (val result = repository.addComment(postId, text, key)) {
            is AppResult.Success -> {
                clearSubmitKey()
                // The create response carries `author_id` only — author
                // hydration happens on list reads — so a fresh comment would
                // render as "?" until the next refresh. The viewer wrote it;
                // attribute it to them.
                val confirmed = result.data.toRow().attributedToViewer(state.viewer)
                // Replace the placeholder, and drop any row the server
                // already returned with the same id — a retry that replayed
                // the idempotent response must not appear twice.
                val others = state.rows.filterNot { it.id == optimistic.id || it.id == confirmed.id }
                state.copy(
                    submitting = false,
                    draft = "",
                    submitError = null,
                    mentionQuery = null,
                    mentions = emptyList(),
                    rows = insertAfterPinned(others, confirmed),
                ).also { state = it }
            }

            is AppResult.Failure -> state.copy(
                submitting = false,
                // The draft is put back so the retry button has something to
                // send and the user does not retype it.
                draft = text,
                submitError = result.error,
                rows = state.rows.filterNot { it.id == optimistic.id },
            ).also { state = it }
        }
    }

    private suspend fun submitReply(
        parentId: String,
        text: String,
        key: String,
        optimistic: CommentRow,
    ): CommentsUiState {
        val opened = state.thread(parentId)
        state = state.copy(submitting = true, submitError = null)
        putThread(parentId, opened.copy(expanded = true, replies = opened.replies + optimistic))

        return when (val result = actions.reply(parentId, text, key)) {
            is AppResult.Success -> {
                clearSubmitKey()
                val confirmed = result.data.toRow(parentHint = parentId).attributedToViewer(state.viewer)
                val thread = state.thread(parentId)
                val replies = thread.replies.map { if (it.id == optimistic.id) confirmed else it }.distinctBy { it.id }
                state.copy(
                    submitting = false,
                    draft = "",
                    submitError = null,
                    replyingTo = null,
                    mentionQuery = null,
                    mentions = emptyList(),
                    threads = state.threads + (parentId to thread.copy(replies = replies)),
                    rows = state.rows.map { if (it.id == parentId) it.copy(replyCount = it.replyCount + 1) else it },
                ).also { state = it }
            }

            is AppResult.Failure -> {
                val thread = state.thread(parentId)
                state.copy(
                    submitting = false,
                    draft = text,
                    submitError = result.error,
                    // The target stays: "try again" must go to the same thread.
                    threads = state.threads +
                        (parentId to thread.copy(replies = thread.replies.filterNot { it.id == optimistic.id })),
                ).also { state = it }
            }
        }
    }

    private fun clearSubmitKey() {
        submitKey = null
        submitKeyIntent = null
    }

    // ── Like ────────────────────────────────────────────────────────────

    /**
     * The heart under a comment. Shown at once; on failure the row's own
     * previous like state and count come back — and a notice says so. On
     * success the server's count replaces the guess.
     */
    suspend fun toggleLike(commentId: String): CommentsUiState {
        val row = findRow(commentId) ?: return state
        if (row.pending || !likesInFlight.add(commentId)) return state
        val liked = !row.viewerLiked
        val beforeLiked = row.viewerLiked
        val beforeCount = row.likeCount
        updateRow(commentId) {
            it.copy(viewerLiked = liked, likeCount = (it.likeCount + if (liked) 1 else -1).coerceAtLeast(0))
        }
        state = state.copy(notice = null)

        val result = actions.setLiked(commentId, liked)
        likesInFlight.remove(commentId)
        return when (result) {
            is AppResult.Success -> updateRow(commentId) {
                it.copy(viewerLiked = result.data.viewerReaction != null, likeCount = result.data.reactionCount)
            }

            is AppResult.Failure -> {
                updateRow(commentId) { it.copy(viewerLiked = beforeLiked, likeCount = beforeCount) }
                state.copy(notice = CommentNotice.LIKE).also { state = it }
            }
        }
    }

    // ── Delete ──────────────────────────────────────────────────────────

    /**
     * Deletes the viewer's own comment or reply (the UI has already asked
     * "Delete this comment?"). Removed at once and put back where it was if
     * the server refuses; a 404 means it is already gone, so it stays gone.
     */
    suspend fun delete(commentId: String): CommentsUiState {
        val row = findRow(commentId) ?: return state
        if (row.pending) return state
        state = state.copy(notice = null)
        val parentId = row.parentId
        val restore = if (parentId == null) removeComment(row) else removeReply(parentId, row)

        return when (val result = actions.delete(commentId)) {
            is AppResult.Success -> state
            is AppResult.Failure -> {
                if (result.error is AppError.NotFound) return state
                restore()
                state.copy(notice = CommentNotice.DELETE).also { state = it }
            }
        }
    }

    /** Takes a top-level comment (and its open thread) out; answers the undo. */
    private fun removeComment(row: CommentRow): () -> Unit {
        val index = state.rows.indexOfFirst { it.id == row.id }
        val thread = state.threads[row.id]
        state = state.copy(rows = state.rows.filterNot { it.id == row.id }, threads = state.threads - row.id)
        return {
            if (state.rows.none { it.id == row.id }) {
                val rows = state.rows.toMutableList().apply { add(index.coerceIn(0, size), row) }
                state = state.copy(
                    rows = rows,
                    threads = if (thread != null) state.threads + (row.id to thread) else state.threads,
                )
            }
        }
    }

    /** Takes a reply out of its thread, its parent's count and its preview; answers the undo. */
    private fun removeReply(parentId: String, reply: CommentRow): () -> Unit {
        val index = state.threads[parentId]?.replies?.indexOfFirst { it.id == reply.id } ?: -1
        val wasPreview = state.rows.firstOrNull { it.id == parentId }?.reply?.id == reply.id
        state.threads[parentId]?.let { t -> putThread(parentId, t.copy(replies = t.replies.filterNot { it.id == reply.id })) }
        updateParent(parentId) {
            it.copy(replyCount = (it.replyCount - 1).coerceAtLeast(0), reply = if (wasPreview) null else it.reply)
        }
        return {
            val current = state.threads[parentId]
            if (current != null && index >= 0 && current.replies.none { it.id == reply.id }) {
                putThread(parentId, current.copy(replies = current.replies.toMutableList().apply { add(index.coerceIn(0, size), reply) }))
            }
            updateParent(parentId) {
                it.copy(replyCount = it.replyCount + 1, reply = if (wasPreview) reply else it.reply)
            }
        }
    }

    private fun updateParent(parentId: String, transform: (CommentRow) -> CommentRow) {
        state = state.copy(rows = state.rows.map { if (it.id == parentId) transform(it) else it })
    }

    // ── Creator tools ───────────────────────────────────────────────────

    /** The creator's heart on a comment or a reply; undone on failure. */
    suspend fun toggleHeart(commentId: String): CommentsUiState {
        val row = findRow(commentId) ?: return state
        if (row.pending || !creatorInFlight.add(commentId)) return state
        val hearted = !row.heartedByAuthor
        updateRow(commentId) { it.copy(heartedByAuthor = hearted) }
        state = state.copy(notice = null)

        val result = actions.setHearted(commentId, hearted)
        creatorInFlight.remove(commentId)
        return when (result) {
            is AppResult.Success -> state
            is AppResult.Failure -> {
                updateRow(commentId) { it.copy(heartedByAuthor = !hearted) }
                state.copy(notice = CommentNotice.HEART).also { state = it }
            }
        }
    }

    /**
     * Pin or unpin a TOP-LEVEL comment. Pinning replaces any other pin (one
     * per post, as the server enforces) and moves the comment to the top,
     * where the server lists it from then on. On failure the previous pin
     * and the comment's old place come back.
     */
    suspend fun togglePin(commentId: String): CommentsUiState {
        val row = state.rows.firstOrNull { it.id == commentId } ?: return state
        if (row.pending || !creatorInFlight.add(commentId)) return state
        val pin = !row.pinned
        val oldIndex = state.rows.indexOf(row)
        val previouslyPinned = state.rows.filter { it.pinned && it.id != commentId }.mapTo(mutableSetOf()) { it.id }
        state = state.copy(
            notice = null,
            rows = if (pin) {
                listOf(row.copy(pinned = true)) + state.rows.filterNot { it.id == commentId }.map { it.copy(pinned = false) }
            } else {
                state.rows.map { if (it.id == commentId) it.copy(pinned = false) else it }
            },
        )

        val result = actions.setPinned(commentId, pin)
        creatorInFlight.remove(commentId)
        return when (result) {
            is AppResult.Success -> state
            is AppResult.Failure -> {
                val current = state.rows.firstOrNull { it.id == commentId }
                val rows = state.rows.filterNot { it.id == commentId }
                    .map { if (it.id in previouslyPinned) it.copy(pinned = true) else it }
                    .toMutableList()
                if (current != null) rows.add(oldIndex.coerceIn(0, rows.size), current.copy(pinned = !pin))
                state.copy(rows = rows, notice = CommentNotice.PIN).also { state = it }
            }
        }
    }

    fun dismissNotice(): CommentsUiState = state.copy(notice = null).also { state = it }

    // ── Report ──────────────────────────────────────────────────────────

    /** Files a `comment` report with trust-safety; a second one reads "already reported". */
    suspend fun report(commentId: String, reason: String, details: String): CommentsUiState {
        if (state.report == CommentReportStatus.SENDING) return state
        state = state.copy(report = CommentReportStatus.SENDING)
        val status = when (reports.reportComment(commentId, reason, details)) {
            ReportOutcome.Filed -> CommentReportStatus.SENT
            ReportOutcome.AlreadyReported -> CommentReportStatus.ALREADY_REPORTED
            is ReportOutcome.Failed -> CommentReportStatus.FAILED
        }
        return state.copy(report = status).also { state = it }
    }

    /** The report step closed: the next report starts fresh. */
    fun resetReport(): CommentsUiState = state.copy(report = CommentReportStatus.IDLE).also { state = it }

    // ── @mentions ───────────────────────────────────────────────────────

    /**
     * Suggestions for the handle being typed. Applied only if the draft still
     * ends in the SAME handle when the answer arrives — a slow search must
     * not offer names for something the person has already finished typing.
     */
    suspend fun searchMentions(): CommentsUiState {
        val query = state.mentionQuery ?: return state
        val found = mentionSource.search(query).filter { it.username.isNotBlank() }.take(MENTION_LIMIT)
        if (state.mentionQuery != query) return state
        return state.copy(mentions = found).also { state = it }
    }

    fun pickMention(candidate: MentionCandidate): CommentsUiState {
        val draft = completeMention(state.draft, candidate.username)
        return state.copy(draft = draft, mentionQuery = null, mentions = emptyList()).also { state = it }
    }

    // ── Row bookkeeping ─────────────────────────────────────────────────

    /** Finds a comment wherever it is shown: the list, an open thread, or a preview. */
    private fun findRow(id: String): CommentRow? =
        state.rows.firstOrNull { it.id == id }
            ?: state.threads.values.firstNotNullOfOrNull { t -> t.replies.firstOrNull { it.id == id } }
            ?: state.rows.firstNotNullOfOrNull { r -> r.reply?.takeIf { it.id == id } }

    /** Applies [transform] to every place [id] is shown, so a preview and its thread copy agree. */
    private fun updateRow(id: String, transform: (CommentRow) -> CommentRow): CommentsUiState {
        state = state.copy(
            rows = state.rows.map { row ->
                val updated = if (row.id == id) transform(row) else row
                val preview = updated.reply
                if (preview != null && preview.id == id) updated.copy(reply = transform(preview)) else updated
            },
            threads = state.threads.mapValues { (_, t) ->
                if (t.replies.none { it.id == id }) {
                    t
                } else {
                    t.copy(replies = t.replies.map { if (it.id == id) transform(it) else it })
                }
            },
        )
        return state
    }

    private fun putThread(parentId: String, thread: ReplyThread): CommentsUiState =
        state.copy(threads = state.threads + (parentId to thread)).also { state = it }

    private companion object {
        /** At most six people, like the web's suggestion list. */
        const val MENTION_LIMIT = 6
    }
}

/** A new comment goes first — after the pinned one, which the server always lists first. */
private fun insertAfterPinned(rows: List<CommentRow>, row: CommentRow): List<CommentRow> {
    val at = rows.indexOfFirst { !it.pinned }.let { if (it < 0) rows.size else it }
    return rows.toMutableList().apply { add(at, row) }
}

/**
 * Fills a missing author from the viewer — only when the server sent none,
 * and only when the row is the viewer's (or unattributed): a hydrated author
 * is never overwritten.
 */
private fun CommentRow.attributedToViewer(viewer: CommentsViewer?): CommentRow {
    if (viewer == null || authorName.isNotBlank()) return this
    if (authorId.isNotBlank() && authorId != viewer.id) return this
    return copy(authorId = viewer.id, authorName = viewer.name)
}

/** [parentHint] fills `parent_id` for a reply whose payload omitted it. */
private fun CommentDto.toRow(parentHint: String? = null): CommentRow = CommentRow(
    id = id,
    authorId = authorId,
    body = body,
    createdAt = createdAt,
    authorName = author?.displayName.orEmpty(),
    authorUsername = author?.username.orEmpty(),
    likeCount = reactionCount ?: likeCount,
    replyCount = replyCount,
    parentId = parentId?.ifBlank { null } ?: parentHint,
    viewerLiked = viewerReaction != null,
    pinned = pinned,
    heartedByAuthor = heartedByAuthor,
    reply = reply?.toRow(parentHint = id),
)
