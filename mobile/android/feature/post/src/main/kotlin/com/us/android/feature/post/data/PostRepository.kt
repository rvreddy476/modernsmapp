package com.us.android.feature.post.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.Post
import com.us.android.core.model.PostCounts
import com.us.android.core.model.PostViewerState
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.feature.post.data.dto.PostDto
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class PostRepository @Inject constructor(
    private val api: PostApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun getPost(postId: String): AppResult<Post> =
        apiCall(errorMapper) { api.getPost(postId) }.map { it.toDomain() }

/*
 * ENGAGEMENT MOVED OUT.
 *
 * Reactions, bookmarks, reposts and comments now live in :core:engagement.
 * They were declared here AND needed by the feed, and the module graph forbids
 * :feature:feed depending on :feature:post — so leaving them would have meant
 * two Retrofit interfaces describing the same routes, free to drift apart on
 * payload names, error handling and idempotency. One seam, one definition.
 */
}

private fun PostDto.toDomain() = Post(
    id = id,
    media = media.toOrderedRefs(),
    authorId = authorId,
    text = text,
    visibility = visibility,
    contentType = contentType,
    title = title,
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
        // Server-authoritative. These were previously hardcoded to false,
        // which made an already-liked or already-reposted post render as
        // untouched and sent the first tap in the wrong direction.
        hasReacted = hasReacted,
        viewerReaction = viewerReaction,
        hasReposted = hasReposted,
    ),
    // Negatives inverted once, here, rather than at every call site.
    allowsComments = !noComments,
    allowsReactions = !noLikes,
    isRepostable = isRepostable,
    isPinned = isPinned,
)
