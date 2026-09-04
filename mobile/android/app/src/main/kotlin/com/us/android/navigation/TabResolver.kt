package com.us.android.navigation

import com.us.android.core.profile.data.ModulePreferences

/**
 * Turns the user's module choices into the bottom bar.
 *
 * The bar's ORDER is fixed — Home, Reels, "+", Explore, Me (founder,
 * 2026-09-05: Explore, the mini-app launcher, took Friends' slot; Friends
 * opens from the launcher) — whatever the user's home module is. The module
 * choices decide only two things:
 *  1. Whether a module-backed tab is shown at all: Reels appears when the
 *     reels module is on; Home, Explore and Me are always there. Modules this
 *     build has no screen for never reach here — they have no
 *     [TopLevelDestination] — so an unbuilt choice is recorded server-side
 *     and produces no dead tab.
 *  2. Which tab opens FIRST at launch — [startDestination]. A home module of
 *     Reels opens on Reels; anything else (the feed, or a module with no tab
 *     in the bar, such as Chat) opens on Home. Earlier the home tab was also
 *     MOVED to the front of the bar; it no longer is, because a bar whose
 *     order depends on a setting is a bar the thumb never learns.
 *
 * Nothing here is a Compose or navigation type, so it is a plain unit test.
 */
object TabResolver {

    /**
     * The bar, in order. Messages and Friends are tab roots (the inbox and
     * the friends feed are reached from the header and the Explore launcher)
     * but never bar items, so they are simply not listed.
     */
    private val BAR_ORDER = listOf(
        TopLevelDestination.HOME,
        TopLevelDestination.REELS,
        TopLevelDestination.EXPLORE,
        TopLevelDestination.ME,
    )

    fun resolve(prefs: ModulePreferences): List<TopLevelDestination> = BAR_ORDER.filter { tab ->
        val module = tab.module
        module == null || (module.hasScreen && prefs.includes(module))
    }

    /**
     * The tab the graph starts on: the home module's, when that module has a
     * tab in the resolved bar; otherwise Home. Always a member of
     * [resolve]'s answer, so the bar has a selected item on the first frame.
     */
    fun startDestination(prefs: ModulePreferences): TopLevelDestination =
        resolve(prefs).firstOrNull { it.module == prefs.homeModule } ?: TopLevelDestination.HOME
}
