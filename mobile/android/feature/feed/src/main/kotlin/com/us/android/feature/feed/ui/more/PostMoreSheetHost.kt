package com.us.android.feature.feed.ui.more

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.UsPostMoreCallbacks
import com.us.android.core.ui.UsPostMoreFollowRow
import com.us.android.core.ui.UsPostMoreSheet
import com.us.android.core.ui.UsPostMoreState
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.postShareLink

/**
 * The "more" sheet, bound to [PostMoreViewModel] for one [item].
 *
 * Every feed surface mounts exactly this when its ⋮ is tapped, so the
 * mapping from a row to a ViewModel call exists once. [onShare] stays with
 * the host because the system chooser needs an Activity context the
 * ViewModel must not hold.
 */
@Composable
internal fun PostMoreSheetHost(
    item: FeedItem,
    overlay: EngagementOverlay,
    followEdge: FollowStatus?,
    ownUserId: String,
    onShare: (FeedItem) -> Unit,
    onDismiss: () -> Unit,
    viewModel: PostMoreViewModel,
) {
    val report by viewModel.report.collectAsStateWithLifecycle()
    LaunchedEffect(item.id) { viewModel.opened() }

    val callbacks = remember(item, viewModel, onShare) {
        UsPostMoreCallbacks(
            onToggleSave = { viewModel.toggleSave(item) },
            onShare = { onShare(item) },
            onInterested = { viewModel.interested(item) },
            onNotInterested = { viewModel.notInterested(item) },
            onFollow = { viewModel.follow(item.author.id) },
            onUnfollow = { viewModel.unfollow(item.author.id) },
            onBlock = { viewModel.block(item) },
            onReport = { reason, details -> viewModel.report(item, reason, details) },
        )
    }
    UsPostMoreSheet(
        state = item.toMoreState(overlay, followEdge, ownUserId, report),
        callbacks = callbacks,
        onDismiss = onDismiss,
    )
}

/**
 * The sheet's state for one row: the server's values with this session's
 * bookmark tap layered in, and the relationship row decided by the graph.
 */
internal fun FeedItem.toMoreState(
    overlay: EngagementOverlay,
    followEdge: FollowStatus?,
    ownUserId: String,
    report: UsPostReportState = UsPostReportState.Idle,
): UsPostMoreState {
    val own = ownUserId.isNotBlank() && author.id == ownUserId
    return UsPostMoreState(
        postId = id,
        username = author.username?.takeIf { it.isNotBlank() } ?: author.nameForDisplay,
        isOwnPost = own,
        isBookmarked = overlay.bookmarkedOr(viewer.isBookmarked),
        followRow = if (own) UsPostMoreFollowRow.HIDDEN else moreFollowRow(followEdge),
        reasonText = reasonText,
        link = postShareLink(id),
        report = report,
    )
}

/**
 * Unfollow when the viewer follows (or has asked to — a pending request is
 * undone the same way), Follow when they are known not to, nothing while
 * the edge is unknown. The same "known, not guessed" rule as `offersFollow`.
 */
internal fun moreFollowRow(edge: FollowStatus?): UsPostMoreFollowRow = when (edge) {
    FollowStatus.FOLLOWING, FollowStatus.REQUESTED -> UsPostMoreFollowRow.UNFOLLOW
    FollowStatus.NONE -> UsPostMoreFollowRow.FOLLOW
    null -> UsPostMoreFollowRow.HIDDEN
}
