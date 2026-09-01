// MatchingDeclarationName: this file is the feature's navigation contract —
// the route types plus the graph and navigation extensions that use them.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.chat.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import com.us.android.feature.chat.ui.ChatInboxScreen
import com.us.android.feature.chat.ui.ChatLockGate
import com.us.android.feature.chat.ui.ChatLockSettingsScreen
import com.us.android.feature.chat.ui.ChatRequestScreen
import com.us.android.feature.chat.ui.ChatThreadScreen
import com.us.android.feature.chat.ui.FriendRequestsScreen
import com.us.android.feature.chat.ui.FriendsScreen
import com.us.android.feature.chat.ui.GroupCreateScreen
import com.us.android.feature.chat.ui.GroupInfoScreen
import kotlinx.serialization.Serializable

/** The list of conversations. Pushed from the feed's Messages control. */
@Serializable
data object ChatInboxRoute

/**
 * One conversation.
 *
 * [title] is carried on the route rather than fetched, so the top bar has a
 * name on the FIRST frame. [isGroup] gates the group-info entry the same way.
 *
 * The argument is named `conversationId` because `ChatThreadViewModel` reads
 * exactly that key out of its `SavedStateHandle`. Renaming one without the
 * other yields a screen that loads an empty conversation id and shows nothing,
 * with no error anywhere.
 */
@Serializable
data class ChatThreadRoute(
    val conversationId: String,
    val title: String,
    val isGroup: Boolean = false,
)

/** A pending message request, before any decision (production chat pass). */
@Serializable
data class ChatRequestRoute(
    val conversationId: String,
    val title: String,
)

/** All the viewer's friends (accepted connections), each one tap from a thread. */
@Serializable
data object FriendsRoute

/** Pending friend requests — received and sent (Figma 140:104). */
@Serializable
data object FriendRequestsRoute

/** The new-group flow. */
@Serializable
data object GroupCreateRoute

/** Group info + administration for one group conversation. */
@Serializable
data class GroupInfoRoute(val conversationId: String)

/**
 * Registers the inbox.
 *
 * [onOpenThread] takes the id AND the resolved title, because resolving a
 * direct conversation's title needs the viewer's own id to pick the other
 * member — which the inbox has and `:app` does not.
 */
fun NavGraphBuilder.chatInboxScreen(
    onBack: () -> Unit,
    onOpenThread: (conversationId: String, title: String, isGroup: Boolean) -> Unit,
    onOpenRequest: (conversationId: String, title: String) -> Unit,
    onCreateGroup: () -> Unit,
    onOpenLockSettings: () -> Unit = {},
    onOpenCallHistory: () -> Unit = {},
) {
    composable<ChatInboxRoute> {
        // EVERY chat surface sits behind the local lock gate (CH-LB-6): while
        // locked, the screen composable never composes, so no message text
        // reaches the UI tree, semantics, or the task-switcher snapshot of
        // the gate itself.
        ChatLockGate {
            ChatInboxScreen(
                onBack = onBack,
                onOpenConversation = onOpenThread,
                onOpenRequest = onOpenRequest,
                onCreateGroup = onCreateGroup,
                onOpenLockSettings = onOpenLockSettings,
                onOpenCallHistory = onOpenCallHistory,
            )
        }
    }
}

/** Registers the thread. */
fun NavGraphBuilder.chatThreadScreen(
    onBack: () -> Unit,
    onOpenGroupInfo: (conversationId: String) -> Unit,
    onStartCall: (peerUserId: String, peerName: String, video: Boolean, conversationId: String) -> Unit =
        { _, _, _, _ -> },
) {
    composable<ChatThreadRoute> { entry ->
        val route = entry.toRoute<ChatThreadRoute>()
        ChatLockGate {
            ChatThreadScreen(
                title = route.title,
                isGroup = route.isGroup,
                onOpenGroupInfo = { onOpenGroupInfo(route.conversationId) },
                onBack = onBack,
                onStartCall = { peerUserId, peerName, video ->
                    onStartCall(peerUserId, peerName, video, route.conversationId)
                },
            )
        }
    }
}

/** Registers the request decision screen. */
fun NavGraphBuilder.chatRequestScreen(
    onBack: () -> Unit,
    onAccepted: (conversationId: String, title: String) -> Unit,
    onClosed: () -> Unit,
) {
    composable<ChatRequestRoute> { entry ->
        ChatLockGate {
            ChatRequestScreen(
                title = entry.toRoute<ChatRequestRoute>().title,
                onAccepted = onAccepted,
                onClosed = onClosed,
                onBack = onBack,
            )
        }
    }
}

/**
 * Registers the friends list.
 *
 * [onBack] is null when Friends is registered as a TAB root — the top bar
 * then renders no back control.
 */
fun NavGraphBuilder.friendsScreen(
    onOpenThread: (conversationId: String, title: String) -> Unit,
    onOpenRequests: () -> Unit = {},
    onBack: (() -> Unit)? = null,
) {
    composable<FriendsRoute> {
        ChatLockGate {
            FriendsScreen(
                onOpenThread = onOpenThread,
                onOpenRequests = onOpenRequests,
                onBack = onBack,
            )
        }
    }
}

/** Registers the friend-requests screen. */
fun NavGraphBuilder.friendRequestsScreen(onBack: () -> Unit) {
    composable<FriendRequestsRoute> {
        ChatLockGate {
            FriendRequestsScreen(onBack = onBack)
        }
    }
}

/** Registers the new-group flow. */
fun NavGraphBuilder.groupCreateScreen(
    onBack: () -> Unit,
    onCreated: (conversationId: String, title: String) -> Unit,
) {
    composable<GroupCreateRoute> {
        ChatLockGate {
            GroupCreateScreen(onCreated = onCreated, onBack = onBack)
        }
    }
}

/** Registers group info. */
fun NavGraphBuilder.groupInfoScreen(
    onBack: () -> Unit,
    onLeft: () -> Unit,
) {
    composable<GroupInfoRoute> {
        ChatLockGate {
            GroupInfoScreen(onLeft = onLeft, onBack = onBack)
        }
    }
}

/** Chat lock settings — reachable from the inbox overflow / settings. */
@Serializable
data object ChatLockSettingsRoute

fun NavGraphBuilder.chatLockSettingsScreen(onBack: () -> Unit) {
    composable<ChatLockSettingsRoute> {
        ChatLockSettingsScreen(onBack = onBack)
    }
}

/** Type-safe navigation to chat lock settings. */
fun NavController.navigateToChatLockSettings() = navigate(ChatLockSettingsRoute)

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

/** Type-safe navigation to the friends list. */
fun NavController.navigateToFriends() = navigate(FriendsRoute)

/** Type-safe navigation to friend requests. */
fun NavController.navigateToFriendRequests() = navigate(FriendRequestsRoute)

/** Type-safe navigation to the new-group flow. */
fun NavController.navigateToGroupCreate() = navigate(GroupCreateRoute)

/** Type-safe navigation to group info. */
fun NavController.navigateToGroupInfo(conversationId: String) =
    navigate(GroupInfoRoute(conversationId))
