package com.us.android.navigation

import androidx.navigation.NavController
import androidx.navigation.NavDestination
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.navOptions
import com.us.android.core.designsystem.component.UsNavItem
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.profile.data.AppModule
import com.us.android.feature.chat.navigation.ChatHomeRoute
import com.us.android.feature.feed.navigation.FeedRoute
import com.us.android.feature.feed.navigation.FriendsFeedRoute
import com.us.android.feature.feed.navigation.ReelsRoute
import com.us.android.feature.profile.navigation.OwnProfileRoute
import kotlin.reflect.KClass

/**
 * Every top-level root this build can show, paired with the route it selects
 * and the module that switches it on.
 *
 * The pairing lives in `:app` because it is the only module allowed to know
 * both the design system's presentation of a tab and the feature that owns its
 * destination. Each entry carries its own [item] rather than indexing into a
 * shared list, because the bar is not a fixed five: [TabResolver] picks
 * entries from the user's module choices in ITS order, and an ordinal-indexed
 * lookup would break the moment one tab is left out.
 *
 * Two entries are roots but never bar items: [MESSAGES] (the one chat screen, opened
 * from Home's header and the Explore launcher) and [FRIENDS] (the friends
 * feed, opened from the launcher since 2026-09-05, when Explore took its
 * place in the bar). They stay here so [forDestination] recognises them as
 * roots — a pushed screen over the inbox is still "inside Messages" — while
 * [TabResolver] leaves them out of the bar.
 *
 * [module] is null for the tabs every user has (Explore, Me, and Friends). Home
 * maps to [AppModule.FEED], which [ModulePreferences.includes] always answers
 * yes to, so it too is always present — the mapping exists so the feed can be
 * the user's *home*, not so it can be switched off.
 */
enum class TopLevelDestination(
    val route: KClass<*>,
    val item: UsNavItem,
    val module: AppModule?,
) {
    HOME(FeedRoute::class, UsNavItem("Home", UsIcons.Home), AppModule.FEED),
    REELS(ReelsRoute::class, UsNavItem("Reels", UsIcons.Reels), AppModule.REELS),
    FRIENDS(FriendsFeedRoute::class, UsNavItem("Friends", UsIcons.Friends), null),
    ME(OwnProfileRoute::class, UsNavItem("Me", UsIcons.Profile, contentDescription = "My profile"), null),
    MESSAGES(ChatHomeRoute::class, UsNavItem("Messages", UsIcons.Comment), AppModule.CHAT),
    EXPLORE(ExploreRoute::class, UsNavItem("Explore", UsIcons.Explore), null),
    ;

    companion object {
        /**
         * The root that owns [destination], or null when the current screen is
         * not a top-level root.
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
 * The serializable route object a tab navigates to, and the graph starts at
 * when the tab is the user's home. Exhaustive: a new tab without a route is a
 * compile error, not a runtime "no destination found".
 */
val TopLevelDestination.rootRoute: Any
    get() = when (this) {
        TopLevelDestination.HOME -> FeedRoute
        TopLevelDestination.REELS -> ReelsRoute
        TopLevelDestination.FRIENDS -> FriendsFeedRoute
        TopLevelDestination.ME -> OwnProfileRoute
        TopLevelDestination.MESSAGES -> ChatHomeRoute
        TopLevelDestination.EXPLORE -> ExploreRoute
    }

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
 *
 * One root does NOT restore: see [restoresItsStack].
 */
fun NavController.navigateToTopLevel(destination: TopLevelDestination) {
    val options = navOptions {
        popUpTo(graph.findStartDestination().id) { saveState = true }
        launchSingleTop = true
        restoreState = destination.restoresItsStack
    }
    navigate(destination.rootRoute, options)
}

/**
 * Whether switching to this root brings back what was last open above it.
 *
 * Every root does, except [TopLevelDestination.EXPLORE] (2026-10-02). What
 * is pushed above the launcher is a whole mini-app — Tube, MStore, Feast —
 * with its own bar and no shell bar. Restoring it meant the bar's Explore
 * item opened Tube, not Explore, for anyone who had left Tube by its Reels
 * slot, and there was then no launcher to be found. The launcher is one
 * screen; a mini-app is re-entered from its tile.
 */
val TopLevelDestination.restoresItsStack: Boolean
    get() = this != TopLevelDestination.EXPLORE

/**
 * Opens the Explore launcher ITSELF, from inside a mini-app (Tube's bar,
 * 2026-10-02).
 *
 * ## WHY THIS IS NOT [navigateToTopLevel]
 *
 * A mini-app is PUSHED from the launcher, so inside Tube the back stack is
 * `Home, Explore, Tube`. [navigateToTopLevel], as it then was, popped to the
 * start with `saveState` — which filed `Explore, Tube` away under Explore's
 * id — and then navigated to Explore with `restoreState`, which put exactly
 * that stack back. The user pressed Explore and was still looking at Tube: "Explore
 * cannot be opened from PostTube" (founder's phone, 2026-10-02). A bottom-bar
 * tab SHOULD come back the way it was left; a button that says "take me to
 * the launcher" must not.
 *
 * So: when the launcher is under the mini-app, go back to it — the same
 * entry, and Back from it still returns Home. When it is not (the mini-app
 * was opened from a search result or a notification), open it as a tab,
 * which since the same day no longer restores ([restoresItsStack]).
 */
fun NavController.navigateToLauncher() {
    if (popBackStack<ExploreRoute>(inclusive = false)) return
    navigateToTopLevel(TopLevelDestination.EXPLORE)
}
