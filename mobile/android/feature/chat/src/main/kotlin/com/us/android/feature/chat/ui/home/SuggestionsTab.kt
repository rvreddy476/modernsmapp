package com.us.android.feature.chat.ui.home

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.chat.data.CommunitySuggestion
import com.us.android.core.chat.data.PersonSuggestion
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsFollowButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.suggestionButton
import com.us.android.core.ui.UsLoadingState

/**
 * Suggestions (founder, 2026-09-05: "so the user follows people and spends
 * more time in chat"): "People you may know" cards — avatar, name, the
 * engine's reason, Follow, × — then "Communities to join". An
 * impression is posted once per shown batch; Follow, Join and × post an
 * action. Empty when both lists are.
 *
 * Follow is the only button a person carries (founder, 24 Oct 2026): no
 * Message, no Connect, no Add friend ([suggestionButton]).
 */
@Composable
internal fun SuggestionsTab(
    state: ChatHomeUiState,
    onFollow: (String) -> Unit,
    onDismiss: (String) -> Unit,
    onJoinCommunity: (CommunitySuggestion) -> Unit,
    onOpenCommunity: (String) -> Unit,
) {
    val people = state.visiblePeople
    val communities = state.visibleSuggestedCommunities
    if (!state.suggestionsLoaded) {
        UsLoadingState(label = "Finding people you may know")
        return
    }
    if (people.isEmpty() && communities.isEmpty()) {
        SuggestionsEmptyState(searching = state.query.isNotBlank())
        return
    }
    LazyColumn(
        modifier = Modifier.fillMaxSize().testTag("chat_home_suggestions"),
        verticalArrangement = Arrangement.spacedBy(CARD_GAP),
        contentPadding = PaddingValues(bottom = LIST_BOTTOM),
    ) {
        if (people.isNotEmpty()) {
            item(key = "people-label") { ChatSectionLabel("People you may know") }
            items(people, key = { "person:${it.userId}" }) { person ->
                PersonCard(
                    person = person,
                    edge = state.followEdges[person.userId],
                    busy = person.userId in state.busyPeopleIds,
                    onFollow = { onFollow(person.userId) },
                    onDismiss = { onDismiss(person.userId) },
                )
            }
        }
        if (communities.isNotEmpty()) {
            item(key = "communities-label") { ChatSectionLabel("Communities to join") }
            items(communities, key = { "community:${it.communityId}" }) { community ->
                CommunityCard(
                    facts = CommunityCardFacts(
                        name = community.name,
                        handle = community.handleForDisplay,
                        avatarMediaId = community.avatarMediaId,
                        memberCount = community.memberCount,
                        reason = community.explainText.ifBlank { community.description },
                    ),
                    joined = false,
                    busy = community.communityId in state.busyCommunityIds,
                    onOpen = { onOpenCommunity(community.communityId) },
                    onTogglePill = { onJoinCommunity(community) },
                    tag = "chat_home_suggested_community:${community.communityId}",
                )
            }
        }
    }
}

/**
 * One person: avatar, name, the reason ("1 mutual friend"), the one button
 * ([suggestionButton]: Follow, or Following / Requested once followed) and ×
 * to dismiss. Nothing else (founder, 24 Oct 2026).
 */
@Composable
internal fun PersonCard(
    person: PersonSuggestion,
    edge: FollowStatus?,
    busy: Boolean,
    onFollow: () -> Unit,
    onDismiss: () -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    val reason = person.explainText.ifBlank {
        when (person.mutualFriendCount) {
            0 -> "Suggested for you"
            1 -> "1 mutual friend"
            else -> "${person.mutualFriendCount} mutual friends"
        }
    }
    Column(
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = CARD_MARGIN)
            .background(UsTheme.extended.bgCardSolid, shape)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .padding(CARD_PADDING)
            .testTag("chat_home_person:${person.userId}"),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)
        ) {
            UsAvatar(name = person.displayName, size = UsAvatarSize.Chat, seed = person.userId)
            Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(LINE_GAP)) {
                Text(
                    text = person.displayName,
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = reason,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            HeaderGlyph(
                icon = UsIcons.Close,
                description = "Dismiss ${person.displayName}",
                onClick = onDismiss,
                tag = "chat_home_person_dismiss:${person.userId}",
                size = DISMISS_TARGET,
                glyph = DISMISS_GLYPH,
                tint = UsTheme.extended.textMuted,
            )
        }
        Row(
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            verticalAlignment = Alignment.CenterVertically
        ) {
            val button = suggestionButton(edge)
            if (button.tappable) {
                UsFollowButton(
                    onClick = onFollow,
                    text = button.label,
                    busy = busy,
                    modifier = Modifier.testTag("chat_home_person_follow:${person.userId}"),
                )
            } else {
                ChatTogglePill(text = button.label, selected = true, onClick = {})
            }
        }
    }
}

@Composable
private fun SuggestionsEmptyState(searching: Boolean) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = EMPTY_TOP)
            .testTag("chat_home_suggestions_empty"),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(EMPTY_ICON)
                .background(
                    UsTheme.extended.launcher.friends.brush,
                    RoundedCornerShape(EMPTY_ICON / ICON_CORNER_DIVISOR)
                ),
        ) {
            Icon(
                imageVector = UsIcons.HeartHandshake,
                contentDescription = null,
                tint = UsTheme.extended.onTile,
                modifier = Modifier.size(EMPTY_GLYPH)
            )
        }
        Text(
            text = if (searching) "No suggestions match" else "Nothing to suggest yet",
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
        )
        Text(
            text = if (searching) {
                "Try another name."
            } else {
                "Follow a few people and join a community — suggestions grow from what you do here."
            },
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            textAlign = TextAlign.Center,
        )
    }
}

private const val ICON_CORNER_DIVISOR = 3
private val CARD_GAP = 8.dp
private val CARD_MARGIN = 12.dp
private val CARD_PADDING = 12.dp
private val LINE_GAP = 2.dp
private val HAIRLINE = 1.dp
private val DISMISS_TARGET = 32.dp
private val DISMISS_GLYPH = 18.dp
private val EMPTY_TOP = 40.dp
private val EMPTY_ICON = 72.dp
private val EMPTY_GLYPH = 34.dp
