package com.us.android.feature.feed.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.TrendingHashtag
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * The HashTag tab: today's trending tags, most-used first.
 *
 * Each row is the tag in bold, its post count muted, and a chevron — a plain
 * list, because the destination is another list and the row's only job is to
 * say what it opens. The empty state is a real state on a quiet day (the dev
 * gateway answered `{"data":{"hashtags":[]}}` on 2026-09-04), not an error.
 */
@Composable
internal fun TrendingHashtagsList(
    state: TrendingState,
    onOpenHashtag: (tag: String) -> Unit,
    onRetry: () -> Unit,
    modifier: Modifier = Modifier,
) {
    when (state) {
        TrendingState.Loading -> UsLoadingState(modifier = modifier, label = "Loading trending tags")

        is TrendingState.Error -> UsErrorState(
            message = state.error.feedMessage(),
            modifier = modifier,
            onRetry = onRetry,
        )

        is TrendingState.Content -> if (state.tags.isEmpty()) {
            UsEmptyState(
                title = "No trending tags yet",
                detail = "Tags people post with today will show up here.",
                modifier = modifier,
            )
        } else {
            LazyColumn(modifier = modifier.fillMaxSize()) {
                items(state.tags, key = { it.name }) { tag ->
                    TrendingHashtagRow(tag = tag, onClick = { onOpenHashtag(tag.name) })
                    HorizontalDivider(color = UsTheme.extended.borderSubtle)
                }
            }
        }
    }
}

@Composable
private fun TrendingHashtagRow(
    tag: TrendingHashtag,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = UsTheme.spacing.xxxxl, vertical = UsTheme.spacing.xl),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = tag.label,
                style = MaterialTheme.typography.titleMedium,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
            )
            Text(
                text = tag.postCountLabel(),
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
        }
        Icon(
            imageVector = UsIcons.ChevronRight,
            // The row itself is the control; the glyph only repeats "opens".
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
        )
    }
}

private fun TrendingHashtag.postCountLabel(): String =
    if (postCount == 1L) "1 post" else "$postCount posts"

@Preview(name = "Trending tags", showBackground = true, backgroundColor = 0xFF0B1220)
@Composable
private fun TrendingHashtagsPreview() {
    UsTheme {
        TrendingHashtagsList(
            state = TrendingState.Content(
                listOf(
                    TrendingHashtag("android", "#android", postCount = 2),
                    TrendingHashtag("momentum", "#momentum", postCount = 1),
                ),
            ),
            onOpenHashtag = {},
            onRetry = {},
        )
    }
}
