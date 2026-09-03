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

/** The Friends tab root: the home timeline narrowed to mutual follows. */
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
 */
fun NavGraphBuilder.feedScreen(
    onOpenPost: (postId: String) -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    onOpenMessages: () -> Unit,
    /** Slice D: the feed's top bar is the entry point to the inbox. */
    onOpenNotifications: () -> Unit,
    /** Momentum's header search glyph. `:app` decides where search lives. */
    onOpenSearch: () -> Unit,
    /** A trending tag was tapped. `:app` pushes [HashtagPostsRoute] for it. */
    onOpenHashtag: (tag: String) -> Unit,
) {
    composable<FeedRoute> {
        FeedScreen(
            onOpenPost = onOpenPost,
            onOpenAuthor = onOpenAuthor,
            onOpenMessages = onOpenMessages,
            onOpenNotifications = onOpenNotifications,
            onOpenSearch = onOpenSearch,
            onOpenHashtag = onOpenHashtag,
        )
    }
}

/** Registers the Friends tab root. Same cross-feature contract as [feedScreen]. */
fun NavGraphBuilder.friendsFeedScreen(
    onOpenPost: (postId: String) -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
) {
    composable<FriendsFeedRoute> {
        FriendsFeedScreen(onOpenPost = onOpenPost, onOpenAuthor = onOpenAuthor)
    }
}

/** Registers a tag's post list — a pushed screen with a back arrow, never a tab. */
fun NavGraphBuilder.hashtagPostsScreen(
    onBack: () -> Unit,
    onOpenPost: (postId: String) -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
) {
    composable<HashtagPostsRoute> { entry ->
        val route = entry.toRoute<HashtagPostsRoute>()
        HashtagPostsScreen(
            tag = route.tag,
            onBack = onBack,
            onOpenPost = onOpenPost,
            onOpenAuthor = onOpenAuthor,
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
) {
    composable<ReelsRoute> {
        ReelsScreen(pool = pool, onOpenAuthor = onOpenAuthor)
    }
}
