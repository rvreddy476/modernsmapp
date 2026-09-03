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
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * Incoming follow requests — the private-account owner's approval queue.
 *
 * Reachable from the own-profile "Requests" pill and from a
 * [com.us.android.core.model.NotificationKind.FollowRequest] row, though the
 * notification row itself decides and Accepts/Declines inline rather than
 * routing here; this screen is for working through the whole backlog.
 */
@Composable
fun FollowRequestsScreen(
    onBack: () -> Unit,
    onOpenProfile: (userId: String) -> Unit,
    viewModel: FollowRequestsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val listState = rememberLazyListState()

    val shouldLoadMore by remember {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: return@derivedStateOf false
            state.hasMore && !state.loadingMore && last >= listState.layoutInfo.totalItemsCount - LOAD_MORE_THRESHOLD
        }
    }
    LaunchedEffect(shouldLoadMore) {
        if (shouldLoadMore) viewModel.loadMore()
    }

    UsScaffold(
        topBar = { UsTopBar(title = "Follow requests", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            when {
                state.loading -> UsLoadingState(label = "Loading follow requests")

                state.error != null && state.rows.isEmpty() -> UsErrorState(
                    message = state.error.orEmpty(),
                    onRetry = viewModel::refresh,
                )

                state.isEmpty -> UsEmptyState(
                    title = "No follow requests",
                    detail = "People who ask to follow you will show up here.",
                )

                else -> FollowRequestsList(
                    state = state,
                    listState = listState,
                    onOpenProfile = onOpenProfile,
                    onAccept = viewModel::accept,
                    onDecline = viewModel::decline,
                )
            }
        }
    }
}

@Composable
private fun FollowRequestsList(
    state: FollowRequestsUiState,
    listState: LazyListState,
    onOpenProfile: (String) -> Unit,
    onAccept: (String) -> Unit,
    onDecline: (String) -> Unit,
) {
    LazyColumn(state = listState, modifier = Modifier.fillMaxSize()) {
        items(state.rows, key = { it.requesterId }) { row ->
            FollowRequestRowItem(
                row = row,
                onOpenProfile = { onOpenProfile(row.requesterId) },
                onAccept = { onAccept(row.requesterId) },
                onDecline = { onDecline(row.requesterId) },
            )
        }
        if (state.loadingMore) {
            item(key = "load-more") {
                Box(
                    modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.l),
                    contentAlignment = Alignment.Center,
                ) {
                    CircularProgressIndicator()
                }
            }
        }
    }
}

@Composable
private fun FollowRequestRowItem(
    row: FollowRequestRow,
    onOpenProfile: () -> Unit,
    onAccept: () -> Unit,
    onDecline: () -> Unit,
) {
    val name = row.displayName.ifBlank { "Someone" }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onOpenProfile)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsAvatar(name = name, size = UsAvatarSize.Chat, seed = row.requesterId)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = name,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
            )
            if (row.actionFailed) {
                Text(
                    text = "Couldn't do that. Try again.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.liveRed,
                )
            }
            Row(
                modifier = Modifier.padding(top = UsTheme.spacing.s),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                UsButton(
                    text = "Accept",
                    onClick = onAccept,
                    loading = row.busy,
                    modifier = Modifier.weight(1f),
                )
                UsSecondaryButton(
                    text = "Decline",
                    onClick = onDecline,
                    enabled = !row.busy,
                    modifier = Modifier.weight(1f),
                )
            }
        }
    }
}

/** How many rows from the end to start fetching the next page. */
private const val LOAD_MORE_THRESHOLD = 3
