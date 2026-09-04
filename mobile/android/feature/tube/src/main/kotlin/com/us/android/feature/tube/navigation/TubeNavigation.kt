// MatchingDeclarationName: this file is the feature's navigation contract.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.tube.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.tube.ui.TubeBarAction
import com.us.android.feature.tube.ui.TubeTab
import com.us.android.feature.tube.ui.home.TubeHomeScreen
import com.us.android.feature.tube.ui.subscriptions.SubscriptionsScreen
import com.us.android.feature.tube.ui.watch.WatchScreen
import com.us.android.feature.tube.ui.you.YouScreen
import kotlinx.serialization.Serializable

/**
 * Tube home — the long-video page. Pushed from the Explore launcher; it is
 * the mini-app's root, so it wears no back arrow: the system Back leaves
 * Tube (founder, 2026-09-05).
 */
@Serializable
data object TubeHomeRoute

/** The Subscriptions page: videos from authors the viewer follows. */
@Serializable
data object TubeSubscriptionsRoute

/** The You page: the viewer's own videos, what they left unfinished, what they saved. */
@Serializable
data object TubeYouRoute

/** One video, playing. Pushed over a Tube page; [postId] is the post to open on. */
@Serializable
data class WatchRoute(val postId: String)

/**
 * Every way out of Tube, and the one way around it. All of it is `:app`'s
 * to resolve — the feature never holds a NavController, the shape every
 * other feature keeps — including the bar's switch between Tube's own
 * pages, which `:app` answers with [navigateToTubeTab].
 */
data class TubeDestinations(
    val onBack: () -> Unit,
    val onOpenAuthor: (userId: String) -> Unit,
    /** The header's search glyph; `:app` opens Explore scoped to videos. */
    val onOpenSearch: () -> Unit,
    /** A card was tapped; `:app` pushes [WatchRoute]. */
    val onOpenVideo: (postId: String) -> Unit,
    /** The header's bell. */
    val onOpenNotifications: () -> Unit,
    /** The compass at the head of the chip rail: the app's launcher. */
    val onOpenExplore: () -> Unit,
    /**
     * A short was tapped, or the bar's Shorts slot: the app's Reels tab.
     * The screen has already left the post id in `ReelsEntry` when a
     * short was tapped, the same handoff the Home feed makes.
     */
    val onOpenReels: () -> Unit,
    /** The bar's "+": the Create hub on its Video surface. */
    val onCreateVideo: () -> Unit,
    /** The bar's Home / Subscriptions / You. */
    val onOpenTab: (TubeTab) -> Unit,
) {
    /** The bar's tap, resolved. */
    fun onBarAction(action: TubeBarAction) = when (action) {
        is TubeBarAction.OpenTab -> onOpenTab(action.tab)
        TubeBarAction.OpenReels -> onOpenReels()
        TubeBarAction.CreateVideo -> onCreateVideo()
    }
}

/** Registers Tube's four destinations. */
fun NavGraphBuilder.tubeScreens(destinations: TubeDestinations) {
    composable<TubeHomeRoute> { TubeHomeScreen(destinations = destinations) }
    composable<TubeSubscriptionsRoute> { SubscriptionsScreen(destinations = destinations) }
    composable<TubeYouRoute> { YouScreen(destinations = destinations) }
    composable<WatchRoute> {
        WatchScreen(
            onBack = destinations.onBack,
            onOpenAuthor = destinations.onOpenAuthor,
        )
    }
}

/** Type-safe navigation to Tube home. */
fun NavController.navigateToTube() = navigate(TubeHomeRoute)

/** Type-safe navigation to one video. */
fun NavController.navigateToWatch(postId: String) = navigate(WatchRoute(postId))

/**
 * The bar's switch between Tube's pages. Everything above Tube home is
 * popped first and the target is single-top, so tapping Subscriptions
 * twice, or You then Subscriptions then You, never stacks a second copy:
 * the stack under a Tube page is always exactly [TubeHomeRoute] and at
 * most one other page, and Back from that page returns to home.
 */
fun NavController.navigateToTubeTab(tab: TubeTab) {
    val route: Any = when (tab) {
        TubeTab.HOME -> TubeHomeRoute
        TubeTab.SUBSCRIPTIONS -> TubeSubscriptionsRoute
        TubeTab.YOU -> TubeYouRoute
    }
    navigate(route) {
        popUpTo<TubeHomeRoute> { inclusive = false }
        launchSingleTop = true
    }
}
