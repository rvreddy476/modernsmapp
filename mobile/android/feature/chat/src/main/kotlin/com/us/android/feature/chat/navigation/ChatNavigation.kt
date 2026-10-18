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
import com.us.android.feature.chat.ui.GroupCreateScreen
import com.us.android.feature.chat.ui.GroupInfoScreen
import com.us.android.feature.chat.ui.InvitationsScreen
import com.us.android.feature.chat.ui.RequestsListScreen
import com.us.android.feature.chat.ui.community.CommunityAdminsScreen
import com.us.android.feature.chat.ui.community.CommunityCreateScreen
import com.us.android.feature.chat.ui.community.CommunityPageDestinations
import com.us.android.feature.chat.ui.community.CommunityPageScreen
import com.us.android.feature.chat.ui.community.CommunityPostScreen
import com.us.android.feature.chat.ui.group.GroupAddMembersScreen
import com.us.android.feature.chat.ui.group.JoinByLinkScreen
import com.us.android.feature.chat.ui.home.ChatHomeDestinations
import com.us.android.feature.chat.ui.home.ChatHomeScreen
import kotlinx.serialization.Serializable

/**
 * The one chat screen — Chats | Groups | Communities | Suggestions — the
 * Messages top-level root since 2026-09-05.
 */
@Serializable
data object ChatHomeRoute

/** The list of conversations. The 2026-08 inbox, still registered for older entry points. */
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

/** The pending message requests, listed. */
@Serializable
data object ChatRequestsListRoute

/** The pending group invitations, listed with Accept / Decline. */
@Serializable
data object InvitationsRoute

/** The new-group flow. */
@Serializable
data object GroupCreateRoute

/** Group info + administration for one group conversation. */
@Serializable
data class GroupInfoRoute(val conversationId: String)

/** The multi-select people picker that adds members to a group. */
@Serializable
data class GroupAddMembersRoute(val conversationId: String)

/**
 * Join a group by invite link. [code] is the link's code when the app was
 * opened by `https://atpost.app/chat/join/{code}`; blank for the in-app
 * "Join with link", which asks for the link.
 */
@Serializable
data class JoinByLinkRoute(val code: String = "")

/** Create a community. */
@Serializable
data object CommunityCreateRoute

/** Edit a community (owner/admin). */
@Serializable
data class CommunityEditRoute(val communityId: String)

/** A community's page: header and updates. */
@Serializable
data class CommunityPageRoute(val communityId: String)

/** The owner's admin roster. */
@Serializable
data class CommunityAdminsRoute(val communityId: String)

/** The admin composer for one community. */
@Serializable
data class CommunityPostRoute(val communityId: String)

/** Registers the one chat screen as the Messages root. */
fun NavGraphBuilder.chatHomeScreen(destinations: ChatHomeDestinations) {
    composable<ChatHomeRoute> {
        // EVERY chat surface sits behind the local lock gate (CH-LB-6): while
        // locked, the screen composable never composes, so no message text
        // reaches the UI tree, semantics, or the task-switcher snapshot of
        // the gate itself.
        ChatLockGate { ChatHomeScreen(destinations = destinations) }
    }
}

/**
 * Registers the inbox.
 *
 * [onOpenThread] takes the id AND the resolved title, because resolving a
 * direct conversation's title needs the viewer's own id to pick the other
 * member — which the inbox has and `:app` does not.
 */
fun NavGraphBuilder.chatInboxScreen(
    /** Null when the inbox is the Messages TAB — a tab root has no back arrow. */
    onBack: (() -> Unit)?,
    onOpenThread: (conversationId: String, title: String, isGroup: Boolean) -> Unit,
    onOpenRequest: (conversationId: String, title: String) -> Unit,
    onCreateGroup: () -> Unit,
    onOpenLockSettings: () -> Unit = {},
    onOpenCallHistory: () -> Unit = {},
) {
    composable<ChatInboxRoute> {
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
    // Kind messages: the report flow the conversation's owner offered for a sender.
    onReportSender: (senderId: String, messageId: String, name: String) -> Unit = { _, _, _ -> },
    // A shared post's card (2026-10-17): :app opens the post where it plays.
    onOpenPost: (postId: String, contentType: String) -> Unit = { _, _ -> },
    // Chat parity (2026-10-18): where the thread's new affordances lead.
    parity: ThreadParityDestinations? = null,
) {
    composable<ChatThreadRoute> { entry ->
        val route = entry.toRoute<ChatThreadRoute>()
        ChatLockGate {
            ChatThreadScreen(
                links = parity?.let { destinations ->
                    com.us.android.feature.chat.ui.ThreadLinks(
                        onOpenChatSettings = { destinations.onOpenChatSettings(route.conversationId) },
                        onOpenProfile = destinations.onOpenProfile,
                        onOpenSound = destinations.onOpenSound,
                        onOpenLive = destinations.onOpenLive,
                        onOpenSharedReels = { postId -> destinations.onOpenSharedReels(route.conversationId, postId) },
                    )
                } ?: com.us.android.feature.chat.ui.ThreadLinks(),
                title = route.title,
                isGroup = route.isGroup,
                onOpenGroupInfo = { onOpenGroupInfo(route.conversationId) },
                onBack = onBack,
                onStartCall = { peerUserId, peerName, video ->
                    onStartCall(peerUserId, peerName, video, route.conversationId)
                },
                onReportSender = onReportSender,
                onOpenPost = onOpenPost,
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

/** Registers the requests list and the invitations list. */
fun NavGraphBuilder.chatListScreens(
    onBack: () -> Unit,
    onOpenRequest: (conversationId: String, title: String) -> Unit,
) {
    composable<ChatRequestsListRoute> {
        ChatLockGate { RequestsListScreen(onOpenRequest = onOpenRequest, onBack = onBack) }
    }
    composable<InvitationsRoute> {
        ChatLockGate { InvitationsScreen(onBack = onBack) }
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

/** Registers group info, the add-members picker and join-by-link. */
fun NavGraphBuilder.groupScreens(
    onBack: () -> Unit,
    onLeft: () -> Unit,
    onAddMembers: (conversationId: String) -> Unit,
    onJoined: (conversationId: String, title: String) -> Unit,
) {
    composable<GroupInfoRoute> {
        ChatLockGate {
            GroupInfoScreen(onLeft = onLeft, onBack = onBack, onAddMembers = onAddMembers)
        }
    }
    composable<GroupAddMembersRoute> {
        ChatLockGate { GroupAddMembersScreen(onDone = onBack, onBack = onBack) }
    }
    composable<JoinByLinkRoute> {
        ChatLockGate { JoinByLinkScreen(onJoined = onJoined, onBack = onBack) }
    }
}

/** Where the community screens can send the user. */
data class CommunityDestinations(
    val onBack: () -> Unit,
    /** A created or edited community lands on its page, replacing the form. */
    val onSaved: (communityId: String) -> Unit,
    val onEdit: (communityId: String) -> Unit,
    val onAdmins: (communityId: String) -> Unit,
    val onPost: (communityId: String) -> Unit,
    /** The page closed itself — the viewer left or deleted the community. */
    val onClosed: () -> Unit,
)

/** Registers create/edit, the page, the admins roster and the composer. */
fun NavGraphBuilder.communityScreens(destinations: CommunityDestinations) {
    composable<CommunityCreateRoute> {
        ChatLockGate {
            CommunityCreateScreen(onSaved = { destinations.onSaved(it.id) }, onBack = destinations.onBack)
        }
    }
    composable<CommunityEditRoute> {
        ChatLockGate {
            CommunityCreateScreen(onSaved = { destinations.onSaved(it.id) }, onBack = destinations.onBack)
        }
    }
    composable<CommunityPageRoute> {
        ChatLockGate {
            CommunityPageScreen(
                destinations = CommunityPageDestinations(
                    onBack = destinations.onBack,
                    onEdit = destinations.onEdit,
                    onAdmins = destinations.onAdmins,
                    onPost = destinations.onPost,
                    onClosed = destinations.onClosed,
                ),
            )
        }
    }
    composable<CommunityAdminsRoute> {
        ChatLockGate { CommunityAdminsScreen(onBack = destinations.onBack) }
    }
    composable<CommunityPostRoute> {
        ChatLockGate { CommunityPostScreen(onPosted = destinations.onBack, onBack = destinations.onBack) }
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

// ── Chat parity (2026-10-18) ───────────────────────────────────────────

/** The new-message people picker. */
@Serializable
data object NewMessageRoute

/** Chats archived from the inbox (`lane=archived`). */
@Serializable
data object ArchivedChatsRoute

/** The chat settings page behind a thread's ⋯. The key matches ChatSettingsViewModel's. */
@Serializable
data class ChatSettingsRoute(val conversationId: String)

/** The swipe viewer over a chat's shared posts, starting at [postId]. Keys match SharedReelsViewModel's. */
@Serializable
data class SharedReelsRoute(val conversationId: String, val postId: String)

/** Where the thread's chat-parity affordances lead; `:app` owns every destination. */
data class ThreadParityDestinations(
    val onOpenChatSettings: (conversationId: String) -> Unit,
    val onOpenProfile: (userId: String) -> Unit,
    val onOpenSound: (soundId: String) -> Unit,
    val onOpenLive: (streamId: String) -> Unit,
    val onOpenSharedReels: (conversationId: String, postId: String) -> Unit,
)

/** Where the chat-parity screens send the user. */
data class ChatParityDestinations(
    val onBack: () -> Unit,
    val onOpenThread: (conversationId: String, title: String, isGroup: Boolean) -> Unit,
    val onOpenProfile: (userId: String) -> Unit,
    val onOpenPost: (postId: String, contentType: String) -> Unit,
    val onOpenSharedReels: (conversationId: String, postId: String) -> Unit,
    /** The chat was deleted or the person blocked: leave its settings AND its thread. */
    val onChatClosed: () -> Unit,
)

/** Registers the new-message picker, archived chats, chat settings and the shared-reel viewer. */
fun NavGraphBuilder.chatParityScreens(destinations: ChatParityDestinations) {
    composable<NewMessageRoute> {
        ChatLockGate {
            com.us.android.feature.chat.ui.NewMessageScreen(
                onBack = destinations.onBack,
                onOpenThread = destinations.onOpenThread,
            )
        }
    }
    composable<ArchivedChatsRoute> {
        ChatLockGate {
            com.us.android.feature.chat.ui.ArchivedChatsScreen(
                onBack = destinations.onBack,
                onOpenThread = destinations.onOpenThread,
            )
        }
    }
    composable<ChatSettingsRoute> { entry ->
        val route = entry.toRoute<ChatSettingsRoute>()
        ChatLockGate {
            com.us.android.feature.chat.ui.ChatSettingsScreen(
                onBack = destinations.onBack,
                onClosed = destinations.onChatClosed,
                onOpenProfile = destinations.onOpenProfile,
                onOpenThread = destinations.onBack,
                onOpenSharedPost = { postId -> destinations.onOpenSharedReels(route.conversationId, postId) },
            )
        }
    }
    composable<SharedReelsRoute> {
        ChatLockGate {
            com.us.android.feature.chat.ui.SharedReelsScreen(
                onBack = destinations.onBack,
                onOpenPost = destinations.onOpenPost,
                onOpenAuthor = destinations.onOpenProfile,
            )
        }
    }
}

fun NavController.navigateToNewMessage() = navigate(NewMessageRoute)

fun NavController.navigateToArchivedChats() = navigate(ArchivedChatsRoute)

fun NavController.navigateToChatSettings(conversationId: String) = navigate(ChatSettingsRoute(conversationId))

fun NavController.navigateToSharedReels(conversationId: String, postId: String) =
    navigate(SharedReelsRoute(conversationId, postId))
