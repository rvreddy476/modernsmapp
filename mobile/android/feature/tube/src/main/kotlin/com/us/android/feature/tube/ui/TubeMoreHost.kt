package com.us.android.feature.tube.ui

import androidx.compose.foundation.layout.BoxScope
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.analytics.AnalyticsSurface
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.feed.ui.more.PostMoreSheetHost
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.rememberPostSharer
import kotlinx.coroutines.flow.StateFlow

/**
 * The app's shared "more" sheet, mounted over a Tube page when a ⋮ is
 * tapped, and the message strip its actions report through. Never a
 * second copy of the sheet: every feed surface mounts exactly this.
 */
@Composable
fun BoxScope.TubeMoreHost(
    state: TubeMoreState,
    overlays: StateFlow<Map<String, EngagementOverlay>>,
    followEdges: StateFlow<Map<String, FollowStatus>>,
    ownUserId: String,
    more: PostMoreViewModel,
) {
    val overlayMap by overlays.collectAsStateWithLifecycle()
    val edges by followEdges.collectAsStateWithLifecycle()
    val message by more.message.collectAsStateWithLifecycle()
    val share = rememberPostSharer()

    UsMessageHost(message = message, onDismiss = more::dismissMessage)
    state.item?.let { item ->
        PostMoreSheetHost(
            item = item,
            overlay = overlayMap[item.id] ?: EngagementOverlay(),
            followEdge = edges[item.author.id],
            ownUserId = ownUserId,
            onShare = { shared -> share(shared.title.ifBlank { shared.text }, shared.author.nameForDisplay) },
            onDismiss = state::close,
            viewModel = more,
            suggested = state.suggested,
            surface = AnalyticsSurface.POSTTUBE,
        )
    }
}
