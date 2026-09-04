package com.us.android.core.feed.ui.more

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.UsPostDeleteState
import com.us.android.core.ui.UsPostDontRecommendState
import com.us.android.core.ui.UsPostMoreCallbacks
import com.us.android.core.ui.UsPostMoreFollowRow
import com.us.android.core.ui.UsPostMoreSheet
import com.us.android.core.ui.UsPostMoreState
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.UsReelMoreState
import com.us.android.core.ui.UsReelQuality
import com.us.android.core.ui.postShareLink

/**
 * The "more" sheet, bound to [PostMoreViewModel] for one [item].
 *
 * Every feed surface mounts exactly this when its ⋮ is tapped, so the
 * mapping from a row to a ViewModel call exists once. [onShare] stays with
 * the host because the system chooser needs an Activity context the
 * ViewModel must not hold.
 */
@Suppress("LongParameterList")
@Composable
fun PostMoreSheetHost(
    item: FeedItem,
    overlay: EngagementOverlay,
    followEdge: FollowStatus?,
    ownUserId: String,
    onShare: (FeedItem) -> Unit,
    onDismiss: () -> Unit,
    viewModel: PostMoreViewModel,
    /** Set by Reels alone: the group above the rows, and the two things only a reel can do. */
    reel: UsReelMoreState? = null,
    onClearScreen: () -> Unit = {},
    onSelectQuality: (UsReelQuality) -> Unit = {},
    /**
     * Overrides whether the post reads as a suggestion. Null derives it from
     * the row's reason, as every feed does; Tube's watch screen passes false —
     * a video the viewer chose to open is not something to say "Interested"
     * about.
     */
    suggested: Boolean? = null,
) {
    val report by viewModel.report.collectAsStateWithLifecycle()
    val delete by viewModel.delete.collectAsStateWithLifecycle()
    val dontRecommend by viewModel.dontRecommend.collectAsStateWithLifecycle()
    LaunchedEffect(item.id) { viewModel.opened() }

    val callbacks = remember(item, viewModel, onShare, onClearScreen, onSelectQuality) {
        UsPostMoreCallbacks(
            onToggleSave = { viewModel.toggleSave(item) },
            onShare = { onShare(item) },
            onInterested = { viewModel.interested(item) },
            onNotInterested = { viewModel.notInterested(item) },
            onDontRecommend = { viewModel.dontRecommend(item) },
            onFollow = { viewModel.follow(item.author.id) },
            onUnfollow = { viewModel.unfollow(item.author.id) },
            onBlock = { viewModel.block(item) },
            onReport = { reason, details -> viewModel.report(item, reason, details) },
            onDelete = { viewModel.delete(item) },
            onClearScreen = onClearScreen,
            onSelectQuality = onSelectQuality,
        )
    }
    UsPostMoreSheet(
        state = item.toMoreState(overlay, followEdge, ownUserId, report, delete, reel, dontRecommend, suggested),
        callbacks = callbacks,
        onDismiss = onDismiss,
    )
}

/**
 * The sheet's state for one row: the server's values with this session's
 * bookmark tap layered in, and the relationship row decided by the graph.
 */
@Suppress("LongParameterList")
fun FeedItem.toMoreState(
    overlay: EngagementOverlay,
    followEdge: FollowStatus?,
    ownUserId: String,
    report: UsPostReportState = UsPostReportState.Idle,
    delete: UsPostDeleteState = UsPostDeleteState.Idle,
    reel: UsReelMoreState? = null,
    dontRecommend: UsPostDontRecommendState = UsPostDontRecommendState.Idle,
    suggested: Boolean? = null,
): UsPostMoreState {
    val own = ownUserId.isNotBlank() && author.id == ownUserId
    return UsPostMoreState(
        postId = id,
        username = author.username?.takeIf { it.isNotBlank() } ?: author.nameForDisplay,
        isOwnPost = own,
        isBookmarked = overlay.bookmarkedOr(viewer.isBookmarked),
        followRow = if (own) UsPostMoreFollowRow.HIDDEN else moreFollowRow(followEdge),
        reasonText = reasonText,
        // "following" and "connection" are the server's two "you asked for
        // this" reasons; everything else was suggested — unless the host
        // knows better (Tube's watch screen).
        suggested = suggested ?: (reason != "following" && reason != "connection"),
        link = postShareLink(id),
        report = report,
        delete = delete,
        dontRecommend = dontRecommend,
        reel = reel,
    )
}

/**
 * Unfollow when the viewer follows (or has asked to — a pending request is
 * undone the same way), Follow when they are known not to, nothing while
 * the edge is unknown. The same "known, not guessed" rule as `offersFollow`.
 */
fun moreFollowRow(edge: FollowStatus?): UsPostMoreFollowRow = when (edge) {
    FollowStatus.FOLLOWING, FollowStatus.REQUESTED -> UsPostMoreFollowRow.UNFOLLOW
    FollowStatus.NONE -> UsPostMoreFollowRow.FOLLOW
    null -> UsPostMoreFollowRow.HIDDEN
}
