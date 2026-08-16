// MatchingDeclarationName: this file is the feature's navigation contract —
// the route type plus the graph and navigation extensions that use it. Naming
// it after the route alone would hide the extensions callers actually import.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.profile.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
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
) {
    composable<ProfileRoute> {
        ProfileScreen(
            onOpenFollowers = onOpenFollowers,
            onOpenFollowing = onOpenFollowing,
        )
    }
}

/** Type-safe navigation to another user's profile. */
fun NavController.navigateToProfile(userId: String) = navigate(ProfileRoute(userId))

/** Type-safe navigation to the signed-in user's own profile. */
fun NavController.navigateToOwnProfile() = navigate(ProfileRoute())
