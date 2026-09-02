package com.us.android.navigation

import com.us.android.core.profile.data.ModulePreferences

/**
 * Turns the user's module choices into the bottom bar.
 *
 * Three rules, in order:
 *  1. A tab is shown when it has no module (always-on) or its module is one
 *     the user switched on. Modules this build has no screen for never reach
 *     here — they have no [TopLevelDestination] — so an unbuilt choice is
 *     recorded server-side and produces no dead tab.
 *  2. The tab for the home module moves to the front; the rest keep the
 *     enum's order. A home module with no tab (Commerce, say) leaves the
 *     order alone, and the feed is first by construction.
 *  3. Nothing here is a Compose or navigation type, so it is a plain unit test.
 */
object TabResolver {
    fun resolve(prefs: ModulePreferences): List<TopLevelDestination> {
        val visible = TopLevelDestination.entries.filter { tab ->
            val module = tab.module
            module == null || (module.hasScreen && prefs.includes(module))
        }
        val home = visible.firstOrNull { it.module == prefs.homeModule } ?: return visible
        return listOf(home) + visible.filter { it != home }
    }
}
