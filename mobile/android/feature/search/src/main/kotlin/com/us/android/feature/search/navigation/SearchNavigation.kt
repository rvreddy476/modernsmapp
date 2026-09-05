// MatchingDeclarationName: this file is the feature's navigation contract.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.search.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.search.ui.SearchScreen
import kotlinx.serialization.Serializable

/**
 * What one scope chip looks for. [wireType] is the `type` the posts search
 * takes — omitted for ordinary posts, `flicks` for reels, `videos` for
 * long videos; users and channels have endpoints of their own.
 */
enum class SearchScope(val label: String, val wireType: String? = null) {
    USERS("Users"),
    POSTS("Posts"),
    REELS("Reels", wireType = "flicks"),
    VIDEOS("Videos", wireType = "videos"),
    CHANNELS("Channels"),
}

/**
 * Where search was opened from, and so which chips it shows (founder,
 * 2026-09-05): Home looks for people and posts, Reels for people and
 * reels, the video app for channels, people and videos, and Explore's
 * field for everything. The first chip is the one selected on open.
 */
enum class SearchOrigin(val scopes: List<SearchScope>, val placeholder: String) {
    HOME(listOf(SearchScope.USERS, SearchScope.POSTS), "Search people and posts"),
    REELS(listOf(SearchScope.USERS, SearchScope.REELS), "Search people and reels"),
    VIDEO(listOf(SearchScope.CHANNELS, SearchScope.USERS, SearchScope.VIDEOS), "Search videos, channels"),
    EXPLORE(SearchScope.entries.toList(), "Search Momentum"),
    ;

    companion object {
        fun fromWire(name: String): SearchOrigin = entries.firstOrNull { it.name == name } ?: EXPLORE
    }
}

/**
 * The search page. [origin] is a [SearchOrigin] by name — a plain string
 * so the route stays a serializable value; [query] is what Explore's field
 * already held when it submitted, blank when a header glyph opened it.
 */
@Serializable
data class SearchRoute(val origin: String = SearchOrigin.EXPLORE.name, val query: String = "")

/**
 * Every way out of search. All ids, all resolved by `:app` — the page
 * never sees a profile, a post, Reels, the watch screen or a channel page.
 */
data class SearchDestinations(
    val onBack: () -> Unit,
    val onOpenProfile: (userId: String) -> Unit,
    val onOpenPost: (postId: String) -> Unit,
    /** A reel row was tapped; the page has left its id in `ReelsEntry`, and `:app` switches to the Reels tab. */
    val onOpenReels: () -> Unit,
    /** A long video row was tapped; `:app` pushes the watch screen. */
    val onOpenVideo: (postId: String) -> Unit,
    /** A channel row was tapped; `:app` pushes the channel page inside Tube. */
    val onOpenChannel: (userId: String) -> Unit,
)

/** Registers the search page. */
fun NavGraphBuilder.searchScreen(destinations: SearchDestinations) {
    composable<SearchRoute> { SearchScreen(destinations = destinations) }
}

/** Opens search scoped to [origin]; single-top, so a header glyph tapped twice shows one page. */
fun NavController.navigateToSearch(origin: SearchOrigin, query: String = "") =
    navigate(SearchRoute(origin = origin.name, query = query)) { launchSingleTop = true }
