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
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * Friend requests, per the Figma requests frame (140:104): Received cards
 * answered with a green Accept and an outlined Decline; Sent cards watched,
 * with an outlined Cancel to take one back.
 */
@Composable
fun FriendRequestsScreen(
    onBack: () -> Unit,
    viewModel: FriendRequestsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        topBar = { UsTopBar(title = "Requests", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            RequestsTabs(selected = state.tab, onSelect = viewModel::selectTab)

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
                state.loading -> UsLoadingState(label = "Loading requests")
                state.visible.isEmpty() && state.error != null -> UsErrorState(
                    message = "Requests couldn't be loaded.",
                    onRetry = viewModel::load,
                )
                state.visible.isEmpty() -> UsEmptyState(
                    title = "All caught up",
                    detail = state.emptyText,
                )
                else -> LazyColumn(
                    verticalArrangement = Arrangement.spacedBy(CARD_GAP),
                    modifier = Modifier
                        .padding(top = UsTheme.spacing.s)
                        .testTag("requests-list"),
                ) {
                    items(state.visible, key = { it.userId }) { item ->
                        RequestCard(
                            item = item,
                            received = state.tab == RequestsTab.Received,
                            onAccept = { viewModel.accept(item) },
                            onDecline = { viewModel.decline(item) },
                            onCancel = { viewModel.cancel(item) },
                        )
                    }
                }
            }
        }
    }
}

/** Received | Sent (140:123): halves, orange underline on the live side. */
@Composable
private fun RequestsTabs(selected: RequestsTab, onSelect: (RequestsTab) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
    ) {
        RequestsTab.entries.forEach { tab ->
            val active = tab == selected
            Column(
                horizontalAlignment = Alignment.CenterHorizontally,
                modifier = Modifier
                    .weight(1f)
                    .clip(RoundedCornerShape(UsTheme.radii.small))
                    .clickable { onSelect(tab) }
                    .padding(vertical = UsTheme.spacing.s)
                    .testTag("requests-tab-${tab.name}"),
            ) {
                Text(
                    text = tab.label,
                    style = MaterialTheme.typography.labelLarge,
                    fontWeight = if (active) FontWeight.Bold else FontWeight.Medium,
                    color = if (active) UsTheme.extended.textPrimary else UsTheme.extended.textMuted,
                )
                Box(
                    modifier = Modifier
                        .padding(top = UsTheme.spacing.xs)
                        .width(SEGMENT_INDICATOR_WIDTH)
                        .height(SEGMENT_INDICATOR_HEIGHT)
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .background(if (active) FRIENDS_ACCENT else Color.Transparent),
                )
            }
        }
    }
}

/**
 * One request (140:131): identity row, then the decision row. The whole card
 * dims while its decision is in flight — the server's answer moves it, not
 * the tap.
 */
@Composable
private fun RequestCard(
    item: FriendRequestItem,
    received: Boolean,
    onAccept: () -> Unit,
    onDecline: () -> Unit,
    onCancel: () -> Unit,
) {
    Column(
        verticalArrangement = Arrangement.spacedBy(CARD_PADDING),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .clip(RoundedCornerShape(CARD_CORNER))
            .background(UsTheme.extended.bgCardSolid)
            .alpha(if (item.busy) BUSY_ALPHA else 1f)
            .padding(CARD_PADDING)
            .testTag("request-card"),
    ) {
        RequestIdentityRow(item = item)
        if (received) {
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                DecisionButton(
                    label = "Accept",
                    filled = true,
                    enabled = !item.busy,
                    onClick = onAccept,
                    modifier = Modifier.weight(1f).testTag("request-accept"),
                )
                DecisionButton(
                    label = "Decline",
                    filled = false,
                    enabled = !item.busy,
                    onClick = onDecline,
                    modifier = Modifier.weight(1f).testTag("request-decline"),
                )
            }
        } else {
            DecisionButton(
                label = "Cancel request",
                filled = false,
                enabled = !item.busy,
                onClick = onCancel,
                modifier = Modifier.fillMaxWidth().testTag("request-cancel"),
            )
        }
    }
}

/** The identity half of a card: avatar, name, detail line, Suggest badge. */
@Composable
private fun RequestIdentityRow(item: FriendRequestItem) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(CARD_PADDING),
    ) {
        UsAvatar(name = item.displayName, size = UsAvatarSize.Chat, seed = item.userId)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = item.displayName,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            // The design's line is "4 mutual friends • 2d ago"; the graph
            // does not expose mutual counts, so the honest line is the
            // request's own words (when it carries any) plus its age.
            val detail = listOfNotNull(
                item.message,
                formatRelativeTime(item.createdAt).takeIf { it.isNotBlank() }
                    ?.let { if (it.equals("now", ignoreCase = true)) "just now" else "$it ago" },
            ).joinToString(" • ")
            if (detail.isNotBlank()) {
                Text(
                    text = detail,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        if (item.suggested) {
            Text(
                text = "Suggest",
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.Bold,
                color = FRIENDS_ACCENT,
                modifier = Modifier
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .background(FRIENDS_ACCENT.copy(alpha = BADGE_BG_ALPHA))
                    .padding(
                        horizontal = UsTheme.spacing.s,
                        vertical = UsTheme.spacing.xs,
                    ),
            )
        }
    }
}

/** The frame's two button voices: solid green yes, outlined quiet no. */
@Composable
private fun DecisionButton(
    label: String,
    filled: Boolean,
    enabled: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .clip(RoundedCornerShape(BUTTON_CORNER))
            .then(
                if (filled) {
                    Modifier.background(UsTheme.extended.chatAccent)
                } else {
                    Modifier.border(
                        width = BUTTON_BORDER,
                        color = UsTheme.extended.borderMedium,
                        shape = RoundedCornerShape(BUTTON_CORNER),
                    )
                },
            )
            .clickable(enabled = enabled, onClick = onClick)
            .padding(vertical = UsTheme.spacing.m),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = if (filled) FontWeight.Bold else FontWeight.SemiBold,
            color = if (filled) Color.White else UsTheme.extended.textMuted,
        )
    }
}

// ── The Figma requests frame (140:104) ──────────────────────────────────

private val CARD_GAP = 12.dp
private val CARD_PADDING = 14.dp
private val CARD_CORNER = 16.dp
private val BUTTON_CORNER = 12.dp
private val BUTTON_BORDER = 1.dp
private val SEGMENT_INDICATOR_WIDTH = 60.dp
private val SEGMENT_INDICATOR_HEIGHT = 2.dp
private const val BUSY_ALPHA = 0.6f
private const val BADGE_BG_ALPHA = 0.13f
