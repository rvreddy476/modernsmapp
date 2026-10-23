package com.us.android.feature.memberships.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.memberships.creator.CreatorMembershipsScreen
import com.us.android.feature.memberships.mine.MembershipDetailScreen
import com.us.android.feature.memberships.mine.MyMembershipsScreen
import kotlinx.serialization.Serializable

/**
 * The creator's Memberships page: eligibility, levels, members. From the Me
 * drawer and from a `membership_new_member` notification
 * (`/monetization/tiers`).
 */
@Serializable
data object CreatorMembershipsRoute

/** The viewer's own memberships. From the Me drawer and `/memberships`. */
@Serializable
data object MyMembershipsRoute

/** One membership: renew, change level, cancel. From every member notice (`/memberships/{id}`). */
@Serializable
data class MembershipDetailRoute(val subscriptionId: String)

/** What the membership screens need from `:app`: going back, and a creator's profile. */
data class MembershipsDestinations(
    val onBack: () -> Unit,
    val onOpenCreator: (creatorId: String) -> Unit,
    val onOpenMembership: (subscriptionId: String) -> Unit,
)

fun NavGraphBuilder.membershipScreens(destinations: MembershipsDestinations) {
    composable<CreatorMembershipsRoute> { CreatorMembershipsScreen(onBack = destinations.onBack) }
    composable<MyMembershipsRoute> { MyMembershipsScreen(destinations = destinations) }
    composable<MembershipDetailRoute> { MembershipDetailScreen(destinations = destinations) }
}

fun NavController.navigateToCreatorMemberships() = navigate(CreatorMembershipsRoute) { launchSingleTop = true }

fun NavController.navigateToMyMemberships() = navigate(MyMembershipsRoute) { launchSingleTop = true }

fun NavController.navigateToMembership(subscriptionId: String) =
    navigate(MembershipDetailRoute(subscriptionId)) { launchSingleTop = true }
