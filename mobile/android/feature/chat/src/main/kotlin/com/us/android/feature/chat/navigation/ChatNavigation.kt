// MatchingDeclarationName: this file is the feature's navigation contract —
// the route types plus the graph and navigation extensions that use them.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.chat.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import com.us.android.feature.chat.ui.ChatInboxScreen
import com.us.android.feature.chat.ui.ChatThreadScreen
import kotlinx.serialization.Serializable

/** The list of conversations. Pushed from the feed's Messages control. */
@Serializable
data object ChatInboxRoute

/**
 * One conversation.
 *
 * [title] is carried on the route rather than fetched, so the top bar has a
 * name on the FIRST frame. The inbox already knows it — a direct thread is
 * named after the other member, and the member list arrives with the
 * conversation — and re-fetching it would make every thread open with a blank
 * or flickering header.
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
)

/**
 * Registers the inbox.
 *
 * [onOpenThread] takes the id AND the resolved title, because resolving a
 * direct conversation's title needs the viewer's own id to pick the other
 * member — which the inbox has and `:app` does not.
 */
fun NavGraphBuilder.chatInboxScreen(
    onBack: () -> Unit,
    onOpenThread: (conversationId: String, title: String) -> Unit,
) {
    composable<ChatInboxRoute> {
        ChatInboxScreen(onBack = onBack, onOpenConversation = onOpenThread)
    }
}

/** Registers the thread. */
fun NavGraphBuilder.chatThreadScreen(onBack: () -> Unit) {
    composable<ChatThreadRoute> { entry ->
        ChatThreadScreen(title = entry.toRoute<ChatThreadRoute>().title, onBack = onBack)
    }
}

/** Type-safe navigation to the inbox. */
fun NavController.navigateToChatInbox() = navigate(ChatInboxRoute)

/** Type-safe navigation to one conversation. */
fun NavController.navigateToChatThread(conversationId: String, title: String) =
    navigate(ChatThreadRoute(conversationId, title))
