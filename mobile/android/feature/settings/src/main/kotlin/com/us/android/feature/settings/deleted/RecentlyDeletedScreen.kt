package com.us.android.feature.settings.deleted

import androidx.compose.foundation.background
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
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.DeletedPost
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * Settings › Recently deleted. Compact rows — a 56dp still or a glyph, the
 * first line of text, "Deleted 2h ago · Permanently removed in 28 days" —
 * each with its own Restore. The empty state is a fact, not a failure:
 * "Nothing here / Posts you delete stay here for 30 days."
 */
@Composable
fun RecentlyDeletedScreen(
    onBack: () -> Unit,
    viewModel: RecentlyDeletedViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar("Recently deleted", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val current = state) {
            RecentlyDeletedUiState.Loading -> UsLoadingState(Modifier.padding(padding), "Loading recently deleted")
            is RecentlyDeletedUiState.Error ->
                UsErrorState(current.message, Modifier.padding(padding), onRetry = viewModel::load)
            is RecentlyDeletedUiState.Content -> Box(
                modifier = Modifier
                    .padding(padding)
                    .fillMaxSize(),
            ) {
                if (current.isEmpty) {
                    UsEmptyState(
                        title = "Nothing here",
                        detail = "Posts you delete stay here for 30 days.",
                        modifier = Modifier.testTag("recently_deleted_empty"),
                    )
                } else {
                    DeletedList(current, onRestore = viewModel::restore, onEndReached = viewModel::loadMore)
                }
                val message = remember(current.message) {
                    current.message?.let { UsMessage(text = it, type = UsMessageType.Error) }
                }
                UsMessageHost(message = message, onDismiss = viewModel::dismissMessage)
            }
        }
    }
}

@Composable
private fun DeletedList(
    content: RecentlyDeletedUiState.Content,
    onRestore: (String) -> Unit,
    onEndReached: () -> Unit,
) {
    val listState = rememberLazyListState()
    // The next page is asked for when the last row is on screen — once per
    // arrival, since the ViewModel ignores a request while one is in flight.
    if (content.hasMore) {
        LaunchedEffect(listState, content.posts.size) {
            snapshotFlow { listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index }
                .collect { last -> if (last != null && last >= content.posts.lastIndex) onEndReached() }
        }
    }
    LazyColumn(
        state = listState,
        modifier = Modifier
            .fillMaxSize()
            .testTag("recently_deleted_list"),
        contentPadding = PaddingValues(
            horizontal = UsTheme.spacing.pageHorizontal,
            vertical = UsTheme.spacing.m,
        ),
    ) {
        items(content.posts, key = { it.id }) { post ->
            DeletedRow(
                post = post,
                restoring = post.id in content.restoring,
                onRestore = { onRestore(post.id) },
            )
            HorizontalDivider(color = UsTheme.extended.borderSubtle)
        }
    }
}

@Composable
private fun DeletedRow(post: DeletedPost, restoring: Boolean, onRestore: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.l)
            .testTag("recently_deleted_row:${post.id}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Thumbnail(post)
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            Text(
                text = post.title(),
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = deletedRowSubtitle(post.deletedAt, post.purgeAt),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
        UsPillButton(
            text = "Restore",
            onClick = onRestore,
            busy = restoring,
            modifier = Modifier.testTag("recently_deleted_restore:${post.id}"),
        )
    }
}

/** The still when the server signed one; otherwise a glyph for the kind of post. */
@Composable
private fun Thumbnail(post: DeletedPost) {
    val shape = RoundedCornerShape(UsTheme.radii.small)
    Box(
        modifier = Modifier
            .size(THUMBNAIL)
            .clip(shape)
            .background(UsTheme.extended.bgRaised),
        contentAlignment = Alignment.Center,
    ) {
        if (post.thumbnailUrl != null) {
            AsyncImage(
                model = post.thumbnailUrl,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        } else {
            Icon(
                imageVector = post.glyph(),
                contentDescription = null,
                tint = UsTheme.extended.textMuted,
                modifier = Modifier.size(THUMBNAIL_GLYPH),
            )
        }
    }
}

/** The first line of text, or the kind of post when there are no words. */
internal fun DeletedPost.title(): String {
    val firstLine = text.lineSequence().map { it.trim() }.firstOrNull { it.isNotEmpty() }
    return firstLine ?: when (postType.lowercase()) {
        "reel", "video" -> "Video"
        "image", "photo", "carousel" -> "Photo"
        "poll" -> "Poll"
        else -> "Post"
    }
}

private fun DeletedPost.glyph() = when (postType.lowercase()) {
    "reel", "video" -> UsIcons.Film
    "image", "photo", "carousel" -> UsIcons.Image
    "poll" -> UsIcons.Poll
    else -> UsIcons.FileText
}

private val THUMBNAIL = 56.dp
private val THUMBNAIL_GLYPH = 22.dp
