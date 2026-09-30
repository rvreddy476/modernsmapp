package com.us.android.feature.feed.ui.reels

import com.us.android.core.model.FeedPostControls
import com.us.android.core.ui.formatCount

/**
 * The rail's controls, in order, each with the label under its glyph
 * (founder, 2026-09-04, from YouTube Shorts: every control names itself).
 *
 * Like and Comment carry their COUNT as the label — "8.8K" — and fall back
 * to the noun when there is nothing to count, because "0" under a heart
 * reads as a score and "Like" reads as an invitation. Share and Save carry
 * theirs too WHEN THE ROW SENDS ONE (2026-09-30, as the web's rail does);
 * a row that does not say reads "Share", and "Save" or "Saved". Share
 * follows the author's `hide_share` switch (see [railVisibility]). Mute is not in this list: it sits under the rail
 * without a label, a setting rather than an action on the reel.
 *
 * There is no More here any more (founder, 2026-09-05): the reel's More
 * sheet opens from the hamburger in the header, so the rail is the four
 * actions on the reel itself and nothing that is about the app.
 */
enum class RailKind { LIKE, COMMENT, SHARE, SAVE }

data class RailControl(val kind: RailKind, val label: String)

/**
 * @param likes the like count with this session's tap already layered in.
 * @param comments the comment count.
 * @param saved whether the reel is bookmarked, this session's tap included.
 * @param shares the share count, or null when the row does not carry one.
 * @param saves the save count with this session's tap already layered in
 *   ([layeredSaves]), or null when the row does not carry one.
 */
fun railControls(
    controls: FeedPostControls,
    likes: Int,
    comments: Int,
    saved: Boolean,
    shares: Int? = null,
    saves: Int? = null,
): List<RailControl> {
    val visible = controls.railVisibility()
    return buildList {
        add(RailControl(RailKind.LIKE, railCountLabel(likes, "Like")))
        if (visible.showComment) add(RailControl(RailKind.COMMENT, railCountLabel(comments, "Comment")))
        if (visible.showShare) add(RailControl(RailKind.SHARE, railCountLabel(shares ?: 0, "Share")))
        add(RailControl(RailKind.SAVE, railCountLabel(saves ?: 0, if (saved) "Saved" else "Save")))
    }
}

/**
 * The row's save count with this session's tap layered in: one more when the
 * viewer saved a reel the server still has as unsaved, one fewer the other
 * way, never below zero. Null stays null — a count the server never gave is
 * not invented from a tap.
 */
fun layeredSaves(serverSaves: Int?, serverSaved: Boolean, saved: Boolean): Int? {
    val count = serverSaves ?: return null
    val delta = when {
        saved == serverSaved -> 0
        saved -> 1
        else -> -1
    }
    return (count + delta).coerceAtLeast(0)
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
