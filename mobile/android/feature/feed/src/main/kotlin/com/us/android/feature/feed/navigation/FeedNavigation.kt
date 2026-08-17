// MatchingDeclarationName: this file is the feature's navigation contract.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.feed.navigation

import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.feed.ui.FeedScreen
import kotlinx.serialization.Serializable

/** The Home tab root. */
@Serializable
data object FeedRoute

/**
 * Registers the feed destination.
 *
 * Both callbacks leave the feature: `:feature:feed` must not import
 * `:feature:post` or `:feature:profile`, so `:app` decides what a post tap and
 * an author tap open.
 */
fun NavGraphBuilder.feedScreen(
    onOpenPost: (postId: String) -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
) {
    composable<FeedRoute> {
        FeedScreen(onOpenPost = onOpenPost, onOpenAuthor = onOpenAuthor)
    }
}
