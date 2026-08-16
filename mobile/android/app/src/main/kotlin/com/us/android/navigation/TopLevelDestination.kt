package com.us.android.navigation

import androidx.navigation.NavController
import androidx.navigation.NavDestination
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.navOptions
import com.us.android.core.designsystem.component.UsDefaultNavItems
import com.us.android.core.designsystem.component.UsNavItem
import com.us.android.feature.profile.navigation.OwnProfileRoute
import kotlin.reflect.KClass

/**
 * The five tabs, paired with the routes they select.
 *
 * The pairing lives in `:app` because it is the only module allowed to know
 * both the design system's presentation of a tab and the feature that owns its
 * destination. `:core:designsystem` supplies the labels and icons and knows
 * nothing about navigation; the features supply routes and know nothing about
 * the bar.
 *
 * Order must match [UsDefaultNavItems], and the test in this module asserts it
 * rather than trusting the two lists to stay in step by inspection.
 */
enum class TopLevelDestination(val route: KClass<*>) {
    HOME(HomeRoute::class),
    FRIENDS(FriendsRoute::class),
    REELS(ReelsRoute::class),
    EXPLORE(ExploreRoute::class),
    ME(OwnProfileRoute::class),
    ;

    val item: UsNavItem get() = UsDefaultNavItems[ordinal]

    companion object {
        /**
         * The tab that owns [destination], or null when the current screen is
         * not a tab root.
         *
         * Null is what hides the bottom bar. A pushed screen — another user's
         * profile, a post, a settings page — is not a tab, and showing the bar
         * there would let a user "switch tabs" out of a half-finished flow.
         */
        fun forDestination(destination: NavDestination?): TopLevelDestination? =
            entries.firstOrNull { entry ->
                destination?.hierarchy?.any { it.hasRoute(entry.route) } == true
            }
    }
}

/** Walks a destination and its parents, so nested graphs still resolve a tab. */
private val NavDestination.hierarchy: Sequence<NavDestination>
    get() = generateSequence(this) { it.parent }

/**
 * Switches tabs the way a bottom bar is expected to behave.
 *
 * Three flags, each load-bearing:
 *  - `popUpTo(graph start) { saveState = true }` — tapping a tab returns to
 *    the app's root rather than stacking tabs on top of each other, and the
 *    outgoing tab's scroll position and back stack are kept.
 *  - `launchSingleTop` — re-tapping the current tab must not push a duplicate.
 *  - `restoreState` — the incoming tab comes back where the user left it.
 *
 * Without the save/restore pair, every tab switch resets the feed to the top,
 * which is the single most-noticed navigation defect in an app like this.
 */
fun NavController.navigateToTopLevel(destination: TopLevelDestination) {
    val options = navOptions {
        popUpTo(graph.findStartDestination().id) { saveState = true }
        launchSingleTop = true
        restoreState = true
    }
    when (destination) {
        TopLevelDestination.HOME -> navigate(HomeRoute, options)
        TopLevelDestination.FRIENDS -> navigate(FriendsRoute, options)
        TopLevelDestination.REELS -> navigate(ReelsRoute, options)
        TopLevelDestination.EXPLORE -> navigate(ExploreRoute, options)
        TopLevelDestination.ME -> navigate(OwnProfileRoute, options)
    }
}
