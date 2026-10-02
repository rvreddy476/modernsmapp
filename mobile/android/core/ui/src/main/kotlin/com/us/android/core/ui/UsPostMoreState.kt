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
    /**
     * Present when the sheet was opened from a LONG VIDEO (2026-10-02): the
     * rows are then the web watch page's More menu and nothing else. Null on
     * a feed card and on a reel.
     */
    val longVideo: UsLongVideoMoreState? = null,
) {
    /** Which of the three menus this is. A long video wins over a reel; a sheet is never both. */
    val surface: UsPostMoreSurface
        get() = when {
            longVideo != null -> UsPostMoreSurface.LONG_VIDEO
            reel != null -> UsPostMoreSurface.REEL
            else -> UsPostMoreSurface.POST
        }
}

/**
 * Where the sheet was opened from. Each surface has its own list of rows,
 * and the reel's and the long video's are the web's (founder, 2026-10-02:
 * "the same rows and words as the web").
 */
enum class UsPostMoreSurface { POST, REEL, LONG_VIDEO }

/** What the long video's menu needs beyond the post's own state. */
@Immutable
data class UsLongVideoMoreState(
    /** The channel's name: "Block <channel>" names the channel, not the handle, as the web does. */
    val channelName: String,
    /** The creator turned sharing off (`hide_share`): no Share row. */
    val shareHidden: Boolean = false,
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
    /**
     * "Use this sound" is offered (original sounds, 2026-09-30): the creator
     * allows reuse or the reel is the viewer's own, and it is not still
     * processing. The host decides — `FeedItem.canUseSound` — the sheet only
     * draws the row.
     */
    val canUseSound: Boolean = false,
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

/** One row of the sheet's menu. The order they are DRAWN in is [rows]'s: alphabetical, by the label shown. */
enum class UsPostMoreRow(val label: String) {
    /** Reels only: the full caption, unfolded inline. */
    DESCRIPTION("Description"),

    /** Reels only: full mode — the header and the bar go, the reel stays. */
    CLEAR_SCREEN("Clear screen"),

    /** Reels only: [CLEAR_SCREEN]'s other face, while full mode is on. */
    SHOW_CONTROLS("Show controls"),

    /** Reels only: the rendition picker, the current choice at the right. */
    QUALITY("Quality"),

    /** Reels only: make a reel with this reel's sound. */
    USE_SOUND("Use this sound"),
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
 * The rows to draw: ONE list, in ascending alphabetical order by the label
 * the viewer reads, case-insensitive. No groups, and no dividers.
 *
 * founder, 2026-09-30: the More sheet is one list in alphabetical order,
 * like the web, wherever the sheet is used — feed posts, reels and long
 * video. It replaces the three groups taken from the Instagram capture
 * (2026-09-04) and the reel's own group above them (YouTube Shorts,
 * 2026-09-04). The order is by the label actually SHOWN ([labelOf]), so
 * "Don't recommend @user" sorts under D and "Unfollow @user" under U.
 *
 * WHICH rows appear is [offeredRows]'s decision.
 */
fun UsPostMoreState.rows(): List<UsPostMoreRow> =
    offeredRows().sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER) { labelOf(it) })

/**
 * Which rows this post offers this viewer, in no particular order — [rows]
 * orders them. One list per [UsPostMoreSurface].
 */
internal fun UsPostMoreState.offeredRows(): List<UsPostMoreRow> = when (surface) {
    UsPostMoreSurface.LONG_VIDEO -> longVideoRows()
    UsPostMoreSurface.REEL -> reelRows()
    UsPostMoreSurface.POST -> postRows()
}

/**
 * A feed post:
 *
 *  - Always: Save or Unsave, Copy link, Share.
 *  - OTHER people's posts: "Why you're seeing this post" only when the
 *    server sent a sentence; Interested only for a suggestion; Not
 *    interested; "Don't recommend @user" (founder, 2026-09-04, from YouTube's
 *    "Don't recommend channel") while the author is not followed; Unfollow
 *    or Follow when the edge is known; Block; Report.
 *  - The viewer's own post: "Delete post" — a soft delete with a 30-day
 *    restore window (founder, 2026-09-04) — and none of the rows that act on
 *    "the author".
 */
private fun UsPostMoreState.postRows(): List<UsPostMoreRow> = buildList {
    add(if (isBookmarked) UsPostMoreRow.UNSAVE else UsPostMoreRow.SAVE)
    add(UsPostMoreRow.COPY_LINK)
    add(UsPostMoreRow.SHARE)
    if (isOwnPost) {
        add(UsPostMoreRow.DELETE)
        return@buildList
    }

    if (reasonText.isNotBlank()) add(UsPostMoreRow.WHY)
    if (suggested) add(UsPostMoreRow.INTERESTED)
    // "Not interested" is about this post and applies to anyone; it is
    // not an unfollow.
    add(UsPostMoreRow.NOT_INTERESTED)
    // Muting an account you follow is what Unfollow is for, so the row
    // exists only while the author is not followed.
    if (followRow != UsPostMoreFollowRow.UNFOLLOW) add(UsPostMoreRow.DONT_RECOMMEND)
    when (followRow) {
        UsPostMoreFollowRow.UNFOLLOW -> add(UsPostMoreRow.UNFOLLOW)
        UsPostMoreFollowRow.FOLLOW -> add(UsPostMoreRow.FOLLOW)
        UsPostMoreFollowRow.HIDDEN -> Unit
    }
    add(UsPostMoreRow.BLOCK)
    add(UsPostMoreRow.REPORT)
}

/**
 * A REEL: the web's More menu (founder, 2026-10-02; `moreMenuItems` in the
 * web's `features/reels/menu.ts`). Description when there is a caption to
 * unfold, Quality, "Use this sound" when the host offers it; on another
 * person's reel also Not interested, "Don't recommend this channel" and
 * Report. Nothing else.
 *
 * What the web's menu has and this one does not: Audio track, Auto scroll,
 * Captions and Playback speed. The Android reel player has none of those
 * settings yet, and a row with nothing behind it is not shown.
 *
 * What this menu had before and the web's does not, so it left: Save, Share
 * (both are on the rail), Copy link, Clear screen (a double tap does it),
 * "Why you're seeing this post", Interested, Follow and Unfollow (the author
 * row has Follow), Block, and "Delete post" on the viewer's own reel. Their
 * rows and handlers are kept, as the web keeps its own, so offering one
 * again is one line here.
 */
private fun UsPostMoreState.reelRows(): List<UsPostMoreRow> = buildList {
    val reel = reel ?: return@buildList
    if (reel.description.isNotBlank()) add(UsPostMoreRow.DESCRIPTION)
    add(UsPostMoreRow.QUALITY)
    if (reel.canUseSound) add(UsPostMoreRow.USE_SOUND)
    if (!isOwnPost) {
        add(UsPostMoreRow.NOT_INTERESTED)
        add(UsPostMoreRow.DONT_RECOMMEND)
        add(UsPostMoreRow.REPORT)
    }
}

/**
 * A LONG VIDEO: the web watch page's More menu (founder, 2026-10-02;
 * `watchMoreMenuRows` in the web's `WatchMoreMenu.tsx`). A viewer sees
 * "Block <channel>", "Don't recommend this channel", Not interested, Report
 * and Share; the owner sees Delete and Share. Share goes when the creator
 * turned sharing off.
 *
 * What the web's menu has and this one does not: "Keep a copy" (a download;
 * the app has no download flow) and the owner's "Audio tracks" and "Edit"
 * (the app has neither screen). Save, Watch later and "Add to collection"
 * are on the action row under the video, as on the web, not in this menu.
 */
private fun UsPostMoreState.longVideoRows(): List<UsPostMoreRow> = buildList {
    if (longVideo?.shareHidden != true) add(UsPostMoreRow.SHARE)
    if (isOwnPost) {
        add(UsPostMoreRow.DELETE)
        return@buildList
    }
    add(UsPostMoreRow.BLOCK)
    add(UsPostMoreRow.DONT_RECOMMEND)
    add(UsPostMoreRow.NOT_INTERESTED)
    add(UsPostMoreRow.REPORT)
}

/**
 * What the row prints on THIS sheet, and therefore what it is sorted by.
 *
 * On a feed post the rows that act on the author carry the handle
 * ([menuLabel]). On a reel and a long video the words are the web's: "Don't
 * recommend this channel"; and on a long video "Block <channel name>" and a
 * plain "Delete".
 */
fun UsPostMoreState.labelOf(row: UsPostMoreRow): String = when (surface) {
    UsPostMoreSurface.POST -> row.menuLabel(username)
    UsPostMoreSurface.REEL -> when (row) {
        UsPostMoreRow.DONT_RECOMMEND -> DONT_RECOMMEND_CHANNEL
        else -> row.menuLabel(username)
    }
    UsPostMoreSurface.LONG_VIDEO -> when (row) {
        UsPostMoreRow.DONT_RECOMMEND -> DONT_RECOMMEND_CHANNEL
        UsPostMoreRow.BLOCK -> "${row.label} ${longVideo?.channelName?.takeIf { it.isNotBlank() } ?: "@$username"}"
        UsPostMoreRow.DELETE -> "Delete"
        else -> row.menuLabel(username)
    }
}

/** The web's words, on a reel and a long video alike. */
private const val DONT_RECOMMEND_CHANNEL = "Don't recommend this channel"

/**
 * What the row prints on a feed post: the rows that act on the author carry
 * the handle — "Unfollow @user", "Block @user", "Don't recommend @user" — the
 * rest their own label. [labelOf] is what the sheet draws and sorts by.
 */
fun UsPostMoreRow.menuLabel(username: String): String = when (this) {
    UsPostMoreRow.DONT_RECOMMEND, UsPostMoreRow.UNFOLLOW, UsPostMoreRow.BLOCK -> "$label @$username"
    else -> label
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
