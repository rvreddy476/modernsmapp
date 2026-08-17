package com.us.android.feature.post.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.Post
import com.us.android.core.model.PostCounts
import com.us.android.core.model.PostViewerState
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.noContentApiCall
import com.us.android.feature.post.data.dto.PostDto
import com.us.android.feature.post.data.dto.ReactionRequest
import com.us.android.feature.post.data.dto.RepostRequest
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class PostRepository @Inject constructor(
    private val api: PostApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun getPost(postId: String): AppResult<Post> =
        apiCall(errorMapper) { api.getPost(postId) }.map { it.toDomain() }

    suspend fun addReaction(postId: String, reaction: String = LIKE): AppResult<Unit> =
        apiCall(errorMapper) { api.addReaction(postId, ReactionRequest(reaction)) }.map { }

    suspend fun removeReaction(postId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.removeReaction(postId) }.map { }

    /**
     * Saves or unsaves, by SET and CLEAR rather than by toggling.
     *
     * The caller states the state it wants; repetition is harmless. Returning
     * the server's boolean rather than `Unit` is kept anyway — it costs
     * nothing and means a future divergence between intent and outcome is
     * visible instead of assumed.
     */
    suspend fun setBookmarked(postId: String, bookmarked: Boolean): AppResult<Boolean> =
        apiCall(errorMapper) {
            if (bookmarked) api.setBookmark(postId) else api.clearBookmark(postId)
        }.map { it.bookmarked }

    suspend fun repost(postId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.repost(postId, RepostRequest(PLAIN)) }.map { }

    /** 204 with no body, so this cannot go through the envelope path. */
    suspend fun removeRepost(postId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.removeRepost(postId) }

    private companion object {
        const val LIKE = "like"

        /** The only repost type this client creates. */
        const val PLAIN = "plain"
    }
}

private fun PostDto.toDomain() = Post(
    id = id,
    authorId = authorId,
    text = text,
    visibility = visibility,
    postType = postType,
    createdAt = createdAt,
    counts = PostCounts(
        likes = counts.likes,
        comments = counts.comments,
        reposts = repostCount,
        views = viewCount,
    ),
    viewer = PostViewerState(
        isBookmarked = isBookmarked,
        // The payload carries no per-viewer reaction flag, so this starts
        // false for everyone and only a local action changes it. See
        // PostViewerState for why inventing the field would be worse.
        hasReacted = false,
    ),
    // Negatives inverted once, here, rather than at every call site.
    allowsComments = !noComments,
    allowsReactions = !noLikes,
    isRepostable = isRepostable,
    isPinned = isPinned,
)
