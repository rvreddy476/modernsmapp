package com.us.android.core.engagement.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import com.us.android.core.network.apiCall
import com.us.android.core.network.pagedApiCall
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Network operations for engagement, mapped into [AppResult].
 *
 * Thin on purpose: no optimistic state, no ordering, no UI concerns. Those
 * live in [EngagementStore], which is what screens observe. Keeping the two
 * apart is what lets the race behaviour be unit-tested without a server.
 */
/**
 * The writes [EngagementStore] performs.
 *
 * An interface so the store's ordering behaviour can be tested against
 * responses that resolve out of order — the defect it exists to prevent is
 * invisible against a real server that usually answers in order.
 */
interface EngagementWrites {
    suspend fun react(postId: String, reaction: String = EngagementRepository.LIKE): AppResult<Unit>
    suspend fun unreact(postId: String): AppResult<Unit>
    suspend fun setBookmarked(postId: String, bookmarked: Boolean): AppResult<Unit>
    suspend fun repost(postId: String): AppResult<Unit>
    suspend fun removeRepost(postId: String): AppResult<Unit>
}

@Singleton
class EngagementRepository @Inject constructor(
    private val api: EngagementApi,
    private val errorMapper: ErrorMapper,
) : EngagementWrites {

    override suspend fun react(postId: String, reaction: String): AppResult<Unit> =
        apiCall(errorMapper) { api.addReaction(postId, ReactionRequest(reaction)) }.map { }

    override suspend fun unreact(postId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.removeReaction(postId) }.map { }

    override suspend fun setBookmarked(postId: String, bookmarked: Boolean): AppResult<Unit> =
        if (bookmarked) {
            apiCall(errorMapper) { api.addBookmark(postId) }.map { }
        } else {
            apiCall(errorMapper) { api.removeBookmark(postId) }.map { }
        }

    /**
     * Plain repost. Quote reposts carry user-authored text and belong to a
     * composer flow, not a one-tap control, so they are not issued here.
     */
    override suspend fun repost(postId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.repost(postId, RepostRequest(type = PLAIN)) }.map { }

    /**
     * Undo a repost.
     *
     * DELETE answers 204 with no body, so there is no envelope to unwrap and
     * no `apiCall` overload that fits — the absence of a thrown exception is
     * the success signal.
     */
    override suspend fun removeRepost(postId: String): AppResult<Unit> =
        apiCall(errorMapper) {
            api.removeRepost(postId)
            com.us.android.core.network.ApiEnvelope(data = Unit)
        }.map { }

    /**
     * Records an EXTERNAL share, once.
     *
     * Called only after the system chooser has actually been launched, so a
     * user who opens the share menu and backs out is not counted. `repost` and
     * `quote` share types are deliberately never sent from here — the repost
     * endpoint already produces those, and sending both would count one action
     * twice.
     */
    suspend fun recordExternalShare(postId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.share(postId, ShareRequest(shareType = EXTERNAL)) }.map { }

    /** One page of comments. A null [cursor] requests the first page. */
    suspend fun comments(
        postId: String,
        limit: Int = COMMENT_PAGE_SIZE,
        cursor: String? = null,
    ): AppResult<Paged<CommentDto>> =
        pagedApiCall(errorMapper) { api.getComments(postId, limit, cursor) }

    /**
     * Creates a comment.
     *
     * [idempotencyKey] is supplied by the CALLER and must be held stable
     * across retries of the same user intent. Generating it here would mint a
     * fresh key per attempt, which is exactly the double-post the header
     * exists to prevent.
     */
    suspend fun addComment(
        postId: String,
        text: String,
        idempotencyKey: String,
    ): AppResult<CommentDto> =
        apiCall(errorMapper) {
            api.addComment(postId, idempotencyKey, CreateCommentRequest(text))
        }

    companion object {
        const val LIKE = "like"
        const val PLAIN = "plain"
        const val EXTERNAL = "external"

        /** Matches post-service's own default page size. */
        const val COMMENT_PAGE_SIZE = 20

        /** A fresh key per user intent, reused verbatim on retry. */
        fun newIdempotencyKey(): String = UUID.randomUUID().toString()
    }
}
