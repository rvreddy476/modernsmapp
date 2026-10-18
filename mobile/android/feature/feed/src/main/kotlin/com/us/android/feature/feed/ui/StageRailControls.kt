package com.us.android.feature.feed.ui

import androidx.compose.runtime.Composable
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.engagement.data.likeCountOr
import com.us.android.core.engagement.data.reactedOr
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.StageRailControl
import com.us.android.feature.feed.ui.reels.RailKind
import com.us.android.feature.feed.ui.reels.layeredSaves
import com.us.android.feature.feed.ui.reels.railControls

/**
 * The rail's controls for a post, drawn the same way on Home and on Reels
 * (2026-10-18): WHICH controls and what each says is [railControls]'s rule
 * — the author's switches, the counts with this session's taps layered in —
 * and this only gives each its glyph, its colour and its tap.
 */
@Composable
internal fun stageRailControls(
    item: FeedItem,
    overlay: EngagementOverlay,
    onReact: (postId: String, serverReacted: Boolean) -> Unit,
    onComment: (postId: String) -> Unit,
    onBookmark: (postId: String, serverBookmarked: Boolean) -> Unit,
    onShare: (FeedItem) -> Unit,
): List<StageRailControl> {
    val reacted = overlay.reactedOr(item.viewer.hasReacted)
    val bookmarked = overlay.bookmarkedOr(item.viewer.isBookmarked)
    val controls = railControls(
        controls = item.controls,
        likes = overlay.likeCountOr(item.counts.likes, item.viewer.hasReacted),
        comments = item.counts.comments,
        saved = bookmarked,
        shares = item.counts.shares,
        saves = layeredSaves(item.counts.saves, serverSaved = item.viewer.isBookmarked, saved = bookmarked),
    )
    return controls.map { control ->
        when (control.kind) {
            RailKind.LIKE -> StageRailControl(
                key = "like",
                icon = if (reacted) UsIcons.HeartFilled else UsIcons.HeartOutline,
                description = if (reacted) "Liked" else "Like",
                label = control.label,
                tint = if (reacted) UsTheme.extended.liveRed else null,
                onClick = { onReact(item.id, item.viewer.hasReacted) },
            )
            RailKind.COMMENT -> StageRailControl(
                key = "comments",
                icon = UsIcons.Comment,
                description = "Comments",
                label = control.label,
                onClick = { onComment(item.id) },
            )
            RailKind.SAVE -> StageRailControl(
                key = if (bookmarked) "saved" else "save",
                icon = if (bookmarked) UsIcons.BookmarkFilled else UsIcons.BookmarkOutline,
                description = if (bookmarked) "Saved" else "Save",
                label = control.label,
                tint = if (bookmarked) UsTheme.extended.accent else null,
                onClick = { onBookmark(item.id, item.viewer.isBookmarked) },
            )
            RailKind.SHARE -> StageRailControl(
                key = "share",
                icon = UsIcons.Share,
                description = "Share",
                label = control.label,
                onClick = { onShare(item) },
            )
        }
    }
}
