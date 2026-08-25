package com.us.android.core.engagement.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult

/**
 * One comment as a screen renders it.
 *
 * NO AUTHOR PRESENTATION. The list payload carries `author_id` and nothing
 * else, and resolving a display name per row would fire one profile request
 * per comment inside a scrolling list — the N+1 the review explicitly forbids.
 * Until a batch author-hydration contract exists, the UI shows an honest
 * minimal identity rather than a name invented on the client.
 *
 * [pending] marks a comment shown optimistically, before the server has
 * confirmed it.
 */
data class CommentRow(
    val id: String,
    val authorId: String,
    val body: String,
    val createdAt: String,
    val likeCount: Int = 0,
    val replyCount: Int = 0,
    val pending: Boolean = false,
)

/** Everything the comments UI renders. */
data class CommentsUiState(
    val rows: List<CommentRow> = emptyList(),
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
) {
    val canLoadMore: Boolean get() = nextCursor != null && !appending && appendError == null
    val canSubmit: Boolean get() = draft.isValidComment() && !submitting
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
 */
fun String.isValidComment(): Boolean {
    val trimmed = trim()
    return trimmed.isNotEmpty() && trimmed.length <= MAX_COMMENT_LENGTH
}

const val MAX_COMMENT_LENGTH = 2_000

/**
 * Loads and mutates one post's comments.
 *
 * Plain class, not a ViewModel: the same logic backs a bottom sheet over the
 * feed and a full screen from post detail, and a ViewModel would tie it to
 * whichever of those owns the lifecycle.
 */
class CommentsController(
    private val postId: String,
    private val repository: EngagementRepository,
) {

    private var state = CommentsUiState()

    /**
     * The key for the comment currently being submitted.
     *
     * Held across retries ON PURPOSE. post-service wraps the create route in
     * `middleware.Idempotency(rdb)`, which replays the first response for a
     * repeated key. Minting a new key per attempt would post the comment twice
     * whenever the first response was merely lost in transit, and the user
     * cannot tell which duplicate to delete.
     *
     * Cleared only once a submission actually succeeds, so the NEXT comment
     * gets its own key.
     */
    private var submitKey: String? = null

    /**
     * The exact normalized text [submitKey] was minted for.
     *
     * A key alone is not enough. The server replays the FIRST response for a
     * repeated key, so if the original request actually succeeded and only its
     * response was lost, the user could edit the draft, submit under the same
     * key, and get the earlier comment back — their edited text silently
     * discarded, the old text shown as though they had written it.
     *
     * Comparing the text at submit time, rather than invalidating on every
     * keystroke, also handles the case where someone edits and then types the
     * original text back: that is still the same intent and must still reuse
     * the key rather than posting a duplicate.
     */
    private var submitKeyText: String? = null

    fun snapshot(): CommentsUiState = state

    suspend fun refresh(): CommentsUiState {
        state = state.copy(loading = true, refreshError = null)
        return when (val result = repository.comments(postId)) {
            is AppResult.Success -> state.copy(
                rows = result.data.items.map { it.toRow() },
                nextCursor = result.data.nextCursor,
                loading = false,
                refreshError = null,
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

    fun onDraftChange(text: String): CommentsUiState =
        state.copy(draft = text, submitError = null).also { state = it }

    /**
     * Posts the draft.
     *
     * The optimistic row is inserted first and REPLACED by the server's row on
     * success rather than left in place — the server assigns the id, and a
     * locally-invented one would collide with the real row on the next page
     * load.
     */
    suspend fun submit(): CommentsUiState {
        val text = state.draft.trim()
        if (!text.isValidComment() || state.submitting) return state

        // Reuse the key only for the SAME text. A retry of an unchanged draft
        // is the same intent and must replay; an edited draft is a different
        // intent and must not be answered with the previous one.
        val key = submitKey?.takeIf { submitKeyText == text }
            ?: EngagementRepository.newIdempotencyKey().also {
                submitKey = it
                submitKeyText = text
            }
        val optimisticId = "pending:$key"
        state = state.copy(
            submitting = true,
            submitError = null,
            rows = listOf(
                CommentRow(
                    id = optimisticId,
                    authorId = "",
                    body = text,
                    createdAt = "",
                    pending = true,
                ),
            ) + state.rows,
        )

        return when (val result = repository.addComment(postId, text, key)) {
            is AppResult.Success -> {
                submitKey = null
                submitKeyText = null
                val confirmed = result.data.toRow()
                state.copy(
                    submitting = false,
                    draft = "",
                    submitError = null,
                    // Replace the placeholder, and drop any row the server
                    // already returned with the same id — a retry that
                    // replayed the idempotent response must not appear twice.
                    rows = listOf(confirmed) +
                        state.rows.filterNot { it.id == optimisticId || it.id == confirmed.id },
                ).also { state = it }
            }

            is AppResult.Failure -> state.copy(
                submitting = false,
                // The draft is put back so the retry button has something to
                // send and the user does not retype it.
                draft = text,
                submitError = result.error,
                rows = state.rows.filterNot { it.id == optimisticId },
            ).also { state = it }
        }
    }
}

private fun CommentDto.toRow() = CommentRow(
    id = id,
    authorId = authorId,
    body = body,
    createdAt = createdAt,
    likeCount = likeCount,
    replyCount = replyCount,
)
