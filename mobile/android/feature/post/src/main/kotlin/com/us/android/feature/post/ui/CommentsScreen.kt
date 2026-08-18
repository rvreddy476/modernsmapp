package com.us.android.feature.post.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.tooling.preview.Preview
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
import com.us.android.feature.post.data.Comment

@Composable
fun CommentsScreen(
    onBack: () -> Unit,
    viewModel: CommentsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    CommentsContent(state = state, onBack = onBack, onRetry = viewModel::load)
}

/**
 * Stateless renderer. Immutable state in, callbacks out; fetches nothing.
 *
 * The `PaddingValues` the scaffold hands out is ignored on purpose. UsScaffold
 * has ALREADY applied those insets to the Box it wraps this content in, so
 * re-applying them here would inset the screen twice — which on a list is not
 * a cosmetic nudge but a visible gap the width of the top bar above the first
 * comment.
 *
 * The page gutter is off for the same reason a feed turns it off: rows own
 * their own horizontal padding so a row's divider and its touch target can run
 * the full width while its text stays inside the gutter.
 */
@Composable
internal fun CommentsContent(
    state: CommentsUiState,
    onBack: () -> Unit,
    onRetry: () -> Unit,
    modifier: Modifier = Modifier,
) {
    UsScaffold(
        modifier = modifier,
        topBar = { UsTopBar(title = "Comments", onBack = onBack) },
        applyPageGutter = false,
    ) { _ ->
        when (state) {
            is CommentsUiState.Loading -> UsLoadingState(label = "Loading comments")

            is CommentsUiState.Error -> UsErrorState(
                message = state.message,
                onRetry = if (state.retryable) onRetry else null,
            )

            // A successful load that returned nothing is not an error, and the
            // shared empty state exists so it never gets styled as one.
            is CommentsUiState.Content -> if (state.comments.isEmpty()) {
                UsEmptyState(
                    title = "No comments yet",
                    detail = "Nobody has commented on this post.",
                )
            } else {
                CommentList(comments = state.comments)
            }
        }
    }
}

/**
 * Lazy because a post's comment list has no bound the client controls: the
 * page size is the only limit, and it is a client guess against an unobserved
 * server cap. A plain scrolling Column would compose every row up front.
 *
 * Keyed by comment id so a reload that reorders or drops rows reuses the right
 * nodes instead of recycling one comment's state onto another's text.
 */
@Composable
private fun CommentList(
    comments: List<Comment>,
    modifier: Modifier = Modifier,
) {
    LazyColumn(modifier = modifier.fillMaxSize()) {
        items(items = comments, key = { it.id }) { comment ->
            CommentRow(comment = comment)
            HorizontalDivider(color = UsTheme.extended.borderSubtle)
        }
    }
}

/**
 * One comment.
 *
 * The avatar is seeded from the author id rather than a name because the
 * payload has no name to seed it with — `UsAvatar` renders "?" for a blank
 * name, which is the honest result, while the seed still gives each commenter
 * a stable colour so two rows by the same person read as the same person.
 */
@Composable
private fun CommentRow(
    comment: Comment,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.xxl,
            ),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        UsAvatar(
            name = "",
            size = UsAvatarSize.Small,
            seed = comment.authorId,
            contentDescription = null,
        )
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            CommentByline(comment)
            Text(
                text = comment.body,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
            )
            CommentMetadata(comment)
        }
    }
}

/**
 * Who and when.
 *
 * Shows a truncated author id, not a name. There is no name on the wire, and a
 * placeholder like "Anonymous" would assert something the server never said —
 * an id stub is at least true, and it is visibly an id, so nobody mistakes it
 * for a handle. It goes the moment the payload carries one.
 *
 * The timestamp is the date portion of the ISO-8601 string, not a relative
 * "2h ago". A relative formatter is a shared formatting rule that belongs in
 * the design system or in the eventual shared-logic module (strategy §2.4);
 * inventing one inside a feature is how two screens end up disagreeing about
 * what "yesterday" means.
 */
@Composable
private fun CommentByline(comment: Comment) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Text(
            text = comment.authorId.take(AUTHOR_ID_STUB_LENGTH),
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier.semantics {
                contentDescription = "Author ${comment.authorId.take(AUTHOR_ID_STUB_LENGTH)}"
            },
        )
        Text(
            text = formatRelativeTime(comment.createdAt),
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textMuted,
        )
    }
}

/**
 * Counts and the reply marker — all inert.
 *
 * No control is offered for any of them: no endpoint to like a comment was
 * captured, and no route returns a reply thread, so a tappable heart or a
 * "3 replies" link would lead nowhere. The reply marker is rendered flat
 * rather than as an indent because the payload carries `is_reply` but no
 * parent id, so nesting a row under its neighbour would assert a relationship
 * the server never stated.
 */
@Composable
private fun CommentMetadata(comment: Comment) {
    Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        if (comment.isReply) {
            Text(
                text = "Reply",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textMuted,
            )
        }
        Text(
            text = "${comment.likeCount} likes",
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textMuted,
        )
        if (comment.replyCount > 0) {
            Text(
                text = "${comment.replyCount} replies",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.textMuted,
            )
        }
    }
}

/** Enough of a UUID to distinguish commenters, short enough to read as a stub. */
private const val AUTHOR_ID_STUB_LENGTH = 8

// ── Previews ────────────────────────────────────────────────────────────

/** The captured comment, field for field, so the preview shows real data. */
private val previewComment = Comment(
    id = "46ab1085-55ca-4fc2-b649-f05527a68b97",
    authorId = "719e2958-f412-44ca-b94a-b00060a7fccb",
    body = "This comment proves the native create and list contract.",
    likeCount = 0,
    replyCount = 0,
    isReply = false,
    createdAt = "2026-08-16T19:44:51.534734Z",
)

private val previewComments = listOf(
    previewComment,
    previewComment.copy(
        id = "b1",
        authorId = "2d373f48-6d0f-4a62-b439-51dee0b0ec2e",
        body = "A longer reply, kept here so the preview shows how a comment " +
            "that wraps across several lines sits against the avatar column.",
        likeCount = 12,
        replyCount = 3,
        isReply = true,
    ),
    previewComment.copy(
        id = "c2",
        authorId = "e872a073-e1b6-4e5b-94ca-0dd31f12d93f",
        body = "Short one.",
        likeCount = 4,
    ),
)

@Composable
private fun PreviewHost(state: CommentsUiState) = UsTheme {
    CommentsContent(state = state, onBack = {}, onRetry = {})
}

@Preview(name = "Comments — loading", showBackground = true, heightDp = 320)
@Composable
private fun CommentsLoadingPreview() = PreviewHost(CommentsUiState.Loading)

@Preview(name = "Comments — load failed", showBackground = true, heightDp = 320)
@Composable
private fun CommentsErrorPreview() = PreviewHost(
    CommentsUiState.Error("We couldn't load the comments.", retryable = true),
)

@Preview(name = "Comments — post deleted", showBackground = true, heightDp = 320)
@Composable
private fun CommentsNotFoundPreview() = PreviewHost(
    CommentsUiState.Error(
        "This post isn't available. It may have been deleted.",
        retryable = false,
    ),
)

@Preview(name = "Comments — none yet", showBackground = true, heightDp = 320)
@Composable
private fun CommentsEmptyPreview() = PreviewHost(CommentsUiState.Content(emptyList()))

@Preview(name = "Comments — one", showBackground = true, heightDp = 320)
@Composable
private fun CommentsSinglePreview() =
    PreviewHost(CommentsUiState.Content(listOf(previewComment)))

@Preview(name = "Comments — many", showBackground = true, heightDp = 520)
@Composable
private fun CommentsManyPreview() = PreviewHost(CommentsUiState.Content(previewComments))
