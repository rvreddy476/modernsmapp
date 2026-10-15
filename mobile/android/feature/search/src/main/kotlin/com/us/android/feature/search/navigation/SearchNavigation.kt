// MatchingDeclarationName: this file is the feature's navigation contract.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.search.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.search.ui.SearchScreen
import kotlinx.serialization.Serializable

/**
 * What one tab looks for (parity item T-16, 2026-10-15), each on its own
 * route — see `SearchApi`:
 *
 *  - [TOP]: search-service's ranked `GET v1/search?types=posts,users,hashtags`.
 *  - [PEOPLE]: `GET v1/search/users?record=1`.
 *  - [VIDEOS] / [REELS]: `GET v1/search/posts?type=videos|flicks` ([wireType]).
 *  - [SOUNDS]: media-service's `GET v1/audio/search`.
 *  - [HASHTAGS]: `GET v1/search/hashtags?record=1`.
 *  - [CHANNELS]: post-service's `GET v1/channels/search` — the video app only.
 *
 * [recordsHistory] is whether the page sends `record=1`: the people and
 * hashtag routes also serve typeahead pickers (@mentions, invites, the
 * composer's tags), so the server records them only when asked, and only
 * this page asks. Top and the posts search record by default server-side.
 */
enum class SearchScope(
    val label: String,
    val wireType: String? = null,
    val recordsHistory: Boolean = false,
) {
    TOP("Top"),
    PEOPLE("People", recordsHistory = true),
    VIDEOS("Videos", wireType = "videos"),
    REELS("Reels", wireType = "flicks"),
    SOUNDS("Sounds"),
    HASHTAGS("Hashtags", recordsHistory = true),
    CHANNELS("Channels"),
}

/** The six tabs of the full search page, in order. */
private val ALL_TABS = listOf(
    SearchScope.TOP,
    SearchScope.PEOPLE,
    SearchScope.VIDEOS,
    SearchScope.REELS,
    SearchScope.SOUNDS,
    SearchScope.HASHTAGS,
)

/**
 * Where search was opened from, and so which tabs it shows. Home and
 * Explore's field get the full page — Top · People · Videos · Reels ·
 * Sounds · Hashtags (T-16); Reels opens on reels, with the people, sounds
 * and tags a reel is found by; the video app keeps its own channels,
 * people and videos (it is an app of its own). The first tab is the one
 * selected on open.
 */
enum class SearchOrigin(val scopes: List<SearchScope>, val placeholder: String) {
    HOME(ALL_TABS, "Search Momentum"),
    REELS(
        listOf(SearchScope.REELS, SearchScope.PEOPLE, SearchScope.SOUNDS, SearchScope.HASHTAGS),
        "Search reels, people and sounds",
    ),
    VIDEO(listOf(SearchScope.CHANNELS, SearchScope.PEOPLE, SearchScope.VIDEOS), "Search videos, channels"),
    EXPLORE(ALL_TABS, "Search Momentum"),
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
 * never sees a profile, a post, Reels, the watch screen, a channel, a tag
 * or a sound page.
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
    /** A hashtag row was tapped; the tag comes without its `#`. */
    val onOpenHashtag: (tag: String) -> Unit,
    /** A sound row was tapped; `:app` pushes the sound's page. */
    val onOpenSound: (soundId: String) -> Unit,
)

/** Registers the search page. */
fun NavGraphBuilder.searchScreen(destinations: SearchDestinations) {
    composable<SearchRoute> { SearchScreen(destinations = destinations) }
}

/** Opens search scoped to [origin]; single-top, so a header glyph tapped twice shows one page. */
fun NavController.navigateToSearch(origin: SearchOrigin, query: String = "") =
    navigate(SearchRoute(origin = origin.name, query = query)) { launchSingleTop = true }
