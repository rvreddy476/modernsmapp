package com.us.android.feature.chat.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * All the viewer's friends (accepted connections), one Message tap from a
 * thread — the design's friends frame (98:226) in the chat green language.
 *
 * [onBack] is null when this is the Friends TAB and non-null when it was
 * pushed from the inbox. A tab root with a back arrow sends the user
 * somewhere they did not come from.
 */
@Composable
fun FriendsScreen(
    onOpenThread: (conversationId: String, title: String) -> Unit,
    onBack: (() -> Unit)? = null,
    viewModel: FriendsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    // Consumed once and cleared, or the thread would be pushed again every
    // time this screen resumed.
    LaunchedEffect(state.openConversation) {
        state.openConversation?.let { open ->
            onOpenThread(open.conversationId, open.title)
            viewModel.onConversationOpened()
        }
    }

    UsScaffold(
        topBar = { UsTopBar(title = "Friends", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            state.error?.let { message ->
                Text(
                    text = "$message Tap to dismiss.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable(onClick = viewModel::dismissError)
                        .padding(
                            horizontal = UsTheme.spacing.pageHorizontal,
                            vertical = UsTheme.spacing.s,
                        ),
                )
            }
            when {
                state.loading -> UsLoadingState(label = "Loading your friends")
                state.friends.isEmpty() && state.error != null -> UsErrorState(
                    message = "Your friends couldn't be loaded.",
                    onRetry = viewModel::load,
                )
                state.friends.isEmpty() -> UsEmptyState(
                    title = "No friends yet",
                    detail = "Send a friend request from someone's profile — " +
                        "accepted requests show up here.",
                )
                else -> LazyColumn(modifier = Modifier.testTag("friends-list")) {
                    items(state.friends, key = { it.userId }) { friend ->
                        FriendRow(
                            friend = friend,
                            opening = state.openingUserId == friend.userId,
                            enabled = state.openingUserId == null,
                            onMessage = { viewModel.message(friend) },
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun FriendRow(
    friend: Friend,
    opening: Boolean,
    enabled: Boolean,
    onMessage: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(enabled = enabled, onClick = onMessage)
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            ),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Box {
            UsAvatar(name = friend.displayName, size = UsAvatarSize.Medium, seed = friend.userId)
            if (friend.online) {
                Box(
                    modifier = Modifier
                        .size(ONLINE_DOT)
                        .align(Alignment.BottomEnd)
                        .clip(CircleShape)
                        .background(UsTheme.extended.chatOnline)
                        .border(ONLINE_DOT_RING, UsTheme.extended.bgCanvas, CircleShape),
                )
            }
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = friend.displayName,
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
            )
            if (friend.online) {
                Text(
                    text = "Online",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.chatOnline,
                )
            }
        }
        MessageChip(opening = opening, enabled = enabled, onClick = onMessage)
    }
}

/** The green pill from the chat language; the whole row triggers it too. */
@Composable
private fun MessageChip(opening: Boolean, enabled: Boolean, onClick: () -> Unit) {
    Text(
        text = if (opening) "Opening…" else "Message",
        style = MaterialTheme.typography.labelLarge,
        fontWeight = FontWeight.SemiBold,
        color = Color.White,
        modifier = Modifier
            .clip(CircleShape)
            .background(
                if (enabled || opening) {
                    UsTheme.extended.chatAccent
                } else {
                    UsTheme.extended.textGhost
                },
            )
            .clickable(enabled = enabled, onClick = onClick)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs),
    )
}

private val ONLINE_DOT = 12.dp
private val ONLINE_DOT_RING = 2.dp
