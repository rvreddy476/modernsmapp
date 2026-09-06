// MatchingDeclarationName: this file is the feature's navigation contract —
// the route type plus the graph and navigation extensions that use it.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.post.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import com.us.android.feature.post.composer.ComposerScreen
import com.us.android.feature.post.createhub.CreateSurface
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
 *
 * Comments are not a destination any more. They open as the shared sheet
 * over the post (see `PostCommentsSheet`), the way the feed and reels open
 * them, so `:app` has nothing to wire for them.
 */
fun NavGraphBuilder.postScreen(
    onBack: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
) {
    composable<PostRoute> {
        PostScreen(
            onBack = onBack,
            onOpenAuthor = onOpenAuthor,
        )
    }
}

/** Type-safe navigation to a post. */
fun NavController.navigateToPost(postId: String) = navigate(PostRoute(postId))

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
data class StudioRoute(
    val initialUris: List<String> = emptyList(),
    /**
     * The pictures arrived from the advanced photo editor, already edited.
     *
     * The studio then opens on Share rather than on its own edit tools. A
     * person who has just finished editing in one editor is not asking to be
     * put into a second one — that reads as the edit having been thrown away.
     */
    val alreadyEdited: Boolean = false,
)

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
fun NavController.navigateToStudio(
    initialUris: List<String> = emptyList(),
    alreadyEdited: Boolean = false,
) = navigate(StudioRoute(initialUris, alreadyEdited))

/**
 * A create composer, opened directly on one surface.
 *
 * The bar's "+" opens the Create SHEET over the current screen; picking a tile
 * pushes this route with that tile's [CreateSurface.routeKey], and the hub
 * opens on exactly that composer with nothing to switch to afterwards. There
 * is no rail any more: choosing what to make happens on the sheet, once.
 *
 * A string rather than the enum so the argument is a plain, stable token in
 * the saved back stack; [CreateSurface.fromRouteKey] maps it back and falls
 * to Text for anything it does not recognise.
 */
@Serializable
data class CreateRoute(val surface: String = CreateSurface.Text.routeKey) {
    companion object {
        /** The route for a sheet tile — the ONLY way a tile becomes a destination. */
        fun of(surface: CreateSurface): CreateRoute = CreateRoute(surface.routeKey)
    }
}

fun NavGraphBuilder.createHubScreen(
    onClose: () -> Unit,
    onPublished: (postId: String) -> Unit,
    onOpenStudio: (uris: List<String>, alreadyEdited: Boolean) -> Unit,
    /** A long video was handed to the worker; `:app` closes the hub and opens the viewer's own profile. */
    onOpenOwnProfile: () -> Unit,
) {
    composable<CreateRoute> { entry ->
        val route = entry.toRoute<CreateRoute>()
        com.us.android.feature.post.createhub.CreateHubScreen(
            surface = CreateSurface.fromRouteKey(route.surface),
            onClose = onClose,
            onPublished = onPublished,
            onOpenStudio = onOpenStudio,
            onOpenOwnProfile = onOpenOwnProfile,
        )
    }
}

/** Type-safe navigation to one create surface. */
fun NavController.navigateToCreate(surface: CreateSurface) = navigate(CreateRoute.of(surface))
