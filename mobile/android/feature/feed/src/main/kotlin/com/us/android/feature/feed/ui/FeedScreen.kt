package com.us.android.feature.feed.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.paging.LoadState
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import com.us.android.core.common.error.AppError
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.PostActionState
import com.us.android.core.ui.PostCard
import com.us.android.core.ui.PostCardState
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.feed.data.AppErrorException

@Composable
fun FeedScreen(
    onOpenPost: (postId: String) -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    viewModel: FeedViewModel = hiltViewModel(),
) {
    val items = viewModel.items.collectAsLazyPagingItems()
    val actions by viewModel.pendingActions.collectAsStateWithLifecycle()

    UsScaffold(topBar = { UsTopBar(title = "Home") }, applyPageGutter = false) { padding ->
        FeedList(
            items = items,
            actions = actions,
            onOpenPost = onOpenPost,
            onOpenAuthor = onOpenAuthor,
            onBookmark = viewModel::onLocalBookmark,
            onReact = viewModel::onLocalReaction,
            modifier = Modifier.padding(padding),
        )
    }
}

@Composable
private fun FeedList(
    items: LazyPagingItems<FeedItem>,
    actions: FeedActionState,
    onOpenPost: (String) -> Unit,
    onOpenAuthor: (String) -> Unit,
    onBookmark: (String) -> Unit,
    onReact: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val refresh = items.loadState.refresh
    when {
        refresh is LoadState.Loading && items.itemCount == 0 ->
            UsLoadingState(modifier = modifier, label = "Loading feed")

        refresh is LoadState.Error && items.itemCount == 0 -> UsErrorState(
            message = refresh.error.feedMessage(),
            modifier = modifier,
            onRetry = items::retry,
        )

        refresh is LoadState.NotLoading && items.itemCount == 0 -> UsEmptyState(
            title = "Nothing here yet",
            detail = "Posts from people you follow will show up here.",
            modifier = modifier,
        )

        else -> LazyColumn(
            state = rememberLazyListState(),
            modifier = modifier
                .fillMaxSize()
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
        ) {
            // `key` is what lets Compose keep an item's state across a page
            // append. Without it every append re-keys by index and the whole
            // visible list recomposes, which is the classic feed jank.
            items(
                count = items.itemCount,
                key = { index -> items.peek(index)?.id ?: index },
            ) { index ->
                val item = items[index] ?: return@items
                PostCard(
                    state = item.toCardState(actions),
                    onClick = { onOpenPost(item.id) },
                    onAuthorClick = { onOpenAuthor(item.authorId) },
                    onReact = { onReact(item.id) },
                    onComment = { onOpenPost(item.id) },
                    onRepost = { onOpenPost(item.id) },
                    onBookmark = { onBookmark(item.id) },
                )
            }

            item { AppendState(state = items.loadState.append, onRetry = items::retry) }
        }
    }
}

/** The footer: a spinner while appending, an inline retry when it failed. */
@Composable
private fun AppendState(state: LoadState, onRetry: () -> Unit) {
    when (state) {
        is LoadState.Loading -> Box(
            modifier = Modifier
                .fillMaxWidth()
                .padding(UsTheme.spacing.xxl),
            contentAlignment = Alignment.Center,
        ) {
            CircularProgressIndicator(color = MaterialTheme.colorScheme.primary)
        }

        is LoadState.Error -> Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(UsTheme.spacing.xxl),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            // A failed APPEND must not clear the list. The rows already on
            // screen are still valid; only the next page is missing.
            Text(
                text = state.error.feedMessage(),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
            )
            UsSecondaryButton(text = "Try again", onClick = onRetry, modifier = Modifier.fillMaxWidth())
        }

        is LoadState.NotLoading -> Unit
    }
}

/**
 * Paging carries errors as `Throwable`, so the typed [AppError] the network
 * layer produced is re-read here rather than reduced to a generic message.
 */
private fun Throwable.feedMessage(): String = when ((this as? AppErrorException)?.error) {
    is AppError.NoNetwork -> "You're offline. Check your connection and try again."
    is AppError.Timeout -> "That took too long. Try again."
    is AppError.AuthFailed -> "Please sign in again to see your feed."
    else -> "We couldn't load the feed."
}

/**
 * Layers this session's local taps over the server's reported state.
 *
 * Membership in the set means "the user changed this since the page loaded",
 * which is why it is an XOR against the server value rather than a replacement.
 */
private fun FeedItem.toCardState(actions: FeedActionState) = PostCardState(
    postId = id,
    authorId = authorId,
    // The feed item carries no author name — only `author_id`. Resolving it
    // needs a profile lookup per author, which is an N+1 the feed must not
    // fire from a list row. Recorded as a contract gap; the avatar and label
    // fall back to the id until the feed carries author identity.
    authorName = authorId.take(AUTHOR_ID_PREFIX),
    text = text,
    timestamp = createdAt,
    postType = postType,
    mediaCount = media.size,
    isPinned = isPinned,
    actions = PostActionState(
        likeCount = counts.likes,
        commentCount = counts.comments,
        repostCount = 0, // Feed items carry no repost_count; post detail does.
        hasReacted = id in actions.reacted,
        isBookmarked = isBookmarked xor (id in actions.bookmarked),
        canComment = true,
        canRepost = true,
    ),
)

private const val AUTHOR_ID_PREFIX = 8

@Preview(name = "Feed — empty", showBackground = true, heightDp = 400)
@Composable
private fun FeedEmptyPreview() {
    UsTheme {
        UsEmptyState(
            title = "Nothing here yet",
            detail = "Posts from people you follow will show up here.",
        )
    }
}
