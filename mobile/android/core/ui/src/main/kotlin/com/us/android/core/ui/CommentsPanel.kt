package com.us.android.core.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import com.us.android.core.common.error.AppError
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.CommentRow
import com.us.android.core.engagement.data.CommentsUiState

/**
 * The comments list and composer.
 *
 * Stateless: it receives [CommentsUiState] and callbacks and fetches nothing,
 * which is what lets the same composable back both the bottom sheet over the
 * feed and a full screen opened from post detail. The loading, paging and
 * idempotency rules live in the controller behind those callbacks.
 *
 * It depends on :core:engagement for the STATE TYPES only — plain data classes
 * with no Android or network types in them. The module's rule is that a
 * component here must not be able to fetch, and this one cannot.
 */
@Composable
fun CommentsPanel(
    state: CommentsUiState,
    onDraftChange: (String) -> Unit,
    onSubmit: () -> Unit,
    onLoadMore: () -> Unit,
    onRetryAppend: () -> Unit,
    onRetryRefresh: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val listState = rememberLazyListState()

    // Paging is driven by what is actually visible rather than by a "load
    // more" button, but the trigger is the LAST INDEX rather than a scroll
    // offset so it fires once per page instead of on every pixel of scroll.
    val shouldLoadMore by remember(state) {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: return@derivedStateOf false
            state.canLoadMore && last >= state.rows.lastIndex - PREFETCH_DISTANCE
        }
    }
    LaunchedEffect(shouldLoadMore) {
        if (shouldLoadMore) onLoadMore()
    }

    Column(modifier = modifier.fillMaxWidth()) {
        Text(
            text = commentsHeading(state.rows.size),
            style = MaterialTheme.typography.titleSmall,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.l,
            ),
        )

        // The whole middle region is weighted so the composer below it ALWAYS
        // gets its space. The state views fill their box; unweighted they
        // filled the column and pushed the composer off-screen — which made
        // every zero-comment post impossible to comment on.
        Box(modifier = Modifier.weight(1f, fill = false)) {
            when {
                state.loading && state.rows.isEmpty() ->
                    UsLoadingState(label = "Loading comments")

                // Only when there is nothing to show. A refresh failure over an
                // already-loaded list keeps the list and surfaces the error under
                // it, so a network blip never costs the reader the conversation.
                state.refreshError != null && state.rows.isEmpty() -> UsErrorState(
                    message = "We couldn't load comments.",
                    onRetry = onRetryRefresh,
                )

                state.rows.isEmpty() -> UsEmptyState(
                    title = "No comments yet",
                    detail = "Be the first to say something.",
                )

                else -> LazyColumn(
                    state = listState,
                    contentPadding = androidx.compose.foundation.layout.PaddingValues(
                        horizontal = UsTheme.spacing.pageHorizontal,
                    ),
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                ) {
                    items(state.rows, key = { it.id }) { row -> CommentItem(row) }

                    if (state.appending || state.appendError != null) {
                        item(key = "append") {
                            AppendFooter(
                                loading = state.appending,
                                failed = state.appendError != null,
                                onRetry = onRetryAppend,
                            )
                        }
                    }
                }
            }
        }

        Composer(
            state = state,
            onDraftChange = onDraftChange,
            onSubmit = onSubmit,
        )
    }
}

@Composable
private fun CommentItem(row: CommentRow) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = Modifier.fillMaxWidth(),
    ) {
        // Named when the server's batch hydration supplied an author; the
        // seed keeps a stable color identity either way.
        UsAvatar(name = row.authorName, size = UsAvatarSize.Small, seed = row.authorId)
        Column(modifier = Modifier.weight(1f)) {
            if (row.authorName.isNotBlank()) {
                Text(
                    text = row.authorName,
                    style = MaterialTheme.typography.labelMedium,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textSecondary,
                )
            }
            Text(
                text = row.body,
                style = MaterialTheme.typography.bodyMedium,
                // A comment still being written to the server is dimmed rather
                // than shown as confirmed. It is the only honest state: it may
                // still fail.
                color = if (row.pending) UsTheme.extended.textMuted else UsTheme.extended.textPrimary,
            )
        }
    }
}

@Composable
private fun AppendFooter(loading: Boolean, failed: Boolean, onRetry: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.l),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        when {
            loading -> CircularProgressIndicator(color = MaterialTheme.colorScheme.primary)
            failed -> {
                Text(
                    text = "Couldn't load more comments.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                )
                UsSecondaryButton(text = "Try again", onClick = onRetry)
            }
        }
    }
}

@Composable
private fun Composer(
    state: CommentsUiState,
    onDraftChange: (String) -> Unit,
    onSubmit: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            // imePadding lifts the composer above the keyboard; without it the
            // field is covered by the very keyboard being used to type in it.
            .imePadding()
            .navigationBarsPadding()
            .padding(UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        state.submitError?.let {
            Text(
                text = commentSubmitErrorMessage(it),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }

        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            OutlinedTextField(
                value = state.draft,
                onValueChange = onDraftChange,
                modifier = Modifier.weight(1f),
                placeholder = { Text("Add a comment…") },
                enabled = !state.submitting,
                maxLines = COMPOSER_MAX_LINES,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
                keyboardActions = KeyboardActions(onSend = { if (state.canSubmit) onSubmit() }),
            )
            IconButton(
                onClick = onSubmit,
                // Disabled rather than hidden: a send control that vanishes
                // while typing is disorienting, and disabled explains itself.
                enabled = state.canSubmit,
                modifier = Modifier.semantics { contentDescription = "Post comment" },
            ) {
                if (state.submitting) {
                    CircularProgressIndicator(
                        strokeWidth = 2.dp,
                        modifier = Modifier.padding(UsTheme.spacing.s),
                        color = MaterialTheme.colorScheme.primary,
                    )
                } else {
                    Icon(
                        imageVector = UsIcons.Send,
                        contentDescription = null,
                        tint = if (state.canSubmit) {
                            UsTheme.extended.textPrimary
                        } else {
                            UsTheme.extended.textGhost
                        },
                    )
                }
            }
        }
    }
}

/**
 * What the composer says under a failed submit.
 *
 * `COMMENTS_RESTRICTED` is a friends-only refusal, not a transient failure —
 * `state.draft` already keeps the typed text (see [CommentsController.submit]),
 * so nothing here needs to re-save it, only word the message honestly: no
 * "tap send to try again" for a request that will keep failing until the
 * audience changes.
 */
internal fun commentSubmitErrorMessage(error: AppError): String =
    if (error is AppError.Forbidden && error.code == CODE_COMMENTS_RESTRICTED) {
        "Only friends can comment on this post"
    } else {
        "Your comment wasn't posted. Tap send to try again."
    }

private const val CODE_COMMENTS_RESTRICTED = "COMMENTS_RESTRICTED"

private fun commentsHeading(count: Int): String = when (count) {
    0 -> "Comments"
    1 -> "1 comment"
    else -> "${formatCount(count)} comments"
}

/**
 * How many rows before the end to start fetching.
 *
 * Small on purpose: a comment page is 20 rows, and prefetching further ahead
 * spends data on a conversation the reader may not scroll into.
 */
private const val PREFETCH_DISTANCE = 3

private const val COMPOSER_MAX_LINES = 5
