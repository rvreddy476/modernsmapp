package com.us.android.core.model

/**
 * A post's members-only gate, as post-service and feed-service send it on
 * every row (paid memberships, 2026-10-23; post-service members_read.go and
 * feed-service hydration.go).
 *
 *  - [tierRequiredId] is the level's id on EVERY gated row, for members and
 *    non-members alike; null on an open post.
 *  - [bodyRedacted] is true only on a row the server emptied for THIS viewer
 *    (signed out, not a member, or the membership lookup failed): text, rich
 *    text, media, poll, hashtags, mentions, link, location, chapters and
 *    credits are gone; title, cover, author, counts and the level are kept.
 *  - [redactedDurationMs] rides only on a redacted row: the longest attached
 *    video's length, kept so the join card can still show it.
 *  - [requiredPricePaise] is the level's monthly price (post-service
 *    `tier_required_price_paise`, posts.go:144), so the Join sheet can say
 *    which levels open the post; null when the server could not read it, and
 *    on rows from a surface that does not carry it.
 */
data class MembersOnly(
    val tierRequiredId: String? = null,
    val bodyRedacted: Boolean = false,
    val redactedDurationMs: Long = 0L,
    val requiredPricePaise: Long? = null,
) {
    /** The post is members-only at all — whether or not this viewer may see it. */
    val isGated: Boolean get() = tierRequiredId != null || bodyRedacted

    companion object {
        val None = MembersOnly()

        /** From the wire: a blank level id is no level; a negative duration is none. */
        fun fromWire(
            tierRequiredId: String?,
            bodyRedacted: Boolean,
            durationMs: Long,
            requiredPricePaise: Long? = null,
        ): MembersOnly {
            val tier = tierRequiredId?.trim()?.takeIf { it.isNotEmpty() }
            if (tier == null && !bodyRedacted) return None
            return MembersOnly(tier, bodyRedacted, durationMs.coerceAtLeast(0L), requiredPricePaise?.takeIf { it > 0L })
        }
    }
}

/** What a viewer gets of a post, decided once by [MembersOnly.access]. */
enum class MembersAccess {
    /** Not members-only: draw it as any post. */
    Open,

    /** Members-only and this viewer may see it (a member, or its author): draw it, with the badge. */
    Member,

    /** Members-only and this viewer may not, or it is not known: the lock card, never the body. */
    Locked,
}

/**
 * THE lock decision. It never fails open:
 *
 *  1. a row the server redacted is [MembersAccess.Locked], whoever is asking
 *     — there is nothing left in it to show;
 *  2. an open row is [MembersAccess.Open];
 *  3. a gated row is [MembersAccess.Member] for its author, or when it
 *     arrived with a body to show ([hasBody]) — the server only sends a gated
 *     body to someone entitled to it;
 *  4. a gated row with no body is [MembersAccess.Locked]: a member's row that
 *     lost its media, or a decision not made yet, is not a reason to play.
 */
fun MembersOnly.access(isAuthor: Boolean, hasBody: Boolean): MembersAccess = when {
    bodyRedacted -> MembersAccess.Locked
    !isGated -> MembersAccess.Open
    isAuthor -> MembersAccess.Member
    hasBody -> MembersAccess.Member
    else -> MembersAccess.Locked
}

/**
 * [MembersOnly.access] for a feed row: the author is [viewerId]'s own post;
 * the body is the media, the text or the poll the row carries.
 */
fun FeedItem.membersAccess(viewerId: String?): MembersAccess = membersOnly.access(
    isAuthor = !viewerId.isNullOrBlank() && viewerId == authorId,
    hasBody = media.isNotEmpty() || text.isNotBlank() || poll != null,
)

/**
 * [MembersOnly.access] for something that must PLAY — a reel or a long
 * video: only a row with media counts as having a body, so a gated row whose
 * media did not arrive is locked rather than started from nothing.
 */
fun FeedItem.membersPlayback(viewerId: String?): MembersAccess = membersOnly.access(
    isAuthor = !viewerId.isNullOrBlank() && viewerId == authorId && media.isNotEmpty(),
    hasBody = media.isNotEmpty(),
)
