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
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.chat.data.Community
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsLoadingState

/**
 * Communities: the "Create community" pill, then "Your communities" from
 * `/my` and "Discover" from `/discover` (paged; the pill's text is sent as
 * `q`). Each card: avatar, name, @handle, member count, Join/Joined.
 */
@Composable
internal fun CommunitiesTab(
    state: ChatHomeUiState,
    onCreate: () -> Unit,
    onOpen: (String) -> Unit,
    onToggleMembership: (Community) -> Unit,
    onLoadMore: () -> Unit,
) {
    val mine = state.visibleMyCommunities
    val discover = state.visibleDiscover
    if (!state.communitiesLoaded && state.discoverLoading) {
        UsLoadingState(label = "Loading communities")
        return
    }
    LazyColumn(
        modifier = Modifier.fillMaxSize().testTag("chat_home_communities"),
        verticalArrangement = Arrangement.spacedBy(CARD_GAP),
        contentPadding = PaddingValues(bottom = LIST_BOTTOM),
    ) {
        item(key = "create") {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
            ) {
                ChatActionPill(
                    text = "Create community",
                    icon = UsIcons.Radio,
                    onClick = onCreate,
                    tag = "chat_home_create_community",
                )
            }
        }
        if (mine.isNotEmpty()) {
            item(key = "mine-label") { ChatSectionLabel("Your communities") }
            items(mine, key = { "mine:${it.id}" }) { community ->
                CommunityCard(
                    facts = community.cardFacts(),
                    joined = community.isMember,
                    busy = community.id in state.busyCommunityIds,
                    onOpen = { onOpen(community.id) },
                    onTogglePill = { onToggleMembership(community) },
                    tag = "chat_home_community:${community.id}",
                    role = community.viewerRole.takeIf { community.isAdmin },
                )
            }
        }
        item(key = "discover-label") { ChatSectionLabel("Discover") }
        if (discover.isEmpty() && !state.discoverLoading) {
            item(key = "discover-empty") {
                CommunitiesEmptyState(
                    searching = state.query.isNotBlank(),
                    nothingJoined = mine.isEmpty(),
                    onCreate = onCreate
                )
            }
        }
        items(discover, key = { "discover:${it.id}" }) { community ->
            CommunityCard(
                facts = community.cardFacts(),
                joined = community.isMember,
                busy = community.id in state.busyCommunityIds,
                onOpen = { onOpen(community.id) },
                onTogglePill = { onToggleMembership(community) },
                tag = "chat_home_discover:${community.id}",
            )
        }
        if (state.discoverCursor != null) {
            item(key = "discover-more") {
                LaunchedEffect(state.discover.size) { onLoadMore() }
                UsLoadingState(label = "Loading more", modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.l))
            }
        } else if (state.discoverLoading) {
            item(key = "discover-loading") {
                UsLoadingState(label = "Searching", modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.l))
            }
        }
    }
}

@Composable
private fun CommunitiesEmptyState(searching: Boolean, nothingJoined: Boolean, onCreate: () -> Unit) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.xxxxl)
            .testTag("chat_home_communities_empty"),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(EMPTY_ICON)
                .background(UsTheme.extended.launcher.live.brush, RoundedCornerShape(EMPTY_ICON / ICON_CORNER_DIVISOR)),
        ) {
            Icon(
                imageVector = UsIcons.Radio,
                contentDescription = null,
                tint = Color.White,
                modifier = Modifier.size(EMPTY_GLYPH)
            )
        }
        Text(
            text = when {
                searching -> "No communities match"
                nothingJoined -> "Communities are broadcast channels"
                else -> "Nothing new to discover"
            },
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
        )
        Text(
            text = when {
                searching -> "Try a name or an @handle."
                else -> "Anyone can start one. Members get every update and event; only the admins post."
            },
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            textAlign = TextAlign.Center,
        )
        if (!searching) {
            ChatActionPill(text = "Create community", icon = UsIcons.Radio, onClick = onCreate)
        }
    }
}

/**
 * One community as a card: the avatar (resolved from its media id), the
 * name with a verified-style role tag for the viewer's own, the @handle,
 * the member count, a line of reason or description, and the pill.
 */
/** What a community card shows about its community — the same five facts whichever list it sits in. */
internal data class CommunityCardFacts(
    val name: String,
    val handle: String,
    val avatarMediaId: String?,
    val memberCount: Int,
    val reason: String,
)

internal fun Community.cardFacts() = CommunityCardFacts(name, handleForDisplay, avatarMediaId, memberCount, description)

@Composable
internal fun CommunityCard(
    facts: CommunityCardFacts,
    joined: Boolean,
    busy: Boolean,
    onOpen: () -> Unit,
    onTogglePill: () -> Unit,
    modifier: Modifier = Modifier,
    tag: String? = null,
    role: String? = null,
) {
    val name = facts.name
    val handle = facts.handle
    val reason = facts.reason
    val shape = RoundedCornerShape(UsTheme.radii.large)
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = CARD_MARGIN)
            .background(UsTheme.extended.bgCardSolid, shape)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .pressScale(onOpen)
            .padding(CARD_PADDING)
            .then(if (tag != null) Modifier.testTag(tag) else Modifier),
    ) {
        UsAvatar(
            name = name,
            size = UsAvatarSize.Chat,
            seed = handle,
            imageUrl = rememberMediaUrl(facts.avatarMediaId),
        )
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(LINE_GAP)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)
            ) {
                Text(
                    text = name,
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
                if (role != null) {
                    Text(
                        text = role.replaceFirstChar { it.uppercase() },
                        style = MaterialTheme.typography.labelSmall,
                        fontWeight = FontWeight.Bold,
                        color = UsTheme.extended.accentSolid,
                    )
                }
            }
            Text(
                text = "$handle · ${memberCountLabel(facts.memberCount)}",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (reason.isNotBlank()) {
                Text(
                    text = reason,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textBody,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        ChatTogglePill(
            text = if (joined) "Joined" else "Join",
            selected = joined,
            onClick = onTogglePill,
            busy = busy,
        )
    }
}

internal fun memberCountLabel(count: Int): String = when {
    count == 1 -> "1 member"
    count >= THOUSAND -> "${count / THOUSAND}.${count % THOUSAND / HUNDRED}k members"
    else -> "$count members"
}

private const val THOUSAND = 1000
private const val HUNDRED = 100
private const val ICON_CORNER_DIVISOR = 3
private val CARD_GAP = 8.dp
private val CARD_MARGIN = 12.dp
private val CARD_PADDING = 12.dp
private val LINE_GAP = 2.dp
private val HAIRLINE = 1.dp
private val EMPTY_ICON = 72.dp
private val EMPTY_GLYPH = 34.dp
