package com.us.android.core.ui

import androidx.compose.runtime.Immutable

/**
 * Everything the post "more" sheet needs to decide what to show.
 *
 * A value type, like [PostCardState]: the feed builds it from a `FeedItem`,
 * the engagement overlay and the follow graph; reels builds it from the same
 * three. The sheet itself never fetches — see the module note in
 * `core/ui/build.gradle.kts`.
 */
@Immutable
data class UsPostMoreState(
    val postId: String,
    /** The handle without `@`, or the display name when the account has none. */
    val username: String,
    /** The viewer's own post: no Interested / Not interested, no Follow, no Block, no Report — Delete instead. */
    val isOwnPost: Boolean,
    /** Server value with this session's tap layered in — flips the Save row's label. */
    val isBookmarked: Boolean,
    val followRow: UsPostMoreFollowRow,
    /** The server's "why you're seeing this" sentence; blank hides the row. */
    val reasonText: String = "",
    /**
     * The post reached the viewer as a recommendation rather than from an
     * account they follow. "Interested" only means something for a
     * suggestion — for someone already followed it says nothing, so the row
     * is withheld (founder, 2026-09-04).
     */
    val suggested: Boolean = true,
    /** What "Copy link" puts on the clipboard. */
    val link: String,
    val report: UsPostReportState = UsPostReportState.Idle,
    /** The viewer's own post: where the delete stands, owned by whoever sends it. */
    val delete: UsPostDeleteState = UsPostDeleteState.Idle,
    /** Another person's post: where "Don't recommend @user" stands, owned by whoever sends it. */
    val dontRecommend: UsPostDontRecommendState = UsPostDontRecommendState.Idle,
    /** True while a one-shot action (block) is on the wire; the rows go inert. */
    val busy: Boolean = false,
    /**
     * Present when the sheet was opened from a REEL: the group that goes
     * above everything else (founder, 2026-09-04, from YouTube Shorts). Null
     * on a feed card, and the card's sheet is exactly what it was.
     */
    val reel: UsReelMoreState? = null,
)

/**
 * What the reel's own group needs: the caption "Description" unfolds, which
 * way the "Clear screen" row points, and the quality picker's options.
 */
@Immutable
data class UsReelMoreState(
    /** The full caption, shown under "Description" when opened; blank hides the row. */
    val description: String,
    /** Full mode is on: the row reads "Show controls" and leaves it. */
    val fullMode: Boolean,
    /** The picker's options, [UsReelQuality.Auto] first — see [reelQualityOptions]. */
    val qualities: List<UsReelQuality>,
    /** The session's choice, shown at the right of the Quality row. */
    val selected: UsReelQuality = UsReelQuality.Auto,
) {
    /**
     * Auto alone means there is nothing to pick — the reel plays its original
     * file, or the ladder has not been read yet — and the row goes inert
     * rather than opening a picker with one entry.
     */
    val canPickQuality: Boolean get() = qualities.size > 1
}

/** One entry of the reel's quality picker: the player's own choice, or one rung of the HLS ladder. */
sealed interface UsReelQuality {
    /** What the row prints: "Auto", "720p". */
    val label: String

    data object Auto : UsReelQuality {
        override val label: String get() = "Auto"
    }

    /** One rendition, by its height in pixels. */
    data class Height(val height: Int) : UsReelQuality {
        override val label: String get() = "${height}p"
    }
}

/**
 * The picker's options from the heights the player reports for the item:
 * Auto first, then each distinct height, tallest first. A non-adaptive
 * item — the original MP4 the server hands out while it transcodes — has
 * no ladder to pick from and offers Auto alone, whatever heights were seen.
 */
fun reelQualityOptions(heights: Iterable<Int>, adaptive: Boolean = true): List<UsReelQuality> {
    if (!adaptive) return listOf(UsReelQuality.Auto)
    val rungs = heights.filter { it > 0 }.distinct().sortedDescending().map { UsReelQuality.Height(it) }
    return listOf(UsReelQuality.Auto) + rungs
}

/**
 * Which relationship row the third group offers.
 *
 * [HIDDEN] when the edge is not yet known — a "Follow" that flips to
 * "Unfollow" once the real answer lands is worse than a row that arrives a
 * moment late — and always on the viewer's own post.
 */
enum class UsPostMoreFollowRow { HIDDEN, FOLLOW, UNFOLLOW }

/** The report step's progress, owned by whoever files the report. */
sealed interface UsPostReportState {
    data object Idle : UsPostReportState
    data object Sending : UsPostReportState
    data object Sent : UsPostReportState

    /** `409 ACTIVE_REPORT_EXISTS`. */
    data object AlreadyReported : UsPostReportState
    data object Failed : UsPostReportState
}

/**
 * The delete's progress, owned by whoever sends it. The sheet shows
 * [Deleted] as its inline confirmation and then leaves; [Failed] keeps the
 * sheet open with the reason under the rows so the viewer can try again.
 */
sealed interface UsPostDeleteState {
    data object Idle : UsPostDeleteState
    data object Deleting : UsPostDeleteState
    data object Deleted : UsPostDeleteState
    data class Failed(val message: String) : UsPostDeleteState
}

/**
 * "Don't recommend @user"'s progress, owned by whoever sends it. Like a
 * delete, it WAITS: the author's posts leave the lists only once the server
 * has the signal, so a refusal never makes rows vanish and then return. The
 * sheet shows [Done] as its inline confirmation and then leaves; [Failed]
 * keeps the sheet open with the reason under the rows.
 */
sealed interface UsPostDontRecommendState {
    data object Idle : UsPostDontRecommendState
    data object Sending : UsPostDontRecommendState
    data object Done : UsPostDontRecommendState
    data class Failed(val message: String) : UsPostDontRecommendState
}

/** One row of the sheet's menu. The order within [rowGroups] is the design's. */
enum class UsPostMoreRow(val label: String) {
    /** Reels only: the full caption, unfolded inline. */
    DESCRIPTION("Description"),

    /** Reels only: full mode — the header and the bar go, the reel stays. */
    CLEAR_SCREEN("Clear screen"),

    /** Reels only: [CLEAR_SCREEN]'s other face, while full mode is on. */
    SHOW_CONTROLS("Show controls"),

    /** Reels only: the rendition picker, the current choice at the right. */
    QUALITY("Quality"),
    SAVE("Save"),
    UNSAVE("Unsave"),
    COPY_LINK("Copy link"),
    SHARE("Share"),
    WHY("Why you're seeing this post"),
    INTERESTED("Interested"),
    NOT_INTERESTED("Not interested"),

    /** Other people's posts: YouTube's "Don't recommend channel" — every post by the author goes. */
    DONT_RECOMMEND("Don't recommend"),
    UNFOLLOW("Unfollow"),
    FOLLOW("Follow"),
    BLOCK("Block"),
    REPORT("Report"),
    DELETE("Delete post"),
}

/**
 * The rows to draw, grouped; hairline dividers go between groups. An empty
 * group is dropped, so no group ever has a dangling divider under it.
 *
 * The rules, from the founder's Instagram capture (2026-09-04):
 *
 *  - Group 1 is always there: Save/Unsave, Copy link, Share.
 *  - Group 2 is for OTHER people's posts: "Why you're seeing this post" only
 *    when the server sent a sentence, then Interested, Not interested, and
 *    "Don't recommend @user" (founder, 2026-09-04, from YouTube's "Don't
 *    recommend channel") — the author-wide "Not interested".
 *  - Group 3, other people's posts: Unfollow or Follow when the edge is
 *    known, Block, and Report last.
 *  - The viewer's own post: group 1, then "Delete post" alone, red and
 *    last — a soft delete with a 30-day restore window (founder, 2026-09-04).
 *  - A REEL puts its own group ABOVE all of that (YouTube Shorts, founder,
 *    2026-09-04): "Description" when there is a caption to unfold, "Clear
 *    screen" or "Show controls" by the mode, and "Quality". Own reel or
 *    not, the group is the same — it is about the frame, not the author.
 */
fun UsPostMoreState.rowGroups(): List<List<UsPostMoreRow>> {
    val reelGroup = reel?.let { reel ->
        buildList {
            if (reel.description.isNotBlank()) add(UsPostMoreRow.DESCRIPTION)
            add(if (reel.fullMode) UsPostMoreRow.SHOW_CONTROLS else UsPostMoreRow.CLEAR_SCREEN)
            add(UsPostMoreRow.QUALITY)
        }
    }
    val first = listOf(
        if (isBookmarked) UsPostMoreRow.UNSAVE else UsPostMoreRow.SAVE,
        UsPostMoreRow.COPY_LINK,
        UsPostMoreRow.SHARE,
    )
    if (isOwnPost) return listOfNotNull(reelGroup, first, listOf(UsPostMoreRow.DELETE))

    val second = buildList {
        if (reasonText.isNotBlank()) add(UsPostMoreRow.WHY)
        if (suggested) add(UsPostMoreRow.INTERESTED)
        // "Not interested" is about this post and applies to anyone; it is
        // not an unfollow.
        add(UsPostMoreRow.NOT_INTERESTED)
        // Muting an account you follow is what Unfollow is for, so the row
        // exists only while the author is not followed.
        if (followRow != UsPostMoreFollowRow.UNFOLLOW) add(UsPostMoreRow.DONT_RECOMMEND)
    }
    val third = buildList {
        when (followRow) {
            UsPostMoreFollowRow.UNFOLLOW -> add(UsPostMoreRow.UNFOLLOW)
            UsPostMoreFollowRow.FOLLOW -> add(UsPostMoreRow.FOLLOW)
            UsPostMoreFollowRow.HIDDEN -> Unit
        }
        add(UsPostMoreRow.BLOCK)
        add(UsPostMoreRow.REPORT)
    }
    return listOfNotNull(reelGroup, first, second, third)
}

/**
 * The reasons a report can carry, in the sheet's order, each with the token
 * trust-safety stores. Labels are what the reader picks; [wire] is the
 * contract.
 */
enum class UsReportReason(val label: String, val wire: String) {
    SPAM("Spam", "spam"),
    HARASSMENT("Harassment", "harassment"),
    NUDITY("Nudity or sexual content", "nudity"),
    VIOLENCE("Violence", "violence"),
    HATE("Hate speech", "hate"),
    FALSE_INFO("False information", "false_info"),
    SCAM("Scam or fraud", "scam_fraud"),
    IMPERSONATION("Impersonation", "impersonation"),
    SELF_HARM("Self-harm", "self_harm"),
    INTELLECTUAL_PROPERTY("Intellectual property", "intellectual_property"),
    OTHER("Other", "other"),
    ;

    /** Only "Other" asks for words; every other reason is complete on its own. */
    val asksForDetails: Boolean get() = this == OTHER
}

/**
 * The post's public address, for "Copy link".
 *
 * There is no canonical post URL on the server yet and no App Link in the
 * manifest (see [rememberPostSharer]), so this is the address the product
 * will own once both exist. It is assembled here, in ONE place, so the day
 * the payload carries a real link only this function changes.
 */
fun postShareLink(postId: String): String = "$POST_LINK_BASE$postId"

private const val POST_LINK_BASE = "https://momentum.app/p/"
