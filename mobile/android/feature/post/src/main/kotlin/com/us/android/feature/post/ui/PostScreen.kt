package com.us.android.feature.post.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementAction
import com.us.android.core.engagement.data.EngagementFailure
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.engagement.data.likeCountOr
import com.us.android.core.engagement.data.reactedOr
import com.us.android.core.engagement.data.repostCountOr
import com.us.android.core.engagement.data.repostedOr
import com.us.android.core.media.data.MediaDelivery
import com.us.android.core.model.Post
import com.us.android.core.model.PostCounts
import com.us.android.core.model.PostViewerState
import com.us.android.core.model.Profile
import com.us.android.core.ui.DEFAULT_MEDIA_ASPECT
import com.us.android.core.ui.EngagementFailureBar
import com.us.android.core.ui.PostActionBar
import com.us.android.core.ui.PostActionState
import com.us.android.core.ui.PostMedia
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.VIDEO_POST
import com.us.android.core.ui.rememberPostSharer

@Composable
fun PostScreen(
    onBack: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    onOpenComments: () -> Unit,
    viewModel: PostViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val failures by viewModel.failures.collectAsStateWithLifecycle()
    PostContent(
        state = state,
        failures = failures,
        onRetryFailure = viewModel::retryFailure,
        onDismissFailure = viewModel::dismissFailure,
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
    failures: List<EngagementFailure> = emptyList(),
    onRetryFailure: (String, EngagementAction) -> Unit = { _, _ -> },
    onDismissFailure: (String, EngagementAction) -> Unit = { _, _ -> },
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
        // No author action here. The header inside the content is the route to
        // the profile, and a second control for the same destination makes a
        // reader wonder what the difference is.
        topBar = { UsTopBar(title = "Post", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        // The scaffold inset is applied ONCE here, on the column, rather than
        // on each branch below. Applying it in both places would inset twice.
        Column(modifier = Modifier.padding(padding)) {
            // Above the post, not inside its card: a failed like is about this
            // session, not about what the author wrote.
            EngagementFailureBar(
                failures = failures,
                onRetry = onRetryFailure,
                onDismiss = onDismissFailure,
            )
            when (state) {
                is PostUiState.Loading -> UsLoadingState(label = "Loading post")

                is PostUiState.Error -> UsErrorState(
                    message = state.message,
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
                )
            }
        }
    }
}

// One Compose layout; see PostCard for why it is not split to fit a budget.
// MagicNumber: inline ARGB surface colours from the current visual design.
@Suppress("LongMethod", "MagicNumber")
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
    val author = state.author
    val share = rememberPostSharer()
    val overlay = state.overlay
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.l,
            ),
    ) {
        // The same card as the feed, so opening a post lands on the object the
        // reader just tapped rather than a differently-shaped page. Detail adds
        // the full text and the real media; it does not restyle the post.
        val cardShape = RoundedCornerShape(18.dp)
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .clip(cardShape)
                .background(
                    brush = Brush.verticalGradient(
                        listOf(
                            Color(0xFF18181D),
                            Color(0xFF121215),
                        ),
                    ),
                )
                .border(
                    width = HAIRLINE,
                    color = Color(0x1FFFFFFF),
                    shape = cardShape,
                )
                .padding(UsTheme.spacing.xxl),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            if (author != null) {
                AuthorHeader(
                    author = author,
                    createdAt = post.createdAt,
                    onOpenAuthor = onOpenAuthor,
                )
            }

            if (post.text.isNotBlank()) {
                // No line cap here, unlike the feed card. This screen exists to
                // show the whole post; truncating it would leave no way to read
                // the rest.
                Text(
                    text = post.text,
                    style = MaterialTheme.typography.bodyLarge,
                    color = UsTheme.extended.textPrimary,
                )
            }

            PostAttachment(post = post, media = state.media)

            PostActionBar(
                state = PostActionState(
                    // Same derivation as the feed, from the same shared
                    // overlay — so a like made in one surface is already
                    // applied when the other opens.
                    likeCount = overlay.likeCountOr(post.counts.likes, post.viewer.hasReacted),
                    commentCount = post.counts.comments,
                    repostCount = overlay.repostCountOr(
                        post.counts.reposts,
                        post.viewer.hasReposted,
                    ),
                    hasReacted = overlay.reactedOr(post.viewer.hasReacted),
                    hasReposted = overlay.repostedOr(post.viewer.hasReposted),
                    isBookmarked = overlay.bookmarkedOr(post.viewer.isBookmarked),
                    canReact = post.allowsReactions,
                    // Live now that the comments list exists and :app wires the
                    // destination. Gated on the AUTHOR's switch, not on whether
                    // the client can render a list — a post with comments turned
                    // off shows a disabled control rather than a route to an
                    // empty screen.
                    canComment = post.allowsComments,
                    canRepost = post.isRepostable,
                    busy = state.busy,
                ),
                onReact = onReact,
                onComment = onOpenComments,
                onRepost = onRepost,
                onBookmark = onBookmark,
                onShare = { share(post.text, author?.nameForDisplay) },
            )
        }

        // Outside the card. An action failure is about this session, not about
        // the post, and putting it inside the surface makes it look like part
        // of what the author wrote.
        state.actionError?.let { error ->
            Text(
                text = error,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = UsTheme.spacing.l),
            )
            UsSecondaryButton(
                text = "Dismiss",
                onClick = onDismissActionError,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

/**
 * The post's attachment, or an honest account of why there isn't one.
 *
 * Three distinct states, because collapsing them is what made the old screen
 * unhelpful:
 *  - resolved and ready → the real image, via the same component the feed uses
 *  - resolved but still processing → say so; it will appear later
 *  - a media reference that would not resolve → render nothing rather than an
 *    apology, since the text is the post and an error block would dominate it
 */
@Composable
private fun PostAttachment(post: Post, media: MediaDelivery?) {
    val reference = post.media.firstOrNull() ?: return

    when {
        media == null -> Unit

        media.isReady -> PostMedia(
            url = media.posterUrl,
            postType = if (reference.kind == VIDEO_POST) VIDEO_POST else reference.kind,
            count = post.media.size,
            aspectRatio = media.aspectRatio ?: DEFAULT_MEDIA_ASPECT,
            // The description the author was required to write, finally read.
            contentDescription = reference.contentDescription,
        )

        else -> UsEmptyState(
            title = "Still processing",
            detail = "This will appear once the upload finishes.",
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

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

/**
 * Avatar, name and age, above the post body.
 *
 * Rendered only once the author lookup lands. The post payload carries just
 * `author_id`, so the name comes from a second call to the public profile
 * endpoint; showing a placeholder that later changes into a real name is a
 * worse first impression than the header arriving whole a moment later.
 *
 * The whole row is the target, not just the name. A 48dp-tall strip is far
 * easier to hit than a line of text, and every social surface has trained
 * people that the avatar is tappable too.
 */
// MagicNumber: inline ARGB border colour from the current visual design.
@Suppress("MagicNumber")
@Composable
private fun AuthorHeader(
    author: Profile,
    createdAt: String,
    onOpenAuthor: (userId: String) -> Unit,
) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier
            .padding(top = UsTheme.spacing.xxl)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .clickable { onOpenAuthor(author.userId) },
    ) {
        Box(
            modifier = Modifier
                .clip(CircleShape)
                .border(
                    width = HAIRLINE,
                    color = Color(0x26FFFFFF),
                    shape = CircleShape,
                ),
        ) {
            UsAvatar(
                name = author.nameForDisplay,
                size = UsAvatarSize.Small,
                seed = author.userId,
            )
        }
        Column {
            Text(
                text = author.nameForDisplay,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            val age = formatRelativeTime(createdAt)
            if (age.isNotBlank()) {
                Text(
                    text = age,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
            }
        }
    }
}

/**
 * Matches the feed card's border weight. Defined here rather than shared
 * because it is a rendering detail of a card edge, not a spacing token — and
 * the two would look wrong the moment only one of them changed.
 */
private val HAIRLINE = 1.dp
