package com.us.android.feature.feed.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsMomentumHeader
import com.us.android.core.notifications.ui.UnreadBadgeViewModel

/**
 * The Momentum header with its live unread count, for the feed module's
 * three top-level pages — Home, Friends and Reels. The pure layout is
 * [UsMomentumHeader]; this only binds the badge.
 *
 * ## WHY THE BADGE IS A COUNT AND NOT A DOT
 *
 * A dot says "something happened". A number says how much, which is what
 * decides whether the user opens it now or later. Above 99 the badge shows
 * "99+": the exact number stops being useful long before it stops being
 * renderable, and a four-digit badge overflows the icon.
 *
 * The count is refreshed when the page appears rather than polled. Polling a
 * count on a timer costs a request per interval per user forever, and these
 * pages are looked at often enough that the badge is never meaningfully stale.
 */
@Composable
internal fun MomentumHeader(
    onOpenSearch: () -> Unit,
    onOpenMessages: () -> Unit,
    onOpenNotifications: () -> Unit,
    modifier: Modifier = Modifier,
    translucent: Boolean = false,
    showWordmark: Boolean = true,
    onHomeClick: () -> Unit = {},
    viewModel: UnreadBadgeViewModel = hiltViewModel(),
) {
    val count by viewModel.count.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) { viewModel.refresh() }

    UsMomentumHeader(
        unreadCount = count,
        onSearch = onOpenSearch,
        onMessages = onOpenMessages,
        onNotifications = onOpenNotifications,
        modifier = modifier,
        onHomeClick = onHomeClick,
        translucent = translucent,
        showWordmark = showWordmark,
    )
}
