// MatchingDeclarationName: this file is the feature's navigation contract —
// the route type plus the graph and navigation extensions that use it.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.post.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import com.us.android.feature.post.ui.CommentsScreen
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
    onOpenComments: (postId: String) -> Unit,
) {
    // The post id is read from the route here rather than plumbed out of the
    // screen, so the screen's callback stays argument-free and `:app` does not
    // have to know that comments are keyed by post.
    composable<PostRoute> { entry ->
        val route = entry.toRoute<PostRoute>()
        PostScreen(
            onBack = onBack,
            onOpenAuthor = onOpenAuthor,
            onOpenComments = { onOpenComments(route.postId) },
        )
    }
}

/** Type-safe navigation to a post. */
fun NavController.navigateToPost(postId: String) = navigate(PostRoute(postId))

/**
 * The comments on a post.
 *
 * A separate destination rather than a section of [PostRoute], because the two
 * load independently: a post that renders fine can have a comments list that
 * fails, and folding them into one screen makes one failure blank the other.
 * The argument name matches [PostRoute]'s so both screens read the same
 * `postId` key out of their SavedStateHandle.
 */
@Serializable
data class CommentsRoute(val postId: String)

/**
 * Registers the comments destination.
 *
 * Takes only [onBack]. The screen shows no author link and no composer — the
 * comment payload carries no author name to open a profile with, and no
 * create-comment request was ever captured — so there is nothing else for the
 * host to wire.
 */
fun NavGraphBuilder.commentsScreen(
    onBack: () -> Unit,
) {
    composable<CommentsRoute> {
        CommentsScreen(onBack = onBack)
    }
}

/** Type-safe navigation to a post's comments. */
fun NavController.navigateToComments(postId: String) = navigate(CommentsRoute(postId))
