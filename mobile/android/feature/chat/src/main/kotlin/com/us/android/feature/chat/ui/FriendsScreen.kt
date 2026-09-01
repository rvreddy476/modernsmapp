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
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Badge
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * The Friends surface, per the Figma friends frame (140:199): search, the
 * three lenses (All / Close Friends / Recently Added), a count line, and
 * card rows with presence and a message shortcut.
 *
 * [onBack] is null when this is the Friends TAB and non-null when pushed.
 */
@Composable
fun FriendsScreen(
    onOpenThread: (conversationId: String, title: String) -> Unit,
    onOpenRequests: () -> Unit = {},
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
        topBar = {
            UsTopBar(
                title = "Friends",
                onBack = onBack,
                actions = {
                    Box {
                        IconButton(
                            onClick = onOpenRequests,
                            modifier = Modifier.testTag("friends-requests"),
                        ) {
                            Icon(
                                imageVector = UsIcons.UserPlus,
                                contentDescription = "Friend requests" +
                                    if (state.pendingRequestCount > 0) {
                                        ", ${state.pendingRequestCount} pending"
                                    } else {
                                        ""
                                    },
                                tint = UsTheme.extended.textPrimary,
                            )
                        }
                        if (state.pendingRequestCount > 0) {
                            Badge(
                                containerColor = FRIENDS_ACCENT,
                                modifier = Modifier.align(Alignment.TopEnd),
                            ) { Text(state.pendingRequestCount.toString()) }
                        }
                    }
                },
            )
        },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            FriendsSearchField(query = state.query, onQueryChange = viewModel::onQueryChange)
            FriendsTabs(selected = state.tab, onSelect = viewModel::selectTab)

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
                else -> FriendsList(state = state, viewModel = viewModel)
            }
        }
    }
}

@Composable
private fun FriendsList(state: FriendsUiState, viewModel: FriendsViewModel) {
    Column {
        // "248 FRIENDS TOTAL" (140:234) — the whole list, not the lens.
        Text(
            text = "${state.friends.size} FRIENDS TOTAL",
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            ),
        )
        if (state.visibleFriends.isEmpty()) {
            UsEmptyState(
                title = if (state.tab == FriendsTab.Close) "No close friends yet" else "No matches",
                detail = if (state.tab == FriendsTab.Close) {
                    "Mark friends as close and they gather here."
                } else {
                    "Nobody fits that search in this tab."
                },
            )
        } else {
            LazyColumn(
                verticalArrangement = Arrangement.spacedBy(ROW_GAP),
                modifier = Modifier.testTag("friends-list"),
            ) {
                items(state.visibleFriends, key = { it.userId }) { friend ->
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

/** The search field (140:218): a full-width soft card, not a pill. */
@Composable
private fun FriendsSearchField(query: String, onQueryChange: (String) -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            )
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCardSolid)
            .padding(
                horizontal = UsTheme.spacing.l,
                vertical = UsTheme.spacing.l,
            ),
    ) {
        Icon(
            imageVector = UsIcons.Explore,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(SEARCH_GLYPH),
        )
        Box(modifier = Modifier.weight(1f)) {
            if (query.isEmpty()) {
                Text(
                    text = "Search friends…",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                )
            }
            BasicTextField(
                value = query,
                onValueChange = onQueryChange,
                singleLine = true,
                textStyle = MaterialTheme.typography.bodyMedium.copy(
                    color = UsTheme.extended.textPrimary,
                ),
                cursorBrush = SolidColor(FRIENDS_ACCENT),
                modifier = Modifier
                    .fillMaxWidth()
                    .semantics { contentDescription = "Search friends" }
                    .testTag("friends-search"),
            )
        }
    }
}

/** The three lenses (140:223): equal thirds, orange underline on the live one. */
@Composable
private fun FriendsTabs(selected: FriendsTab, onSelect: (FriendsTab) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
    ) {
        FriendsTab.entries.forEach { tab ->
            val active = tab == selected
            Column(
                horizontalAlignment = Alignment.CenterHorizontally,
                modifier = Modifier
                    .weight(1f)
                    .clip(RoundedCornerShape(UsTheme.radii.small))
                    .clickable { onSelect(tab) }
                    .padding(vertical = UsTheme.spacing.s)
                    .testTag("friends-tab-${tab.name}"),
            ) {
                Text(
                    text = tab.label,
                    style = MaterialTheme.typography.labelLarge,
                    fontWeight = if (active) FontWeight.Bold else FontWeight.Medium,
                    color = if (active) UsTheme.extended.textPrimary else UsTheme.extended.textMuted,
                    maxLines = 1,
                )
                Box(
                    modifier = Modifier
                        .padding(top = UsTheme.spacing.xs)
                        .width(TAB_INDICATOR_WIDTH)
                        .height(TAB_INDICATOR_HEIGHT)
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .background(if (active) FRIENDS_ACCENT else Color.Transparent),
                )
            }
        }
    }
}

/** One friend (140:236): 68dp card, 44dp avatar, identity line, message glyph. */
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
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.bgCardSolid)
            .clickable(enabled = enabled, onClick = onMessage)
            .padding(ROW_PADDING),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(ROW_PADDING),
    ) {
        Box {
            UsAvatar(name = friend.displayName, size = UsAvatarSize.Medium, seed = friend.userId)
            if (friend.online) {
                Box(
                    modifier = Modifier
                        .size(ONLINE_DOT)
                        .align(Alignment.BottomEnd)
                        .offset(x = -DOT_INSET, y = -DOT_INSET)
                        .clip(CircleShape)
                        .background(UsTheme.extended.chatOnline)
                        .border(ONLINE_DOT_RING, UsTheme.extended.bgCardSolid, CircleShape),
                )
            }
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = friend.displayName,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            // "@handle • Active now" (140:242). Presence is a boolean here —
            // there is no last-seen feed — so the row says "Active now" or
            // says nothing, rather than inventing an "Active 2h ago".
            val identity = buildList {
                if (friend.username.isNotBlank()) add("@${friend.username}")
                if (friend.online) add("Active now")
            }.joinToString(" • ")
            if (identity.isNotBlank()) {
                Text(
                    text = identity,
                    style = MaterialTheme.typography.bodySmall,
                    color = if (friend.online) {
                        UsTheme.extended.chatOnline
                    } else {
                        UsTheme.extended.textMuted
                    },
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        if (opening) {
            Text(
                text = "Opening…",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textMuted,
            )
        } else {
            Icon(
                imageVector = UsIcons.Comment,
                contentDescription = "Message ${friend.displayName}",
                tint = UsTheme.extended.textMuted,
                modifier = Modifier.size(MESSAGE_GLYPH),
            )
        }
    }
}

// ── The Figma friends frame (140:199) ───────────────────────────────────

/** The frame's accent — tabs, badges, cursor. */
@Suppress("MagicNumber")
internal val FRIENDS_ACCENT = Color(0xFFF97316)

private val ROW_GAP = 12.dp
private val ROW_PADDING = 12.dp
private val TAB_INDICATOR_WIDTH = 40.dp
private val TAB_INDICATOR_HEIGHT = 2.dp
private val SEARCH_GLYPH = 20.dp
private val MESSAGE_GLYPH = 20.dp
private val ONLINE_DOT = 12.dp
private val ONLINE_DOT_RING = 2.dp
private val DOT_INSET = 2.dp
