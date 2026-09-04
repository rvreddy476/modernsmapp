package com.us.android.explore

import com.us.android.core.profile.data.AppModule

/**
 * The mini-apps the Explore launcher can show, in the order the founder set
 * (2026-09-05): the four every user has first — Chat, Friends, Alerts, Live
 * — then the five modules the picker offers, Shop → Match → Ask → Feast →
 * Tube.
 *
 * [module] is null for the always-on four: they are not module choices and
 * no setting hides them. The other five are tied to their [AppModule], and
 * the two facts about a module — whether the user switched it on, whether
 * this build has a screen for it — decide the tile in [launcherTiles].
 *
 * `:app` owns this rather than a feature module because a launcher tile
 * opens a destination in whichever feature owns it, and only `:app` may
 * know all of them.
 */
enum class LauncherApp(val label: String, val module: AppModule?) {
    CHAT("Chat", null),
    FRIENDS("Friends", null),
    ALERTS("Alerts", null),
    LIVE("Live", null),
    SHOP("Shop", AppModule.COMMERCE),
    MATCH("Match", AppModule.DATING),
    ASK("Ask", AppModule.QA),
    FEAST("Feast", AppModule.FOOD),
    TUBE("Tube", AppModule.POSTTUBE),
}

/**
 * One tile on the launcher. [soon] is a module the user has on but this
 * build has no screen for: drawn dimmed with a "Soon" pill, and a tap says
 * so ([comingSoonMessage]) rather than opening nothing.
 */
data class LauncherTile(val app: LauncherApp, val soon: Boolean)

/**
 * The launcher's tiles — a pure rule, so it is a plain unit test:
 *
 *  1. Order is [LauncherApp]'s, never the user's.
 *  2. Every app is always there. Explore is where the apps are FOUND
 *     (founder, 2026-09-05: "keep all nine or ten in Explore"); the module
 *     choices in Settings decide the home page and the bar, not this grid.
 *  3. An app this build cannot open yet is shown as "Soon": a promise, not
 *     a lie — its tile is dimmed and says so on tap.
 */
fun launcherTiles(): List<LauncherTile> =
    LauncherApp.entries.map { app -> LauncherTile(app, soon = app.module?.hasScreen == false) }

/** What a "Soon" tile says when tapped: "Shop is coming soon". */
fun comingSoonMessage(app: LauncherApp): String = "${app.label} is coming soon"
