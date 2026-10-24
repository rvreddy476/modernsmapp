package com.us.android.feature.feed.ui.hashtag

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.feed.ui.FeedContent
import com.us.android.feature.feed.ui.FeedEmptyCopy
import com.us.android.feature.feed.ui.FeedMode
import com.us.android.feature.feed.ui.FeedViewModel

/**
 * The Hashtags feed (followed hashtags, 2026-10-24): the recent posts of
 * every tag the viewer follows, in Home's full-screen pager. Opened from
 * Home's More menu — a section of Home, not a header tab (the HashTag tab
 * left the header on 2026-10-19). The bar's # opens the list of tags.
 */
@Composable
fun FollowedHashtagPostsScreen(
    onBack: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    onOpenHashtag: (tag: String) -> Unit,
    onManage: () -> Unit,
    viewModel: FeedViewModel = hiltViewModel<FeedViewModel, FeedViewModel.Factory>(
        creationCallback = { factory -> factory.create(FeedMode.FollowedHashtags) },
    ),
) {
    UsScaffold(
        topBar = {
            UsTopBar(
                title = "Hashtags",
                onBack = onBack,
                actions = {
                    IconButton(onClick = onManage, modifier = Modifier.testTag("followed_hashtags_manage")) {
                        Icon(
                            imageVector = UsIcons.Hash,
                            contentDescription = "Hashtags you follow",
                            tint = UsTheme.extended.textPrimary,
                        )
                    }
                },
            )
        },
        applyPageGutter = false,
    ) { padding ->
        FeedContent(
            viewModel = viewModel,
            onOpenAuthor = onOpenAuthor,
            onOpenHashtag = onOpenHashtag,
            empty = FeedEmptyCopy(
                title = "No posts from your hashtags yet",
                detail = "Follow a hashtag from its page and its new posts will show up here.",
            ),
            modifier = Modifier.padding(padding),
        )
    }
}

/**
 * Followed hashtags (2026-10-24): every tag the viewer follows, newest
 * first, with Following / Follow on each row. An unfollowed row stays where
 * it is, saying Follow, so a slip is one tap to undo. Reached from the Me
 * drawer and from the Hashtags feed.
 */
@Composable
fun FollowedHashtagsScreen(
    onBack: () -> Unit,
    onOpenHashtag: (tag: String) -> Unit,
    viewModel: FollowedHashtagsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar(title = "Followed hashtags", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            when {
                state.loading -> UsLoadingState(label = "Loading your hashtags")
                state.loadError != null -> UsErrorState(message = state.loadError.orEmpty(), onRetry = viewModel::load)
                state.empty -> UsEmptyState(
                    title = "You don't follow any hashtags yet",
                    detail = "Open a hashtag and tap Follow to see its posts in your Hashtags feed.",
                    modifier = Modifier.testTag("followed_hashtags_empty"),
                )
                else -> FollowedList(state, viewModel, onOpenHashtag)
            }
            UsMessageHost(message = state.message, onDismiss = viewModel::dismissMessage)
        }
    }
}

@Composable
private fun FollowedList(
    state: FollowedHashtagsUiState,
    viewModel: FollowedHashtagsViewModel,
    onOpenHashtag: (tag: String) -> Unit,
) {
    LazyColumn(modifier = Modifier.fillMaxSize().testTag("followed_hashtags_list")) {
        items(state.rows, key = { it.tag }) { row ->
            FollowedRow(row = row, onOpen = { onOpenHashtag(row.tag) }, onToggle = { viewModel.toggle(row.tag) })
        }
        if (state.nextCursor != null) {
            item(key = "more") {
                UsSecondaryButton(
                    text = if (state.loadingMore) "Loading…" else "Show more",
                    onClick = viewModel::loadMore,
                    enabled = !state.loadingMore,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(UsTheme.spacing.pageHorizontal),
                )
            }
        }
    }
}

@Composable
private fun FollowedRow(row: FollowedHashtagRow, onOpen: () -> Unit, onToggle: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .height(ROW_HEIGHT)
            .clickable(role = Role.Button, onClickLabel = "Open ${row.label}", onClick = onOpen)
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .testTag("followed_hashtag:${row.tag}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Box(
            modifier = Modifier
                .size(ICON_SQUARE)
                .background(UsTheme.extended.fillSubtle, RoundedCornerShape(UsTheme.radii.medium)),
            contentAlignment = Alignment.Center,
        ) {
            Icon(UsIcons.Hash, contentDescription = null, tint = UsTheme.extended.textPrimary, modifier = Modifier.size(GLYPH))
        }
        Text(
            text = row.label,
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        UsPillButton(
            text = if (row.following) "Following" else "Follow",
            onClick = onToggle,
            filled = !row.following,
            busy = row.busy,
            modifier = Modifier
                .semantics { contentDescription = followChangeLabel(row) }
                .testTag("followed_hashtag_toggle:${row.tag}"),
        )
    }
}

private val ROW_HEIGHT = 64.dp
private val ICON_SQUARE = 40.dp
private val GLYPH = 20.dp
