// MatchingDeclarationName: this file is the feature's navigation contract —
// the route type plus the graph and navigation extensions that use it. Naming
// it after the route alone would hide the extensions callers actually import.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.profile.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.profile.ui.DirectMessagesScreen
import com.us.android.feature.profile.ui.EditProfileScreen
import com.us.android.feature.profile.ui.FollowRequestsScreen
import com.us.android.feature.profile.ui.NotificationSettingsScreen
import com.us.android.feature.profile.ui.PrivacySettingsScreen
import com.us.android.feature.profile.ui.ProfileDestinations
import com.us.android.feature.profile.ui.ProfileDetailsScreen
import com.us.android.feature.profile.ui.ProfileScreen
import com.us.android.feature.profile.ui.SecuritySettingsScreen
import com.us.android.feature.profile.ui.SettingsHubScreen
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
    onOpenChat: (conversationId: String, title: String) -> Unit,
) {
    composable<ProfileRoute> {
        ProfileScreen(
            destinations = ProfileDestinations(
                onOpenFollowers = onOpenFollowers,
                onOpenFollowing = onOpenFollowing,
                onBack = onBack,
                // Only the PUSHED registration passes this, and [ownProfileScreen]
                // deliberately does not: message-service refuses a conversation
                // with yourself ("cannot create conversation with self"), so a
                // Message button on your own profile is a control whose only
                // possible outcome is an error.
                onOpenChat = onOpenChat,
            ),
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
    onOpenSettings: () -> Unit,
    onOpenFollowRequests: () -> Unit,
) {
    composable<OwnProfileRoute> {
        ProfileScreen(
            destinations = ProfileDestinations(
                onOpenFollowers = onOpenFollowers,
                onOpenFollowing = onOpenFollowing,
                // Only the OWN-profile registration passes this. A pushed profile
                // of someone else leaves it null, so the edit control cannot appear
                // on a screen whose subject the viewer has no right to change.
                onEditProfile = onEditProfile,
                onOpenSettings = onOpenSettings,
                // Same reasoning as onEditProfile: approving someone into an
                // account is only ever offered on that account's own screen.
                onOpenFollowRequests = onOpenFollowRequests,
            ),
        )
    }
}

/**
 * Incoming follow requests for the signed-in user's own private account.
 *
 * Carries no arguments for the same reason [EditProfileRoute] does not: the
 * endpoints behind it — `GET /v1/graph/follow-requests/incoming` and its
 * accept/decline actions — are all keyed off the access token, never a path
 * id, so there is no "whose requests" to parameterize.
 */
@Serializable data object FollowRequestsRoute

fun NavGraphBuilder.followRequestsScreen(
    onBack: () -> Unit,
    onOpenProfile: (userId: String) -> Unit,
) {
    composable<FollowRequestsRoute> {
        FollowRequestsScreen(onBack = onBack, onOpenProfile = onOpenProfile)
    }
}

fun NavController.navigateToFollowRequests() = navigate(FollowRequestsRoute)

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

@Serializable data object SettingsRoute

@Serializable data object PrivacySettingsRoute

@Serializable data object NotificationSettingsRoute

@Serializable data object SecuritySettingsRoute

@Serializable data object ProfileDetailsRoute

/** "Who can message you", the three-row picker pushed from Privacy. */
@Serializable data object DirectMessagesRoute

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

fun NavGraphBuilder.settingsScreens(
    destinations: SettingsDestinations,
) {
    composable<SettingsRoute> {
        SettingsHubScreen(
            destinations.onBack,
            destinations.onEditProfile,
            destinations.onProfileDetails,
            destinations.sections,
        )
    }
    composable<ProfileDetailsRoute> { ProfileDetailsScreen(destinations.onBack) }
    composable<PrivacySettingsRoute> {
        PrivacySettingsScreen(destinations.onBack, destinations.onDirectMessages)
    }
    composable<DirectMessagesRoute> { DirectMessagesScreen(destinations.onBack) }
    composable<NotificationSettingsRoute> { NotificationSettingsScreen(destinations.onBack) }
    composable<SecuritySettingsRoute> {
        SecuritySettingsScreen(destinations.onBack, destinations.onSignedOut)
    }
}

data class SettingsDestinations(
    val onBack: () -> Unit,
    val onEditProfile: () -> Unit,
    val onProfileDetails: () -> Unit,
    val onDirectMessages: () -> Unit,
    val onSignedOut: () -> Unit,
    val sections: SettingsSections,
)

/**
 * The hub's per-section entry points. `:app` owns every destination —
 * `:feature:profile` renders the hub, but only two of these sections
 * (Privacy, Notifications) live in this feature; the rest are `:feature:settings`
 * pages or cross-feature targets, so the hub never imports them directly.
 */
data class SettingsSections(
    val onManageAccount: () -> Unit,
    val onPrivacy: () -> Unit,
    val onNotifications: () -> Unit,
    val onScreenTime: () -> Unit,
    val onContentPreferences: () -> Unit,
    val onSecurity: () -> Unit,
    /**
     * The module picker lives in `:feature:settings`; `:app` owns which
     * destination that is, so this feature never imports it.
     */
    val onModules: () -> Unit,
)

/** Type-safe navigation to another user's profile. */
fun NavController.navigateToProfile(userId: String) = navigate(ProfileRoute(userId))

/** Type-safe navigation to the signed-in user's own profile. */
fun NavController.navigateToOwnProfile() = navigate(ProfileRoute())

/** Type-safe navigation to the edit form for the signed-in user's profile. */
fun NavController.navigateToEditProfile() = navigate(EditProfileRoute)
fun NavController.navigateToSettings() = navigate(SettingsRoute)
