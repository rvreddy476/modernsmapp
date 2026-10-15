package com.us.android.core.engagement.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import com.us.android.core.network.apiCall
import com.us.android.core.network.pagedApiCall
import javax.inject.Inject
import javax.inject.Singleton

/**
 * [CommentActionsApi] mapped into [AppResult]. Thin, like
 * [EngagementRepository]: the optimistic state and its rollbacks live in
 * [CommentsController].
 */
@Singleton
class CommentActionsRepository @Inject constructor(
    private val api: CommentActionsApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun replies(
        commentId: String,
        cursor: String? = null,
        limit: Int = REPLY_PAGE_SIZE,
    ): AppResult<Paged<CommentDto>> =
        pagedApiCall(errorMapper) { api.replies(commentId, limit, cursor) }

    /** [idempotencyKey] is held by the caller across retries, as for [EngagementRepository.addComment]. */
    suspend fun reply(parentId: String, text: String, idempotencyKey: String): AppResult<CommentDto> =
        apiCall(errorMapper) { api.reply(parentId, idempotencyKey, CreateCommentRequest(text)) }

    /** Likes ([liked] true: `PUT` ❤️) or unlikes (`DELETE`) and answers the server's count. */
    suspend fun setLiked(commentId: String, liked: Boolean): AppResult<CommentReactionSummaryDto> =
        if (liked) {
            apiCall(errorMapper) { api.setReaction(commentId, CommentReactionRequest(LIKE_EMOJI)) }
        } else {
            apiCall(errorMapper) { api.removeReaction(commentId) }
        }

    suspend fun delete(commentId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.delete(commentId) }.map { }

    suspend fun setHearted(commentId: String, hearted: Boolean): AppResult<Unit> =
        if (hearted) {
            apiCall(errorMapper) { api.heart(commentId) }.map { }
        } else {
            apiCall(errorMapper) { api.unheart(commentId) }.map { }
        }

    suspend fun setPinned(commentId: String, pinned: Boolean): AppResult<Unit> =
        if (pinned) {
            apiCall(errorMapper) { api.pin(commentId) }.map { }
        } else {
            apiCall(errorMapper) { api.unpin(commentId) }.map { }
        }

    companion object {
        /** post-service's `LikeEmoji` — what the legacy like route writes too. */
        const val LIKE_EMOJI = "❤️"

        /** post-service's default `limit` for replies. */
        const val REPLY_PAGE_SIZE = 20
    }
}

/** One person the composer's `@` suggestions can offer. */
data class MentionCandidate(
    val userId: String,
    val username: String,
    val displayName: String,
)

/**
 * Where `@` suggestions come from: search-service's `GET /v1/search/users`.
 *
 * A seam rather than a fourth copy of that endpoint: `:core:chat` already
 * reads it (`PeopleLookupRepository`, which tolerates both captured page
 * shapes), and `:app` binds this to it — the same arrangement as
 * [CommentsViewerSource]. Answers an empty list on any failure: a missing
 * suggestion never blocks typing.
 */
fun interface CommentMentionSource {
    suspend fun search(query: String): List<MentionCandidate>

    companion object {
        /** No suggestions — for hosts and tests with no search behind them. */
        val None: CommentMentionSource = CommentMentionSource { emptyList() }
    }
}
