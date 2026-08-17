// MatchingDeclarationName: this file is the feature's navigation contract —
// the route type plus the graph and navigation extensions that use it. Naming
// it after the route alone would hide the extensions callers actually import.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.profile.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.profile.ui.EditProfileScreen
import com.us.android.feature.profile.ui.ProfileScreen
import kotlinx.serialization.Serializable

/**
 * A profile screen.
 *
 * [userId] is null for the signed-in user's own profile. Modelling "me" as an
 * absent id rather than requiring the caller to supply its own means the Me
 * tab can navigate on the first frame, without first reading the session to
 * discover who it is.
 */
@Serializable
data class ProfileRoute(val userId: String? = null)

/**
 * The signed-in user's own profile, as a tab root.
 *
 * A separate route from [ProfileRoute] rather than `ProfileRoute(null)`, for
 * one concrete reason: the shell decides bottom-bar visibility and back
 * behaviour from the destination. If both the Me tab and a pushed profile were
 * the same route type, that decision would have to inspect a navigation
 * *argument*, and a pushed profile with a null id would silently behave like a
 * tab root.
 *
 * Both routes render the same screen. The ViewModel reads the `userId`
 * argument, finds none here, and loads `/v1/profiles/me`.
 */
@Serializable
data object OwnProfileRoute

/**
 * Registers the profile destination.
 *
 * The feature exposes a `NavGraphBuilder` extension rather than its route
 * table, so `:app` composes the graph without importing the feature's screens
 * or ViewModels. Navigation out of the feature is passed in as callbacks —
 * a feature module must not know which screen comes next, or the two modules
 * become mutually dependent.
 */
fun NavGraphBuilder.profileScreen(
    onOpenFollowers: (userId: String) -> Unit,
    onOpenFollowing: (userId: String) -> Unit,
    onBack: () -> Unit,
) {
    composable<ProfileRoute> {
        ProfileScreen(
            onOpenFollowers = onOpenFollowers,
            onOpenFollowing = onOpenFollowing,
            onBack = onBack,
        )
    }
}

/**
 * Registers the Me tab.
 *
 * No `onBack`: this is a tab root, and the top bar renders no back control
 * when none is supplied.
 */
fun NavGraphBuilder.ownProfileScreen(
    onOpenFollowers: (userId: String) -> Unit,
    onOpenFollowing: (userId: String) -> Unit,
    onEditProfile: () -> Unit,
) {
    composable<OwnProfileRoute> {
        ProfileScreen(
            onOpenFollowers = onOpenFollowers,
            onOpenFollowing = onOpenFollowing,
            // Only the OWN-profile registration passes this. A pushed profile
            // of someone else leaves it null, so the edit control cannot appear
            // on a screen whose subject the viewer has no right to change.
            onEditProfile = onEditProfile,
        )
    }
}

/**
 * Editing the signed-in user's own profile.
 *
 * Carries no arguments, and cannot: the endpoint behind it is a full
 * replacement of the OWNER's fields, keyed off the access token rather than a
 * path id. A `userId` parameter here would imply an editing-someone-else
 * capability that neither the route nor the server has.
 */
@Serializable
data object EditProfileRoute

/**
 * Registers the edit-profile destination.
 *
 * [onSaved] is separate from [onBack] because the two outcomes are not the
 * same event. Backing out abandons unsaved edits; saving completes them, and
 * the caller usually wants to refresh the profile it returns to. Collapsing
 * both into one callback would leave the shell unable to tell the difference.
 */
fun NavGraphBuilder.editProfileScreen(
    onBack: () -> Unit,
    onSaved: () -> Unit,
) {
    composable<EditProfileRoute> {
        EditProfileScreen(
            onBack = onBack,
            onSaved = onSaved,
        )
    }
}

/** Type-safe navigation to another user's profile. */
fun NavController.navigateToProfile(userId: String) = navigate(ProfileRoute(userId))

/** Type-safe navigation to the signed-in user's own profile. */
fun NavController.navigateToOwnProfile() = navigate(ProfileRoute())

/** Type-safe navigation to the edit form for the signed-in user's profile. */
fun NavController.navigateToEditProfile() = navigate(EditProfileRoute)
