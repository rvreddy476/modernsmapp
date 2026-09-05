// MatchingDeclarationName: this file is the feature's navigation contract.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.tube.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.tube.ui.TubeBarAction
import com.us.android.feature.tube.ui.TubeTab
import com.us.android.feature.tube.ui.channel.ChannelScreen
import com.us.android.feature.tube.ui.home.TubeHomeScreen
import com.us.android.feature.tube.ui.saved.SavedVideosScreen
import com.us.android.feature.tube.ui.scheduled.ScheduledPostsScreen
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

/** The Subscriptions page: videos from channels the viewer follows. */
@Serializable
data object TubeSubscriptionsRoute

/** The You page: the viewer's channel, their own videos, what they left unfinished, what they saved. */
@Serializable
data object TubeYouRoute

/** One creator's channel inside Tube (2026-09-05); [userId] is the channel's owner. */
@Serializable
data class TubeChannelRoute(val userId: String)

/**
 * The viewer's scheduled posts (header More → "Scheduled posts", 2026-09-05):
 * what is waiting to go live, with reschedule and publish-now on each row.
 */
@Serializable
data object TubeScheduledRoute

/** The viewer's saved long videos (header More → "Saved videos", 2026-09-05). */
@Serializable
data object TubeSavedRoute

/** One video, playing. Pushed over a Tube page; [postId] is the post to open on. */
@Serializable
data class WatchRoute(val postId: String)

/**
 * Every way out of Tube, and the ways around it. All of it is `:app`'s
 * to resolve — the feature never holds a NavController, the shape every
 * other feature keeps — including the bar's switch between Tube's own
 * pages, which `:app` answers with [navigateToTubeTab], a channel bubble,
 * which it answers with [navigateToTubeChannel], and the header's More
 * rows, which it answers with [navigateToTubeScheduled] / [navigateToTubeSaved].
 */
data class TubeDestinations(
    val onBack: () -> Unit,
    val onOpenAuthor: (userId: String) -> Unit,
    /** The header's search glyph; `:app` opens the search page scoped to the video app. */
    val onOpenSearch: () -> Unit,
    /** A card was tapped; `:app` pushes [WatchRoute]. */
    val onOpenVideo: (postId: String) -> Unit,
    /** The header More sheet's "Notifications" row. */
    val onOpenNotifications: () -> Unit,
    /**
     * A reel was tapped, or the bar's Reels slot: the app's Reels tab.
     * The screen has already left the post id in `ReelsEntry` when a
     * reel was tapped, the same handoff the Home feed makes.
     */
    val onOpenReels: () -> Unit,
    /** The bar's "+": the Create hub on its Video surface. */
    val onCreateVideo: () -> Unit,
    /** The bar's Explore slot: the app's launcher, the way to every other mini-app. */
    val onOpenExplore: () -> Unit,
    /** The bar's Home / You, the More sheet's channel and Subscriptions rows, and the You page's Subscriptions row. */
    val onOpenTab: (TubeTab) -> Unit,
    /** A channel bubble or a card's channel: the channel's page inside Tube. */
    val onOpenChannel: (userId: String) -> Unit,
    /** The More sheet's "Scheduled posts": `:app` pushes [TubeScheduledRoute]. */
    val onOpenScheduled: () -> Unit,
    /** The More sheet's "Saved videos": `:app` pushes [TubeSavedRoute]. */
    val onOpenSaved: () -> Unit,
) {
    /** The bar's tap, resolved. */
    fun onBarAction(action: TubeBarAction) = when (action) {
        is TubeBarAction.OpenTab -> onOpenTab(action.tab)
        TubeBarAction.OpenReels -> onOpenReels()
        TubeBarAction.CreateVideo -> onCreateVideo()
        TubeBarAction.OpenExplore -> onOpenExplore()
    }
}

/** Registers Tube's seven destinations. */
fun NavGraphBuilder.tubeScreens(destinations: TubeDestinations) {
    composable<TubeHomeRoute> { TubeHomeScreen(destinations = destinations) }
    composable<TubeSubscriptionsRoute> { SubscriptionsScreen(destinations = destinations) }
    composable<TubeYouRoute> { YouScreen(destinations = destinations) }
    composable<TubeChannelRoute> { ChannelScreen(destinations = destinations) }
    composable<TubeScheduledRoute> { ScheduledPostsScreen(destinations = destinations) }
    composable<TubeSavedRoute> { SavedVideosScreen(destinations = destinations) }
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

/** Type-safe navigation to a channel's page inside Tube. */
fun NavController.navigateToTubeChannel(userId: String) = navigate(TubeChannelRoute(userId))

/** The scheduled list, pushed over whichever Tube page opened the More sheet; single-top. */
fun NavController.navigateToTubeScheduled() = navigate(TubeScheduledRoute) { launchSingleTop = true }

/** The saved videos, pushed the same way. */
fun NavController.navigateToTubeSaved() = navigate(TubeSavedRoute) { launchSingleTop = true }

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
