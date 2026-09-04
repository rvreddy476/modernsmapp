// MatchingDeclarationName: this file is the feature's navigation contract.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.tube.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.tube.ui.home.TubeHomeScreen
import com.us.android.feature.tube.ui.watch.WatchScreen
import kotlinx.serialization.Serializable

/** Tube home — the long-video list. Pushed from the Explore launcher, so it wears a back arrow. */
@Serializable
data object TubeHomeRoute

/** One video, playing. Pushed over Tube home; [postId] is the post to open on. */
@Serializable
data class WatchRoute(val postId: String)

/**
 * Registers Tube's two destinations.
 *
 * Every callback that leaves the feature is `:app`'s to resolve — a profile,
 * the search placeholder — and so is the hop from the list to a video:
 * [onOpenVideo] is handed to `:app` so the feature never holds a
 * NavController, the same shape every other feature keeps.
 */
fun NavGraphBuilder.tubeScreens(
    onBack: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    /** The home bar's search glyph; `:app` opens Explore scoped to videos. */
    onOpenSearch: () -> Unit,
    /** A card was tapped; `:app` pushes [WatchRoute]. */
    onOpenVideo: (postId: String) -> Unit,
) {
    composable<TubeHomeRoute> {
        TubeHomeScreen(
            onBack = onBack,
            onOpenSearch = onOpenSearch,
            onOpenVideo = onOpenVideo,
        )
    }
    composable<WatchRoute> {
        WatchScreen(
            onBack = onBack,
            onOpenAuthor = onOpenAuthor,
        )
    }
}

/** Type-safe navigation to Tube home. */
fun NavController.navigateToTube() = navigate(TubeHomeRoute)

/** Type-safe navigation to one video. */
fun NavController.navigateToWatch(postId: String) = navigate(WatchRoute(postId))
