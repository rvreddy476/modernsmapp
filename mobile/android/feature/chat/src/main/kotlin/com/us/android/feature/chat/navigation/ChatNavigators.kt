// MatchingDeclarationName: the type-safe navigation helpers for every chat
// route, kept beside the route types in ChatNavigation.kt.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.chat.navigation

import androidx.navigation.NavController

/** Type-safe navigation to chat lock settings. */
fun NavController.navigateToChatLockSettings() = navigate(ChatLockSettingsRoute)

/** Type-safe navigation to the one chat screen. */
fun NavController.navigateToChatHome() = navigate(ChatHomeRoute)

/** Type-safe navigation to the inbox. */
fun NavController.navigateToChatInbox() = navigate(ChatInboxRoute)

/** Type-safe navigation to one conversation. */
fun NavController.navigateToChatThread(
    conversationId: String,
    title: String,
    isGroup: Boolean = false,
) = navigate(ChatThreadRoute(conversationId, title, isGroup))

/** Type-safe navigation to a request decision. */
fun NavController.navigateToChatRequest(conversationId: String, title: String) =
    navigate(ChatRequestRoute(conversationId, title))

fun NavController.navigateToChatRequestsList() = navigate(ChatRequestsListRoute)

fun NavController.navigateToInvitations() = navigate(InvitationsRoute)

/** Type-safe navigation to the new-group flow. */
fun NavController.navigateToGroupCreate() = navigate(GroupCreateRoute)

/** Type-safe navigation to group info. */
fun NavController.navigateToGroupInfo(conversationId: String) =
    navigate(GroupInfoRoute(conversationId))

fun NavController.navigateToGroupAddMembers(conversationId: String) =
    navigate(GroupAddMembersRoute(conversationId))

fun NavController.navigateToJoinByLink(code: String = "") = navigate(JoinByLinkRoute(code))

fun NavController.navigateToCommunityCreate() = navigate(CommunityCreateRoute)

fun NavController.navigateToCommunityEdit(communityId: String) = navigate(CommunityEditRoute(communityId))

fun NavController.navigateToCommunity(communityId: String) = navigate(CommunityPageRoute(communityId))

fun NavController.navigateToCommunityAdmins(communityId: String) = navigate(CommunityAdminsRoute(communityId))

fun NavController.navigateToCommunityPost(communityId: String) = navigate(CommunityPostRoute(communityId))
