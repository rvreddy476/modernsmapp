package com.us.android.feature.profile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.HiddenChannel
import com.us.android.core.profile.data.RestrictedAccount
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * Settings › Privacy › Blocked accounts (web parity, 2026-10-22): who the
 * viewer blocked, each with Unblock (asked first), and who they muted, each
 * with Unmute. Loading, error with Retry, empty, and pull to refresh, as on
 * every list.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun BlockedAccountsScreen(
    onBack: () -> Unit,
    viewModel: BlockedAccountsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val message by viewModel.message.collectAsStateWithLifecycle()
    UsScaffold(topBar = { UsTopBar(title = "Blocked accounts", onBack = onBack) }, applyPageGutter = false) { padding ->
        PullToRefreshBox(
            isRefreshing = state.refreshing,
            onRefresh = viewModel::refresh,
            modifier = Modifier.padding(padding).fillMaxSize(),
        ) {
            when {
                state.loading -> UsLoadingState(label = "Loading blocked accounts")
                state.error != null -> ScrollableState {
                    UsErrorState(message = state.error.orEmpty(), onRetry = viewModel::refresh)
                }
                state.isEmpty -> ScrollableState {
                    UsEmptyState(
                        title = "You haven't blocked anyone",
                        detail = "People you block or mute will show up here, with the way to undo it.",
                        modifier = Modifier.testTag("blocked_accounts_empty"),
                    )
                }
                else -> LazyColumn(modifier = Modifier.fillMaxSize().testTag("blocked_accounts_list")) {
                    accountSection(
                        title = "Blocked",
                        note = "They can't find your profile, see your posts or message you.",
                        rows = state.blocked,
                        action = "Unblock",
                        onAction = viewModel::askUnblock,
                    )
                    accountSection(
                        title = "Muted",
                        note = "Their posts stay out of your feeds. They are not told.",
                        rows = state.muted,
                        action = "Unmute",
                        onAction = viewModel::unmute,
                    )
                }
            }
            UsMessageHost(message = message, onDismiss = viewModel::dismissMessage)
        }
    }
    state.confirmingUnblock?.let { account ->
        AlertDialog(
            onDismissRequest = viewModel::cancelUnblock,
            containerColor = UsTheme.extended.bgSheet,
            titleContentColor = UsTheme.extended.textPrimary,
            textContentColor = UsTheme.extended.textSecondary,
            title = { Text(text = "Unblock ${account.label()}?", style = MaterialTheme.typography.titleMedium) },
            text = {
                Text(
                    text = "They will be able to find your profile and see your posts again. " +
                        "They won't be told you unblocked them.",
                    style = MaterialTheme.typography.bodyMedium,
                )
            },
            confirmButton = {
                UsPillButton(
                    text = "Unblock",
                    onClick = viewModel::unblock,
                    modifier = Modifier.testTag("blocked_accounts_unblock_confirm"),
                )
            },
            dismissButton = { UsPillButton(text = "Cancel", onClick = viewModel::cancelUnblock, filled = false) },
        )
    }
}

private fun LazyListScope.accountSection(
    title: String,
    note: String,
    rows: List<RestrictedAccount>,
    action: String,
    onAction: (RestrictedAccount) -> Unit,
) {
    if (rows.isEmpty()) return
    item(key = "header:$title") { SectionHeader(title = title, note = note) }
    items(rows, key = { "$title:${it.userId}" }) { account ->
        AccountRow(
            name = account.label(),
            username = account.username,
            seed = account.userId,
            avatarMediaId = account.avatarMediaId,
            detail = null,
            action = action,
            onAction = { onAction(account) },
            testTag = "restricted_account:${account.userId}",
            modifier = if (UsTheme.reducedMotion) Modifier else Modifier.animateItem(),
        )
    }
}

/**
 * Settings › Hidden channels (web parity, 2026-10-22): the channels the
 * viewer asked not to be recommended, each with "Show again".
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HiddenChannelsScreen(
    onBack: () -> Unit,
    viewModel: HiddenChannelsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val message by viewModel.message.collectAsStateWithLifecycle()
    UsScaffold(topBar = { UsTopBar(title = "Hidden channels", onBack = onBack) }, applyPageGutter = false) { padding ->
        PullToRefreshBox(
            isRefreshing = state.refreshing,
            onRefresh = viewModel::refresh,
            modifier = Modifier.padding(padding).fillMaxSize(),
        ) {
            when {
                state.loading -> UsLoadingState(label = "Loading hidden channels")
                state.error != null -> ScrollableState {
                    UsErrorState(message = state.error.orEmpty(), onRetry = viewModel::refresh)
                }
                state.isEmpty -> ScrollableState {
                    UsEmptyState(
                        title = "You haven't hidden any channels",
                        detail = "Tap Don't recommend this channel on a reel or a video and it will be listed here.",
                        modifier = Modifier.testTag("hidden_channels_empty"),
                    )
                }
                else -> LazyColumn(modifier = Modifier.fillMaxSize().testTag("hidden_channels_list")) {
                    item(key = "header") {
                        SectionHeader(
                            title = "Not recommended",
                            note = "Their reels and videos stay out of your feeds until you show them again.",
                        )
                    }
                    items(state.channels, key = { it.authorId }) { channel ->
                        AccountRow(
                            name = channel.label(),
                            username = channel.username,
                            seed = channel.authorId,
                            avatarMediaId = channel.avatarMediaId,
                            detail = formatRelativeTime(channel.hiddenAt).takeIf { it.isNotBlank() }?.let { "Hidden $it" },
                            action = "Show again",
                            onAction = { viewModel.showAgain(channel) },
                            testTag = "hidden_channel:${channel.authorId}",
                            modifier = if (UsTheme.reducedMotion) Modifier else Modifier.animateItem(),
                        )
                    }
                }
            }
            UsMessageHost(message = message, onDismiss = viewModel::dismissMessage)
        }
    }
}

/** A state view inside a scrollable, so pull to refresh still works over it. */
@Composable
private fun ScrollableState(content: @Composable () -> Unit) {
    LazyColumn(modifier = Modifier.fillMaxSize()) {
        item { Box(modifier = Modifier.fillParentMaxHeight(STATE_HEIGHT)) { content() } }
    }
}

@Composable
private fun SectionHeader(title: String, note: String) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .padding(top = UsTheme.spacing.l, bottom = UsTheme.spacing.s),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.semantics { heading() },
        )
        Text(text = note, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
    }
}

/** A person, their @handle and a line under it, and the one undo at the end. */
@Suppress("LongParameterList")
@Composable
private fun AccountRow(
    name: String,
    username: String,
    seed: String,
    avatarMediaId: String?,
    detail: String?,
    action: String,
    onAction: () -> Unit,
    testTag: String,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .testTag(testTag),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsAvatar(
            name = name,
            size = UsAvatarSize.Chat,
            seed = seed,
            imageUrl = rememberProfileMediaUrl(avatarMediaId),
        )
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = name,
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (username.isNotBlank()) {
                Text(
                    text = "@$username",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            detail?.let {
                Text(text = it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            }
        }
        UsPillButton(text = action, onClick = onAction, filled = false, modifier = Modifier.testTag("$testTag:action"))
    }
}

private const val STATE_HEIGHT = 0.8f
