package com.us.android.core.model

/**
 * A post, as the product understands it.
 *
 * Narrower than the wire payload on purpose. The captured response carries
 * around thirty fields, most of them distribution and moderation metadata —
 * `paid_promotion`, `altered_content`, `is_made_for_kids`, `license`,
 * `allow_embedding`, `remix_setting`, `distribution_rev`, `app_origin`. The
 * DTO deserializes all of them so nothing breaks, and this model carries only
 * what a screen renders or a decision depends on.
 *
 * Verified against a live capture on 2026-08-16
 * (prompt/android-api-contracts.md §2).
 */
data class Post(
    val id: String,
    val authorId: String,
    val text: String,
    val visibility: String,
    val postType: String,
    val createdAt: String,
    val counts: PostCounts,
    val viewer: PostViewerState,
    /**
     * Whether the post accepts comments and reactions.
     *
     * These arrive as `no_comments` and `no_likes` — negatives. They are
     * inverted here because a UI that reads `if (!post.noComments)` inverts
     * them at every call site instead, and one missed negation silently offers
     * a control the author disabled.
     */
    val allowsComments: Boolean,
    val allowsReactions: Boolean,
    val isRepostable: Boolean,
    val isPinned: Boolean,
    /**
     * Attachment references. The post payload carries ids only; delivery URLs
     * are resolved separately through media-service.
     */
    val media: List<PostMediaRef> = emptyList(),
)

/** An attachment reference on a post: what to resolve, and what it is. */
data class PostMediaRef(
    val mediaId: String,
    val kind: String,
)

data class PostCounts(
    val likes: Int,
    val comments: Int,
    val reposts: Int,
    val views: Int,
)

/**
 * State that depends on who is asking.
 *
 * The same post read anonymously and with a bearer token returns different
 * values here, which is why an anonymous read must never be cached and then
 * shown to a signed-in viewer.
 *
 * There is no `isReacted` field. The captured payload does not carry one —
 * only `is_bookmarked`. The client therefore cannot know on load whether the
 * viewer already reacted, and [hasReacted] starts as an honest `false` that
 * only local actions change. Inventing a field the server does not send would
 * produce a like button that lies on every cold start.
 */
data class PostViewerState(
    val isBookmarked: Boolean,
    val hasReacted: Boolean = false,
)
