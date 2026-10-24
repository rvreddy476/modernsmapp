package com.us.android.feature.profile.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * Connections (2026-10-24, the web's `/connections` and Activity › Requests):
 * an account's connections, and on the viewer's own the requests they were
 * sent (Accept / Decline) and the ones they sent (Cancel). Every row opens
 * that person's profile.
 */
@Composable
fun ConnectionsScreen(
    onBack: () -> Unit,
    onOpenProfile: (userId: String) -> Unit,
    viewModel: ConnectionsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        topBar = { UsTopBar(title = connectionsTitle(state.handle, state.isOwn), onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            if (state.tabs.size > 1) {
                TabRow(
                    selectedTabIndex = state.tabs.indexOf(state.selected),
                    containerColor = UsTheme.extended.bgCanvas,
                    contentColor = UsTheme.extended.textPrimary,
                ) {
                    state.tabs.forEach { tab ->
                        Tab(
                            selected = tab == state.selected,
                            onClick = { viewModel.select(tab) },
                            text = { Text(tab.label, maxLines = 1) },
                            modifier = Modifier.testTag("connections_tab:${tab.wire}"),
                        )
                    }
                }
            }
            Box(modifier = Modifier.fillMaxSize()) {
                when (state.selected) {
                    ConnectionsTab.CONNECTIONS -> PageBody(
                        page = state.connections,
                        label = "connections",
                        emptyTitle = if (state.isOwn) "No connections yet" else "No connections to show",
                        emptyDetail = if (state.isOwn) "People you connect with will be listed here." else null,
                        onRetry = viewModel::retry,
                    ) { rows ->
                        LazyColumn(Modifier.fillMaxSize().testTag("connections_list")) {
                            items(rows, key = { it.userId }) { person ->
                                PersonRow(
                                    userId = person.userId,
                                    name = person.displayName,
                                    username = person.username,
                                    avatarMediaId = person.avatarMediaId,
                                    failed = person.userId in state.failedIds,
                                    onOpen = { onOpenProfile(person.userId) },
                                ) {
                                    if (state.isOwn) {
                                        UsPillButton(
                                            text = "Remove",
                                            filled = false,
                                            busy = person.userId in state.busyIds,
                                            onClick = { viewModel.askRemove(person) },
                                        )
                                    }
                                }
                            }
                        }
                    }

                    ConnectionsTab.RECEIVED -> PageBody(
                        page = state.received,
                        label = "connection requests",
                        emptyTitle = "No connection requests",
                        emptyDetail = "When someone asks to connect, it shows up here.",
                        onRetry = viewModel::retry,
                    ) { rows ->
                        LazyColumn(Modifier.fillMaxSize().testTag("connections_received")) {
                            items(rows, key = { it.userId }) { person ->
                                val busy = person.userId in state.busyIds
                                PersonRow(
                                    userId = person.userId,
                                    name = person.displayName,
                                    username = person.username,
                                    avatarMediaId = person.avatarMediaId,
                                    detail = person.message,
                                    failed = person.userId in state.failedIds,
                                    onOpen = { onOpenProfile(person.userId) },
                                ) {
                                    Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                                        UsPillButton("Accept", busy = busy, onClick = { viewModel.accept(person.userId) })
                                        UsPillButton(
                                            "Decline",
                                            filled = false,
                                            busy = busy,
                                            onClick = { viewModel.decline(person.userId) },
                                        )
                                    }
                                }
                            }
                        }
                    }

                    ConnectionsTab.SENT -> PageBody(
                        page = state.sent,
                        label = "sent requests",
                        emptyTitle = "No requests sent",
                        emptyDetail = "Requests you send wait here until they're answered.",
                        onRetry = viewModel::retry,
                    ) { rows ->
                        LazyColumn(Modifier.fillMaxSize().testTag("connections_sent")) {
                            items(rows, key = { it.userId }) { person ->
                                PersonRow(
                                    userId = person.userId,
                                    name = person.displayName,
                                    username = person.username,
                                    avatarMediaId = person.avatarMediaId,
                                    failed = person.userId in state.failedIds,
                                    onOpen = { onOpenProfile(person.userId) },
                                ) {
                                    UsPillButton(
                                        text = "Cancel",
                                        filled = false,
                                        busy = person.userId in state.busyIds,
                                        onClick = { viewModel.cancel(person.userId) },
                                    )
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    state.confirmRemove?.let { person ->
        val name = person.displayName.ifBlank { person.username.ifBlank { "this person" } }
        AlertDialog(
            onDismissRequest = viewModel::dismissRemove,
            title = { Text("Remove $name from your connections?") },
            text = { Text("They won't be told. Following stays as it is.") },
            confirmButton = { TextButton(onClick = viewModel::confirmRemove) { Text("Remove") } },
            dismissButton = { TextButton(onClick = viewModel::dismissRemove) { Text("Cancel") } },
        )
    }
}

/** The title: the viewer's own reads "Connections", anyone else's their @handle. */
internal fun connectionsTitle(handle: String, isOwn: Boolean): String =
    if (isOwn || handle.isBlank()) "Connections" else "@$handle"

@Composable
private fun <T> PageBody(
    page: ConnectionsPage<T>,
    label: String,
    emptyTitle: String,
    emptyDetail: String?,
    onRetry: () -> Unit,
    content: @Composable (List<T>) -> Unit,
) {
    when {
        page.error != null -> UsErrorState(message = page.error, onRetry = onRetry)
        !page.loaded -> UsLoadingState(label = "Loading $label")
        page.isEmpty -> UsEmptyState(title = emptyTitle, detail = emptyDetail)
        else -> content(page.rows)
    }
}

@Composable
@Suppress("LongParameterList") // one row: who, the line under the name, and its trailing action
private fun PersonRow(
    userId: String,
    name: String,
    username: String,
    avatarMediaId: String?,
    failed: Boolean,
    onOpen: () -> Unit,
    detail: String? = null,
    trailing: @Composable () -> Unit,
) {
    val shown = name.ifBlank { username.ifBlank { "Someone" } }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onOpen)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .testTag("connections_row:$userId"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsAvatar(
            name = shown,
            size = UsAvatarSize.Chat,
            seed = userId,
            imageUrl = rememberProfileMediaUrl(avatarMediaId),
        )
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = shown,
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
                Text(
                    text = it,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (failed) {
                Text(
                    text = "Couldn't do that. Try again.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.liveRed,
                )
            }
        }
        trailing()
    }
}
