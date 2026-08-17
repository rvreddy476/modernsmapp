package com.us.android.feature.post.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.Post
import com.us.android.core.model.PostCounts
import com.us.android.core.model.PostViewerState
import com.us.android.core.ui.PostActionBar
import com.us.android.core.ui.PostActionState
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

@Composable
fun PostScreen(
    onBack: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    onOpenComments: () -> Unit,
    viewModel: PostViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    PostContent(
        state = state,
        onBack = onBack,
        onRetry = viewModel::load,
        onReact = viewModel::onReactToggle,
        onBookmark = viewModel::onBookmarkToggle,
        onRepost = viewModel::onRepostToggle,
        onDismissActionError = viewModel::dismissActionError,
        onOpenAuthor = onOpenAuthor,
        onOpenComments = onOpenComments,
    )
}

/** Stateless renderer. Immutable state in, callbacks out; fetches nothing. */
@Suppress("LongParameterList")
@Composable
internal fun PostContent(
    state: PostUiState,
    onBack: () -> Unit,
    onRetry: () -> Unit,
    onReact: () -> Unit,
    onBookmark: () -> Unit,
    onRepost: () -> Unit,
    onDismissActionError: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    onOpenComments: () -> Unit,
    modifier: Modifier = Modifier,
) {
    UsScaffold(
        modifier = modifier,
        topBar = { UsTopBar(title = "Post", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (state) {
            is PostUiState.Loading -> UsLoadingState(
                modifier = Modifier.padding(padding),
                label = "Loading post",
            )

            is PostUiState.Error -> UsErrorState(
                message = state.message,
                modifier = Modifier.padding(padding),
                onRetry = if (state.retryable) onRetry else null,
            )

            is PostUiState.Content -> LoadedPost(
                state = state,
                onReact = onReact,
                onBookmark = onBookmark,
                onRepost = onRepost,
                onDismissActionError = onDismissActionError,
                onOpenAuthor = onOpenAuthor,
                onOpenComments = onOpenComments,
                modifier = Modifier.padding(padding),
            )
        }
    }
}

@Composable
private fun LoadedPost(
    state: PostUiState.Content,
    onReact: () -> Unit,
    onBookmark: () -> Unit,
    onRepost: () -> Unit,
    onDismissActionError: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    onOpenComments: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val post = state.post
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
    ) {
        if (post.text.isNotBlank()) {
            Text(
                text = post.text,
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.padding(top = UsTheme.spacing.xxl),
            )
        }

        // Media is not rendered here yet, but the reason changed on
        // 2026-08-17: delivery works end to end now — /v1/media/:id/url
        // returns signed variants and an authorized HLS master path. What is
        // still missing is client-side, not server-side: this payload carries
        // no media reference to resolve (the FEED item does), and there is no
        // player or image loader in the module graph. Both arrive with the
        // media slice.
        if (post.postType != TEXT_POST) {
            UsEmptyState(
                title = "Media isn't shown here yet",
                detail = "Delivery is working; this screen gains the player " +
                    "when the media slice lands.",
                modifier = Modifier.fillMaxWidth(),
            )
        }

        HorizontalDivider(color = UsTheme.extended.borderSubtle)

        PostActionBar(
            state = PostActionState(
                likeCount = post.counts.likes,
                commentCount = post.counts.comments,
                repostCount = post.counts.reposts,
                hasReacted = post.viewer.hasReacted,
                isBookmarked = post.viewer.isBookmarked,
                canReact = post.allowsReactions,
                // Live now that the comments list exists and :app wires the
                // destination. Gated on the AUTHOR's switch, not on whether
                // the client can render a list — a post with comments turned
                // off shows a disabled control rather than a route to an empty
                // screen.
                canComment = post.allowsComments,
                canRepost = post.isRepostable,
                busy = state.busy,
            ),
            onReact = onReact,
            onComment = onOpenComments,
            onRepost = onRepost,
            onBookmark = onBookmark,
        )

        UsSecondaryButton(
            text = "View author",
            onClick = { onOpenAuthor(post.authorId) },
            modifier = Modifier.fillMaxWidth(),
        )

        state.actionError?.let { error ->
            Text(
                text = error,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.fillMaxWidth(),
            )
            UsSecondaryButton(
                text = "Dismiss",
                onClick = onDismissActionError,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

private const val TEXT_POST = "text"

// ── Previews ────────────────────────────────────────────────────────────

private val previewPost = Post(
    id = "b7f4cf83-b2fd-4096-97f9-b50b2c1751da",
    authorId = "e872a073-e1b6-4e5b-94ca-0dd31f12d93f",
    text = "Notes on the Analytical Engine, and why a machine that weaves " +
        "algebraic patterns is not merely a calculator.",
    visibility = "public",
    postType = "text",
    createdAt = "2026-08-15T17:05:33Z",
    counts = PostCounts(likes = 128, comments = 12, reposts = 3, views = 940),
    viewer = PostViewerState(isBookmarked = false),
    allowsComments = true,
    allowsReactions = true,
    isRepostable = true,
    isPinned = false,
)

@Composable
private fun PreviewHost(state: PostUiState) = UsTheme {
    PostContent(
        state = state,
        onBack = {},
        onRetry = {},
        onReact = {},
        onBookmark = {},
        onRepost = {},
        onDismissActionError = {},
        onOpenAuthor = {},
        onOpenComments = {},
    )
}

@Preview(name = "Post", showBackground = true, heightDp = 520)
@Composable
private fun PostPreview() = PreviewHost(PostUiState.Content(previewPost))

@Preview(name = "Post — engaged", showBackground = true, heightDp = 520)
@Composable
private fun PostEngagedPreview() = PreviewHost(
    PostUiState.Content(
        previewPost.copy(viewer = PostViewerState(isBookmarked = true, hasReacted = true)),
        hasReposted = true,
    ),
)

@Preview(name = "Post — reactions disabled by author", showBackground = true, heightDp = 520)
@Composable
private fun PostRestrictedPreview() =
    PreviewHost(PostUiState.Content(previewPost.copy(allowsReactions = false)))

@Preview(name = "Post — media attachment", showBackground = true, heightDp = 520)
@Composable
private fun PostWithMediaPreview() =
    PreviewHost(PostUiState.Content(previewPost.copy(postType = "video")))

@Preview(name = "Post — action failed", showBackground = true, heightDp = 520)
@Composable
private fun PostActionErrorPreview() = PreviewHost(
    PostUiState.Content(previewPost, actionError = "That didn't go through. Try again."),
)

@Preview(name = "Post — busy", showBackground = true, heightDp = 520)
@Composable
private fun PostBusyPreview() = PreviewHost(PostUiState.Content(previewPost, busy = true))

@Preview(name = "Post — loading", showBackground = true, heightDp = 320)
@Composable
private fun PostLoadingPreview() = PreviewHost(PostUiState.Loading)

@Preview(name = "Post — deleted", showBackground = true, heightDp = 320)
@Composable
private fun PostDeletedPreview() = PreviewHost(
    PostUiState.Error("This post isn't available. It may have been deleted.", retryable = false),
)
