package com.us.android.feature.post.data

/**
 * A comment, as the comments surface understands it.
 *
 * Lives in `:feature:post` rather than `:core:model` on purpose. `Post` is in
 * the shared model because three modules already read it; a comment currently
 * has exactly one consumer. The reuse strategy (§2.3) anticipates a shared
 * `CommentRow` composite, and the day a second surface renders comments this
 * type moves to `:core:model` unchanged. Promoting it before that consumer
 * exists would add a shared contract nobody is holding.
 *
 * Narrower than [com.us.android.feature.post.data.dto.CommentDto], for the
 * same reason `Post` is narrower than `PostDto`:
 *
 *  - `post_id` is the parameter the caller already supplied to fetch this list.
 *  - `updated_at` has no renderer; an edited-comment marker would need a rule
 *    about how far the two timestamps may drift, and none was captured.
 *  - `dislike_count` is on the wire but is deliberately not modelled here. No
 *    downvote control exists anywhere in the design system, and no endpoint to
 *    cast one was ever captured. Carrying the field would invite a UI for a
 *    feature the product does not have.
 */
data class Comment(
    val id: String,
    /**
     * The only identity the payload provides. There is no display name,
     * handle or avatar on a comment, so any name shown next to one would be
     * fabricated or would need a per-author profile fetch this surface does
     * not make.
     */
    val authorId: String,
    val body: String,
    /** Display only. No endpoint to like a comment has been captured. */
    val likeCount: Int,
    /** Display only. There is no captured route that returns a reply thread. */
    val replyCount: Int,
    /**
     * True when the server marked this row a reply.
     *
     * It is a label, not a structure. The payload carries no parent id, so
     * replies arrive flat in the same list as top-level comments and cannot be
     * nested under what they answer. The UI marks them instead of indenting
     * them, because indenting rows under an arbitrary neighbour would assert a
     * parent-child relationship the server never stated.
     */
    val isReply: Boolean,
    /** Raw ISO-8601 from the wire; formatting is the renderer's problem. */
    val createdAt: String,
)
