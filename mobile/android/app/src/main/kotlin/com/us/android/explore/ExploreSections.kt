// The section value and the directory grouping/search rules belong together.
@file:Suppress("MatchingDeclarationName")

package com.us.android.explore

internal data class ExploreSection(val title: String, val tiles: List<LauncherTile>)

/** One source of honest app descriptions, used in the directory and its search. */
internal val LauncherApp.description: String
    get() = when (this) {
        LauncherApp.CHAT -> "Your conversations"
        LauncherApp.FRIENDS -> "Stay connected"
        LauncherApp.ALERTS -> "Your latest updates"
        LauncherApp.LIVE -> "In the moment"
        LauncherApp.MSTORE -> "Explore shopping"
        LauncherApp.MSELLER -> "Your seller space"
        LauncherApp.MATCH -> "Meaningful connections"
        LauncherApp.ASK -> "Questions & ideas"
        LauncherApp.FEAST -> "Food to your door"
        LauncherApp.TUBE -> "Watch & discover"
        LauncherApp.RIDE -> "Book a ride"
        LauncherApp.DOORSTEP -> "Home services"
    }

internal fun exploreSections(tiles: List<LauncherTile>, query: String): List<ExploreSection> {
    val term = query.trim()
    val visible = tiles.filter {
        term.isBlank() || it.app.label.contains(term, true) || it.app.description.contains(term, true)
    }
    val connect = setOf(LauncherApp.CHAT, LauncherApp.FRIENDS, LauncherApp.ALERTS, LauncherApp.LIVE)
    val everyday =
        setOf(LauncherApp.MSTORE, LauncherApp.MSELLER, LauncherApp.FEAST, LauncherApp.RIDE, LauncherApp.DOORSTEP)
    return listOf(
        ExploreSection("Connect & share", visible.filter { it.app in connect }),
        ExploreSection("Your everyday", visible.filter { it.app in everyday }),
        ExploreSection("Discover something new", visible.filter { it.app !in connect && it.app !in everyday }),
    ).filter { it.tiles.isNotEmpty() }
}
