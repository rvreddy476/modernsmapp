// MatchingDeclarationName: this file is the feature's navigation contract.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.feed.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import com.us.android.core.media.PlayerPool
import com.us.android.feature.feed.ui.FeedScreen
import com.us.android.feature.feed.ui.FriendsFeedScreen
import com.us.android.feature.feed.ui.HashtagPostsScreen
import com.us.android.feature.feed.ui.reels.ReelsScreen
import kotlinx.serialization.Serializable

/** The Home tab root. */
@Serializable
data object FeedRoute

/** The Friends feed: the home timeline narrowed to mutual follows. A root, opened from Explore. */
@Serializable
data object FriendsFeedRoute

/**
 * One tag's posts, pushed from the HashTag tab. [tag] is the normalized name
 * without its `#`, exactly as `GET /v1/hashtags/{tag}/posts` takes it.
 */
@Serializable
data class HashtagPostsRoute(val tag: String)

/**
 * Registers the feed destination.
 *
 * Every callback leaves the feature: `:feature:feed` must not import
 * `:feature:post`, `:feature:profile` or `:feature:chat`, so `:app` decides
 * what a post tap, an author tap and the Messages control open.
 *
 * [onOpenMessages] is REQUIRED rather than defaulted. The Messages icon was
 * once rendered here with an empty handler and shipped inert; a required
 * parameter is what makes that impossible to repeat by omission.
 *
 * [onOpenReels] is the tab switch a tapped video asks for. The feed has
 * already left the post id in `ReelsEntry` (`:core:media`) by the time it
 * calls this; `:app` only switches to the Reels root, which reads the
 * entry when it starts. No argument travels through the route, because a
 * tab root is restored, not pushed.
 */
fun NavGraphBuilder.feedScreen(
    onOpenAuthor: (userId: String) -> Unit,
    onOpenMessages: () -> Unit,
    /** Slice D: the feed's top bar is the entry point to the inbox. */
    onOpenNotifications: () -> Unit,
    /** Momentum's header search glyph. `:app` decides where search lives. */
    onOpenSearch: () -> Unit,
    /** A trending tag was tapped. `:app` pushes [HashtagPostsRoute] for it. */
    onOpenHashtag: (tag: String) -> Unit,
    /** A video was tapped. `:app` switches to the Reels tab. */
    onOpenReels: () -> Unit,
) {
    composable<FeedRoute> {
        FeedScreen(
            onOpenAuthor = onOpenAuthor,
            onOpenMessages = onOpenMessages,
            onOpenNotifications = onOpenNotifications,
            onOpenSearch = onOpenSearch,
            onOpenHashtag = onOpenHashtag,
            onOpenReels = onOpenReels,
        )
    }
}

/**
 * Registers the Friends feed. Since 2026-09-05 it is opened from the Explore
 * launcher rather than the bar, so it wears a back arrow: [onBack] returns
 * to wherever it was opened from. Same cross-feature contract as
 * [feedScreen] for authors and reels.
 */
fun NavGraphBuilder.friendsFeedScreen(
    onBack: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    onOpenReels: () -> Unit,
) {
    composable<FriendsFeedRoute> {
        FriendsFeedScreen(
            onBack = onBack,
            onOpenAuthor = onOpenAuthor,
            onOpenReels = onOpenReels,
        )
    }
}

/** Registers a tag's post list — a pushed screen with a back arrow, never a tab. */
fun NavGraphBuilder.hashtagPostsScreen(
    onBack: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    onOpenReels: () -> Unit,
) {
    composable<HashtagPostsRoute> { entry ->
        val route = entry.toRoute<HashtagPostsRoute>()
        HashtagPostsScreen(
            tag = route.tag,
            onBack = onBack,
            onOpenAuthor = onOpenAuthor,
            onOpenReels = onOpenReels,
        )
    }
}

/** Type-safe navigation to one tag's posts. */
fun NavController.navigateToHashtagPosts(tag: String) = navigate(HashtagPostsRoute(tag))

/** The Reels tab root. */
@Serializable
data object ReelsRoute

/**
 * Registers the reels destination.
 *
 * [pool] is passed in rather than injected into the screen so `:app` owns the
 * player pool's lifetime. The pool holds decoder sessions, and scoping it to a
 * composable that the pager recomposes would release and reacquire them mid
 * scroll.
 */
fun NavGraphBuilder.reelsScreen(
    pool: PlayerPool,
    onOpenAuthor: (userId: String) -> Unit,
    /** The header's search glyph over the video; `:app` decides it opens Explore. */
    onOpenSearch: () -> Unit,
) {
    composable<ReelsRoute> {
        ReelsScreen(
            pool = pool,
            onOpenAuthor = onOpenAuthor,
            onOpenSearch = onOpenSearch,
        )
    }
}
