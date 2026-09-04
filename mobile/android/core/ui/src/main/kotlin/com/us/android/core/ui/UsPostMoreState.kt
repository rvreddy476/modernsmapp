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
    /** What "Copy link" puts on the clipboard. */
    val link: String,
    val report: UsPostReportState = UsPostReportState.Idle,
    /** The viewer's own post: where the delete stands, owned by whoever sends it. */
    val delete: UsPostDeleteState = UsPostDeleteState.Idle,
    /** True while a one-shot action (block) is on the wire; the rows go inert. */
    val busy: Boolean = false,
)

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

/** One row of the sheet's menu. The order within [rowGroups] is the design's. */
enum class UsPostMoreRow(val label: String) {
    SAVE("Save"),
    UNSAVE("Unsave"),
    COPY_LINK("Copy link"),
    SHARE("Share"),
    WHY("Why you're seeing this post"),
    INTERESTED("Interested"),
    NOT_INTERESTED("Not interested"),
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
 *    when the server sent a sentence, then Interested and Not interested.
 *  - Group 3, other people's posts: Unfollow or Follow when the edge is
 *    known, Block, and Report last.
 *  - The viewer's own post: group 1, then "Delete post" alone, red and
 *    last — a soft delete with a 30-day restore window (founder, 2026-09-04).
 */
fun UsPostMoreState.rowGroups(): List<List<UsPostMoreRow>> {
    val first = listOf(
        if (isBookmarked) UsPostMoreRow.UNSAVE else UsPostMoreRow.SAVE,
        UsPostMoreRow.COPY_LINK,
        UsPostMoreRow.SHARE,
    )
    if (isOwnPost) return listOf(first, listOf(UsPostMoreRow.DELETE))

    val second = buildList {
        if (reasonText.isNotBlank()) add(UsPostMoreRow.WHY)
        add(UsPostMoreRow.INTERESTED)
        add(UsPostMoreRow.NOT_INTERESTED)
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
    return listOf(first, second, third)
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
