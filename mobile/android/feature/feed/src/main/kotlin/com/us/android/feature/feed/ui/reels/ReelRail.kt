package com.us.android.feature.feed.ui.reels

import com.us.android.core.model.FeedPostControls
import com.us.android.core.ui.formatCount

/**
 * The rail's controls, in order, each with the label under its glyph
 * (founder, 2026-09-04, from YouTube Shorts: every control names itself).
 *
 * Like and Comment carry their COUNT as the label — "8.8K" — and fall back
 * to the noun when there is nothing to count, because "0" under a heart
 * reads as a score and "Like" reads as an invitation. Save says whether it
 * is done. Share follows the author's `hide_share` switch (see
 * [railVisibility]) and More is there when the surface has a sheet to open.
 * Mute is not in this list: it sits under the rail without a label, a
 * setting rather than an action on the reel.
 */
enum class RailKind { LIKE, COMMENT, SHARE, SAVE, MORE }

data class RailControl(val kind: RailKind, val label: String)

/**
 * @param likes the like count with this session's tap already layered in.
 * @param comments the comment count.
 * @param saved whether the reel is bookmarked, this session's tap included.
 * @param offersMore whether the surface has a "more" sheet to open.
 */
fun railControls(
    controls: FeedPostControls,
    likes: Int,
    comments: Int,
    saved: Boolean,
    offersMore: Boolean,
): List<RailControl> {
    val visible = controls.railVisibility()
    return buildList {
        add(RailControl(RailKind.LIKE, railCountLabel(likes, "Like")))
        if (visible.showComment) add(RailControl(RailKind.COMMENT, railCountLabel(comments, "Comment")))
        if (visible.showShare) add(RailControl(RailKind.SHARE, "Share"))
        add(RailControl(RailKind.SAVE, if (saved) "Saved" else "Save"))
        if (offersMore) add(RailControl(RailKind.MORE, "More"))
    }
}

/** The compact count when there is one, the control's own name when there is not. */
fun railCountLabel(count: Int, noun: String): String = if (count > 0) formatCount(count) else noun

/**
 * The author line under the reel: "@handle" when the account has one, the
 * display name otherwise — the same fallback the feed card's header makes
 * (`PostCardState.username`), with the "@" only where there is a handle to
 * put it on. "@Ada Lovelace" is not a thing.
 */
fun reelAuthorLabel(username: String?, displayName: String): String {
    val handle = username?.removePrefix("@")?.takeIf { it.isNotBlank() }
    return if (handle != null) "@$handle" else displayName
}

/**
 * How far through the reel the playhead is, 0..1, for the line along the
 * bottom of the frame. An unknown duration (still loading, or live) is 0,
 * never a division by zero; a position past the end clamps to full rather
 * than drawing off the frame.
 */
fun progressFraction(positionMillis: Long, durationMillis: Long): Float {
    if (durationMillis <= 0L || positionMillis <= 0L) return 0f
    return (positionMillis.toDouble() / durationMillis).toFloat().coerceIn(0f, 1f)
}
