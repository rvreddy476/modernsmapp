package com.us.android.feature.tube.ui.credits

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.pressScale
import com.us.android.feature.tube.ui.shimmer

/**
 * "Credit requests" (video credits, 2026-10-23): the long videos someone
 * credited the viewer in, waiting for an answer — opened by a
 * `credit_request` notification and from the Me tab's drawer. Each row is the
 * video's cover and title, who credited them and as what, and Accept /
 * Decline. An accepted row stays, marked, with "Remove me from this video".
 * A row's cover or title opens the video.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CreditRequestsScreen(
    destinations: TubeDestinations,
    viewModel: CreditRequestsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    TubePage(selected = null, destinations = destinations, onBack = destinations.onBack) { padding ->
        PullToRefreshBox(
            isRefreshing = state.refreshing,
            onRefresh = viewModel::refresh,
            modifier = Modifier
                .fillMaxSize()
                .testTag("credit_requests_page"),
        ) {
            when {
                state.loading -> RequestsSkeleton()
                state.loadError != null -> UsErrorState(
                    message = state.loadError.orEmpty(),
                    onRetry = viewModel::retry,
                    modifier = Modifier.testTag("credit_requests_error"),
                )
                state.empty -> UsEmptyState(
                    title = "No credit requests",
                    detail = "When someone credits you in a video, you'll decide here whether it shows on your profile.",
                    modifier = Modifier.testTag("credit_requests_empty"),
                )
                else -> RequestsList(
                    state = state,
                    viewModel = viewModel,
                    onOpenVideo = destinations.onOpenVideo,
                    bottomPadding = padding,
                )
            }
        }
    }
}

@Composable
private fun RequestsList(
    state: CreditRequestsUiState,
    viewModel: CreditRequestsViewModel,
    onOpenVideo: (String) -> Unit,
    bottomPadding: PaddingValues,
) {
    val list = rememberLazyListState()
    LoadMoreNearEnd(list = list, canLoadMore = state.canLoadMore, onLoadMore = viewModel::loadMore)
    LazyColumn(
        state = list,
        modifier = Modifier
            .fillMaxSize()
            .testTag("credit_requests_list"),
        contentPadding = PaddingValues(
            start = UsTheme.spacing.pageHorizontal,
            end = UsTheme.spacing.pageHorizontal,
            top = UsTheme.spacing.l,
            bottom = bottomPadding.calculateBottomPadding() + UsTheme.spacing.xxl,
        ),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        item(key = "title") {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                Text(
                    text = "Credit requests",
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = "Accept to show the video under \"Appears in\" on your profile.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
                state.message?.let { message ->
                    Text(
                        text = message,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.testTag("credit_requests_message"),
                    )
                }
            }
        }
        items(state.rows, key = { it.request.id }) { row ->
            RequestRow(
                row = row,
                busy = row.request.id in state.busy,
                onOpenVideo = { onOpenVideo(row.request.postId) },
                onAccept = { viewModel.accept(row.request.id) },
                onDecline = { viewModel.decline(row.request.id) },
                onRemoveMe = { viewModel.removeMe(row.request.id) },
            )
        }
        if (state.loadingMore) {
            item(key = "more") { SkeletonRow() }
        }
    }
}

/** Asks for the next page when the last row is within a few of the screen. */
@Composable
private fun LoadMoreNearEnd(list: LazyListState, canLoadMore: Boolean, onLoadMore: () -> Unit) {
    val nearEnd by remember(list) {
        derivedStateOf {
            val info = list.layoutInfo
            val last = info.visibleItemsInfo.lastOrNull()?.index ?: 0
            info.totalItemsCount > 0 && last >= info.totalItemsCount - LOAD_AHEAD
        }
    }
    LaunchedEffect(nearEnd, canLoadMore) { if (nearEnd && canLoadMore) onLoadMore() }
}

@Composable
@Suppress("LongParameterList") // One callback per control on the row.
private fun RequestRow(
    row: CreditRequestRow,
    busy: Boolean,
    onOpenVideo: () -> Unit,
    onAccept: () -> Unit,
    onDecline: () -> Unit,
    onRemoveMe: () -> Unit,
) {
    val request = row.request
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.glassBg)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
            .padding(UsTheme.spacing.l)
            .testTag("credit_request:${request.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .pressScale(onOpenVideo)
                .semantics { role = Role.Button },
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Cover(url = request.coverUrl)
            Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                Text(
                    text = request.postTitle.ifBlank { "Untitled video" },
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.SemiBold,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                if (request.channelName.isNotBlank()) {
                    Text(
                        text = request.channelName,
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textMuted,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        }
        Row(
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            UsAvatar(
                name = request.from.displayName,
                seed = request.from.id,
                size = UsAvatarSize.Small,
                imageUrl = request.from.avatarUrl,
            )
            Text(
                text = creditRequestLine(request.from.displayName, request.role, request.name),
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
                modifier = Modifier.weight(1f),
            )
        }
        if (row.accepted) {
            Row(
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(
                    imageVector = UsIcons.Check,
                    contentDescription = null,
                    tint = UsTheme.extended.statusSuccess,
                    modifier = Modifier.size(CHECK_GLYPH),
                )
                Text(
                    text = "Accepted",
                    style = MaterialTheme.typography.labelLarge,
                    color = UsTheme.extended.statusSuccess,
                    modifier = Modifier.weight(1f),
                )
                UsPillButton(
                    text = "Remove me from this video",
                    onClick = onRemoveMe,
                    filled = false,
                    busy = busy,
                    modifier = Modifier.testTag("credit_request_remove:${request.id}"),
                )
            }
        } else {
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                UsPillButton(
                    text = "Accept",
                    onClick = onAccept,
                    busy = busy,
                    modifier = Modifier
                        .weight(1f)
                        .testTag("credit_request_accept:${request.id}"),
                )
                UsPillButton(
                    text = "Decline",
                    onClick = onDecline,
                    filled = false,
                    enabled = !busy,
                    modifier = Modifier
                        .weight(1f)
                        .testTag("credit_request_decline:${request.id}"),
                )
            }
        }
    }
}

/** "Asha Rao credited you as Director · \"Asha R.\"" — who, as what, and the name they typed. */
internal fun creditRequestLine(from: String, role: String, name: String): String {
    val who = from.ifBlank { "Someone" }
    val asRole = role.trim().takeIf { it.isNotEmpty() }?.let { " as $it" }.orEmpty()
    val asName = name.trim().takeIf { it.isNotEmpty() }?.let { " · \"$it\"" }.orEmpty()
    return "$who credited you$asRole$asName"
}

@Composable
private fun Cover(url: String?) {
    val shape = RoundedCornerShape(UsTheme.radii.small)
    Box(
        modifier = Modifier
            .width(COVER_WIDTH)
            .aspectRatio(LANDSCAPE)
            .clip(shape)
            .background(UsTheme.extended.bgCard),
        contentAlignment = Alignment.Center,
    ) {
        if (url != null) {
            AsyncImage(
                model = url,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        } else {
            Icon(
                imageVector = UsIcons.Film,
                contentDescription = null,
                tint = UsTheme.extended.textDim,
                modifier = Modifier.size(CHECK_GLYPH),
            )
        }
    }
}

/** The first load: rows shaped like the real ones, with the shared shimmer (still under reduced motion). */
@Composable
private fun RequestsSkeleton() {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
            .testTag("credit_requests_loading"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        repeat(SKELETON_ROWS) { SkeletonRow() }
    }
}

@Composable
private fun SkeletonRow() {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Box(
            modifier = Modifier
                .width(COVER_WIDTH)
                .aspectRatio(LANDSCAPE)
                .shimmer(RoundedCornerShape(UsTheme.radii.small)),
        )
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(LINE_HEIGHT)
                    .shimmer(RoundedCornerShape(UsTheme.radii.small)),
            )
            Box(
                modifier = Modifier
                    .fillMaxWidth(SHORT_LINE)
                    .height(LINE_HEIGHT)
                    .shimmer(RoundedCornerShape(UsTheme.radii.small)),
            )
        }
    }
}

private const val LOAD_AHEAD = 3
private const val SKELETON_ROWS = 4
private const val LANDSCAPE = 16f / 9f
private const val SHORT_LINE = 0.6f
private val HAIRLINE = 1.dp
private val COVER_WIDTH = 120.dp
private val LINE_HEIGHT = 14.dp
private val CHECK_GLYPH = 18.dp
