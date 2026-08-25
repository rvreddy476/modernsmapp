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

/**
 * A post's attachment, with the accessibility decision its author made.
 *
 * ## WHY [altText] AND [altDecorative] ARE BOTH HERE
 *
 * The composer refuses to publish an image until the author either describes it
 * or explicitly marks it decorative. Carrying only the description would lose
 * the difference between the two empty cases, and they are not the same claim:
 *
 *  - `altDecorative = true` means "this image carries no information" — a
 *    deliberate statement, and the correct thing to leave unannounced;
 *  - both empty means nobody ever said. Legacy rows, created before the
 *    requirement existed, look like this.
 *
 * Rendering the second as though it were the first is what makes an
 * accessibility bug invisible: the screen reader stays silent either way, and
 * only one of those silences was chosen by a person.
 */
data class PostMediaRef(
    val mediaId: String,
    val kind: String,
    val altText: String = "",
    val altDecorative: Boolean = false,
    /** Carousel ordinal; -1 when the payload predates the field. */
    val position: Int = CarouselOrdinals.ABSENT,
) {
    /**
     * What a screen reader should announce, or null for silence.
     *
     * The single rule both renderers use. Duplicating it would let the feed and
     * the detail screen disagree about the same image.
     */
    val contentDescription: String?
        get() = altText.takeIf { it.isNotBlank() && !altDecorative }
}

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
 * EVERY FIELD HERE IS SERVER-AUTHORITATIVE.
 *
 * An earlier version of this file claimed the payload carried no reaction or
 * repost flag and defaulted both to `false`. That was wrong, and it broke the
 * two journeys that matter most: a post the viewer had already liked rendered
 * unliked after a cold start, so the first tap sent another POST instead of
 * removing the reaction; an existing repost rendered un-reposted, so the first
 * tap POSTed and came back `409 ALREADY_REPOSTED` with no way to undo.
 *
 * Verified against a live capture on 2026-08-21: the payload carries
 * `has_reacted`, `viewer_reaction`, `is_bookmarked` and `has_reposted`
 * together. Anything defaulted here must be defaulted because the SERVER
 * omitted it, never because the client did not ask.
 */
data class PostViewerState(
    val isBookmarked: Boolean,
    val hasReacted: Boolean = false,
    /**
     * Which reaction, when there is one: `like`, `love`, `haha`, `wow`, `sad`,
     * `angry`. Sent as `viewer_reaction` and omitted entirely when the viewer
     * has not reacted, so null and "no reaction" are the same state.
     *
     * [hasReacted] stays the authoritative binary — a future reaction type the
     * client does not recognise must still count as reacted.
     */
    val viewerReaction: String? = null,
    val hasReposted: Boolean = false,
)
