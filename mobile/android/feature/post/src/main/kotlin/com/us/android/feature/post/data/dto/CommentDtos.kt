// Filename / MatchingDeclarationName: named for the endpoint's wire types the
// way PostDtos.kt is, not for the one class that happens to live here today.
// Both rules assume a file with a single classlike declaration is *about* that
// declaration; here the file is about a route, and renaming it to
// CommentDto.kt would break the module's one-file-per-endpoint-group
// convention the moment a second type is captured for this route.
@file:Suppress("Filename", "MatchingDeclarationName")

package com.us.android.feature.post.data.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * `GET /v1/posts/:postId/comments`, transcribed from the 2026-08-17 recapture
 * (prompt/android-api-contracts.md §2).
 *
 * This type exists now and did not before because the evidence changed. The
 * 2026-08-16 pass only ever saw `{"data":[]}`, so the item shape was unknown
 * and `PostApi` deliberately declared no comments route rather than guess one.
 * The recapture returned a populated item, and every field below is a
 * transcription of that body — none of it is inferred from the post-service
 * source or from what a comment is assumed to carry.
 *
 * Every field has a default for the same reason as [PostDto]: a strict parser
 * turns one renamed or added server field into a blank comments screen, and
 * losing the whole surface is a worse failure than rendering one stale field.
 */
@Serializable
data class CommentDto(
    val id: String = "",
    @SerialName("post_id") val postId: String = "",
    /**
     * The commenter. The payload carries an id and NOTHING else — no display
     * name, handle or avatar — which is why the row cannot show a name without
     * a second lookup this surface does not perform.
     */
    @SerialName("author_id") val authorId: String = "",
    /** The text. Named `body` here, not `text` as it is on a post. */
    val body: String = "",
    @SerialName("like_count") val likeCount: Int = 0,
    @SerialName("dislike_count") val dislikeCount: Int = 0,
    @SerialName("reply_count") val replyCount: Int = 0,
    /**
     * True when this row is a reply rather than a top-level comment.
     *
     * The capture carries the flag but no parent id, so the client can know
     * that a row is a reply and still not know what it replies to. A tree
     * cannot be rebuilt from this; see `Comment` for what the UI does instead.
     */
    @SerialName("is_reply") val isReply: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)
