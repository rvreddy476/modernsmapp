// MatchingDeclarationName: this file is the feature's navigation contract —
// the route types plus the graph and navigation extensions that use them.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.live.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.live.ui.GoLiveScreen
import com.us.android.feature.live.ui.LiveHubScreen
import com.us.android.feature.live.ui.LiveWatchScreen
import kotlinx.serialization.Serializable

/** The live hub: who is live now, and the door to going live yourself. */
@Serializable
data object LiveHubRoute

/** The broadcaster surface. */
@Serializable
data object GoLiveRoute

/** Watching one stream. The id key must match LiveWatchViewModel's handle. */
@Serializable
data class LiveWatchRoute(val streamId: String)

/** Registers the three live destinations. */
fun NavGraphBuilder.liveScreens(
    onBack: () -> Unit,
    onGoLive: () -> Unit,
    onWatch: (streamId: String) -> Unit,
) {
    composable<LiveHubRoute> {
        LiveHubScreen(onClose = onBack, onGoLive = onGoLive, onWatch = onWatch)
    }
    composable<GoLiveRoute> {
        GoLiveScreen(onClose = onBack)
    }
    composable<LiveWatchRoute> {
        LiveWatchScreen(onClose = onBack)
    }
}

/** Type-safe navigation into live. */
fun NavController.navigateToLiveHub() = navigate(LiveHubRoute)

fun NavController.navigateToGoLive() = navigate(GoLiveRoute)

fun NavController.navigateToLiveWatch(streamId: String) = navigate(LiveWatchRoute(streamId))
