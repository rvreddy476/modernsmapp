// MatchingDeclarationName: this file is the feature's navigation contract —
// the route type plus the graph and navigation extensions that use it.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.post.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import com.us.android.feature.post.composer.ComposerScreen
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

/**
 * The composer.
 *
 * A pushed destination with no arguments: it opens empty, or restores whatever
 * draft the ViewModel persisted. Carrying draft content on the route would put
 * a half-written post — potentially thousands of characters — into the back
 * stack's saved state, which is not what that mechanism is for.
 */
@Serializable
data object ComposerRoute

/**
 * Registers the composer.
 *
 * [onPublished] receives the SERVER's post id. `:app` decides that opening it
 * means the real post destination, so this feature never learns which screen
 * comes next and `:feature:post` stays independent of every other feature.
 */
fun NavGraphBuilder.composerScreen(
    onClose: () -> Unit,
    onPublished: (postId: String) -> Unit,
) {
    composable<ComposerRoute> {
        ComposerScreen(onClose = onClose, onPublished = onPublished)
    }
}

/** Type-safe navigation to the composer. */
fun NavController.navigateToComposer() = navigate(ComposerRoute)

/**
 * The Post Studio — the multi-photo editor.
 *
 * [initialUris] carries the Create hub's picker result (content-URI strings)
 * so the studio opens already loaded. Document STATE still never rides the
 * route — the studio resumes its project from ProjectStore; these are only
 * the not-yet-imported picks, consumed once by the ViewModel.
 */
@Serializable
data class StudioRoute(val initialUris: List<String> = emptyList())

fun NavGraphBuilder.studioScreen(
    onClose: () -> Unit,
    onPublished: (postId: String) -> Unit,
) {
    composable<StudioRoute> {
        com.us.android.feature.post.studio.StudioScreen(
            onClose = onClose,
            onPublished = onPublished,
        )
    }
}

/** Type-safe navigation to the Post Studio. */
fun NavController.navigateToStudio(initialUris: List<String> = emptyList()) =
    navigate(StudioRoute(initialUris))

/**
 * The Create hub — ONE entry for making anything.
 *
 * The feed's "+" lands here. A footer rail switches the surface (Text, Image,
 * Reel, Poll); nothing else on the screen selects a format, which is the whole
 * point: no extra plus buttons, no dropdowns.
 */
@Serializable
data object CreateRoute

fun NavGraphBuilder.createHubScreen(
    onClose: () -> Unit,
    onPublished: (postId: String) -> Unit,
    onOpenStudio: (uris: List<String>) -> Unit,
    onOpenLive: () -> Unit = {},
) {
    composable<CreateRoute> {
        com.us.android.feature.post.createhub.CreateHubScreen(
            onClose = onClose,
            onPublished = onPublished,
            onOpenStudio = onOpenStudio,
            onOpenLive = onOpenLive,
        )
    }
}

/** Type-safe navigation to the Create hub. */
fun NavController.navigateToCreate() = navigate(CreateRoute)
