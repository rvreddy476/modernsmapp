// MatchingDeclarationName: this file is the feature's navigation contract —
// the route type plus the graph and navigation extensions that use it.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.post.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.post.ui.PostScreen
import kotlinx.serialization.Serializable

/** A single post. Always pushed, never a tab root. */
@Serializable
data class PostRoute(val postId: String)

/**
 * Registers the post destination.
 *
 * [onOpenAuthor] is a callback rather than a direct navigation to the profile
 * route: `:feature:post` must not import `:feature:profile`, or the two become
 * mutually dependent and neither can be tested alone. `:app` owns the wiring
 * between features.
 */
fun NavGraphBuilder.postScreen(
    onBack: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
) {
    composable<PostRoute> {
        PostScreen(onBack = onBack, onOpenAuthor = onOpenAuthor)
    }
}

/** Type-safe navigation to a post. */
fun NavController.navigateToPost(postId: String) = navigate(PostRoute(postId))
