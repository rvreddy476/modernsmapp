package com.us.android.feature.notifications.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.core.model.NotificationTarget
import com.us.android.feature.notifications.ui.NotificationsScreen
import kotlinx.serialization.Serializable

/**
 * The notification inbox — Slice D.
 *
 * A pushed destination with no arguments. It always opens at the top of the
 * inbox and loads fresh: notifications are the surface where stale content is
 * most obviously wrong, and restoring a scroll position into a list that has
 * since gained rows puts the user somewhere arbitrary.
 */
@Serializable
data object NotificationsRoute

/**
 * Registers the inbox.
 *
 * [onOpenTarget] receives a resolved [NotificationTarget], never a URL. `:app`
 * decides which destination each target means, so this feature never learns
 * that a post target implies `:feature:post` and the two features stay
 * independent — the same contract the composer uses for `onPublished`.
 */
fun NavGraphBuilder.notificationsScreen(
    onBack: () -> Unit,
    onOpenTarget: (NotificationTarget) -> Unit,
    /**
     * Slice D / D-D7. Notification preferences live in `:feature:profile`;
     * `:app` owns which destination that is, so this feature never imports it.
     */
    onOpenPreferences: () -> Unit,
) {
    composable<NotificationsRoute> {
        NotificationsScreen(
            onBack = onBack,
            onOpenTarget = onOpenTarget,
            onOpenPreferences = onOpenPreferences,
        )
    }
}

/** Type-safe navigation to the inbox. */
fun NavController.navigateToNotifications() = navigate(NotificationsRoute)
