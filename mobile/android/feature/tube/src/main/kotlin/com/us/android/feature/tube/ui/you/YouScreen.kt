package com.us.android.feature.tube.ui.you

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.staggeredgrid.LazyStaggeredGridScope
import androidx.compose.foundation.lazy.staggeredgrid.LazyVerticalStaggeredGrid
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridCells
import androidx.compose.foundation.lazy.staggeredgrid.StaggeredGridItemSpan
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.paging.LoadState
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import androidx.paging.compose.itemKey
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsFollowButton
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.ChannelState
import com.us.android.core.feed.data.ContinueWatching
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.ui.channel.CreateChannelSheet
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.model.Channel
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsErrorState
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.ui.TubeMoreHost
import com.us.android.feature.tube.ui.TubePage
import com.us.android.feature.tube.ui.TubeTab
import com.us.android.feature.tube.ui.TubeViewer
import com.us.android.feature.tube.ui.bleed
import com.us.android.feature.tube.ui.channel.EditChannelSheet
import com.us.android.feature.tube.ui.home.ContinueRow
import com.us.android.feature.tube.ui.home.GRID_COLUMNS
import com.us.android.feature.tube.ui.home.GridCard
import com.us.android.feature.tube.ui.home.SectionTitle
import com.us.android.feature.tube.ui.home.ShelfSkeleton
import com.us.android.feature.tube.ui.home.TubeGridSkeleton
import com.us.android.feature.tube.ui.home.appendFooter
import com.us.android.feature.tube.ui.pressScale
import com.us.android.feature.tube.ui.rememberTubeMoreState

/**
 * You (Momentum look, 2026-09-05): the same chrome as home, the viewer's
 * channel header — avatar, name, `@handle`, the video count, "Edit
 * channel" — or the "Create your channel" prompt, then "Your videos" as the
 * mosaic, "Continue watching" and "Saved". A section with nothing to show
 * is not drawn — except Your videos, whose empty state is the invitation
 * to post one.
 */
@Composable
fun YouScreen(
    destinations: TubeDestinations,
    viewModel: YouViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
) {
    val channel by viewModel.channel.collectAsStateWithLifecycle()
    val viewer by viewModel.viewer.collectAsStateWithLifecycle()
    val continueWatching by viewModel.continueWatching.collectAsStateWithLifecycle()
    val own = viewModel.ownVideos.collectAsLazyPagingItems()
    val saved = viewModel.saved.collectAsLazyPagingItems()
    val moreState = rememberTubeMoreState()
    var sheet by rememberSaveable { mutableStateOf(YouSheet.None) }

    val actions = YouActions(
        open = YouOpenActions(
            own = { item ->
                viewModel.onOpen(item, own.itemSnapshotList.items)
                destinations.onOpenVideo(item.id)
            },
            saved = { item ->
                viewModel.onOpen(item, saved.itemSnapshotList.items)
                destinations.onOpenVideo(item.id)
            },
            resume = { item ->
                viewModel.onOpen(item, continueWatching.orEmpty().map { it.item })
                destinations.onOpenVideo(item.id)
            },
        ),
        channel = YouChannelActions(
            create = { sheet = YouSheet.Create },
            edit = { sheet = YouSheet.Edit },
            retry = viewModel::retryChannel,
        ),
        more = { item -> moreState.open(item, suggested = false) },
        postVideo = destinations.onCreateVideo,
        thumbFor = viewModel::thumb,
    )

    TubePage(selected = TubeTab.YOU, destinations = destinations) { padding ->
        Box(modifier = Modifier.fillMaxSize()) {
            LazyVerticalStaggeredGrid(
                columns = StaggeredGridCells.Fixed(GRID_COLUMNS),
                modifier = Modifier
                    .fillMaxSize()
                    .testTag("tube_you"),
                contentPadding = PaddingValues(
                    start = UsTheme.spacing.pageHorizontal,
                    end = UsTheme.spacing.pageHorizontal,
                    bottom = padding.calculateBottomPadding() + UsTheme.spacing.xxl,
                ),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                verticalItemSpacing = UsTheme.spacing.l,
            ) {
                item(key = "channel", span = StaggeredGridItemSpan.FullLine) {
                    ChannelHeader(state = channel, viewer = viewer, actions = actions)
                }
                item(key = "subscriptions", span = StaggeredGridItemSpan.FullLine) {
                    SubscriptionsRow(onOpen = { destinations.onOpenTab(TubeTab.SUBSCRIPTIONS) })
                }
                ownVideosSection(own, actions)
                continueWatchingSection(continueWatching, actions)
                savedSection(saved, actions)
            }
            TubeMoreHost(
                state = moreState,
                overlays = viewModel.overlays,
                followEdges = viewModel.followEdges,
                ownUserId = viewModel.ownUserId,
                more = more,
            )
        }
    }

    YouSheets(sheet = sheet, channel = channel, onClose = { sheet = YouSheet.None })
}

private enum class YouSheet { None, Create, Edit }

/**
 * The way into Subscriptions — videos from the channels the viewer follows.
 * It left Tube's bar for this page (founder, 2026-09-05: "keep it inside
 * profile only"), so the row is the one door to it: a glass row with the
 * list glyph, the title, a line saying what is behind it, and a chevron.
 */
@Composable
private fun SubscriptionsRow(onOpen: () -> Unit) {
    val shape = RoundedCornerShape(ROW_RADIUS)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.glassBg)
            .border(ROW_HAIRLINE, UsTheme.extended.glassBorder, shape)
            .pressScale(onOpen)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.l)
            .testTag("tube_you_subscriptions"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        RowGlyph(UsIcons.ListVideo)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = "Subscriptions",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = "Videos from channels you follow",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        RowGlyph(UsIcons.ChevronRight)
    }
}

@Composable
private fun RowGlyph(icon: ImageVector) {
    Icon(
        imageVector = icon,
        contentDescription = null,
        tint = UsTheme.extended.textPrimary,
        modifier = Modifier.width(ROW_GLYPH),
    )
}

private val ROW_RADIUS = 18.dp
private val ROW_HAIRLINE = 1.dp
private val ROW_GLYPH = 22.dp

/** The create sheet, the edit sheet, or nothing — one at a time over the page. */
@Composable
private fun YouSheets(sheet: YouSheet, channel: ChannelState, onClose: () -> Unit) {
    when (sheet) {
        YouSheet.Create -> CreateChannelSheet(onCreated = { onClose() }, onDismiss = onClose)
        YouSheet.Edit -> (channel as? ChannelState.Present)?.let { present ->
            EditChannelSheet(channel = present.channel, onDismiss = onClose)
        }
        YouSheet.None -> Unit
    }
}

/** Everything the page's rows can do, grouped, so the section builders stay short. */
internal class YouActions(
    val open: YouOpenActions,
    val channel: YouChannelActions,
    val more: (FeedItem) -> Unit,
    val postVideo: () -> Unit,
    val thumbFor: (FeedItem) -> VideoThumb,
)

/** Opening a video from each of the three lists — its list becomes the watch queue. */
internal class YouOpenActions(
    val own: (FeedItem) -> Unit,
    val saved: (FeedItem) -> Unit,
    val resume: (FeedItem) -> Unit,
)

/** The channel header's three ways forward. */
internal class YouChannelActions(
    val create: () -> Unit,
    val edit: () -> Unit,
    val retry: () -> Unit,
)

/**
 * The top of You, by what is known about the channel: the channel's
 * header; the create prompt (a 404 from the server is "none", the normal
 * case for a new account); a lookup that failed, with Retry; or, while
 * unknown, the viewer's own row so the page is never blank.
 */
@Composable
private fun ChannelHeader(state: ChannelState, viewer: TubeViewer?, actions: YouActions) {
    when (state) {
        is ChannelState.Present -> ChannelCard(channel = state.channel, viewer = viewer, onEdit = actions.channel.edit)
        ChannelState.None -> CreatePrompt(viewer = viewer, onCreate = actions.channel.create)
        is ChannelState.Failed -> UsErrorState(
            message = state.message,
            onRetry = actions.channel.retry,
            modifier = Modifier.padding(vertical = UsTheme.spacing.xxl),
        )
        ChannelState.Unknown -> ViewerRow(viewer)
    }
}

/** Avatar with the ember ring, name, `@handle`, the count, and "Edit channel". */
@Composable
private fun ChannelCard(channel: Channel, viewer: TubeViewer?, onEdit: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.xxl)
            .testTag("tube_you_channel"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
        ) {
            UsAvatar(
                name = channel.name,
                seed = channel.userId,
                size = UsAvatarSize.Large,
                imageUrl = channel.avatarUrl ?: viewer?.avatarUrl,
                hasRing = true,
                contentDescription = "${channel.name}'s channel photo",
            )
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                Text(
                    text = channel.name,
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = channel.handleForDisplay,
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                )
                Text(
                    text = videoCountLabel(channel.videoCount),
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                    modifier = Modifier.testTag("tube_you_count"),
                )
            }
        }
        if (channel.about.isNotBlank()) {
            Text(
                text = channel.about,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
                maxLines = ABOUT_LINES,
                overflow = TextOverflow.Ellipsis,
            )
        }
        UsPillButton(
            text = "Edit channel",
            onClick = onEdit,
            filled = false,
            modifier = Modifier.testTag("tube_you_edit"),
        )
    }
}

/** No channel yet: the invitation, on glass. */
@Composable
private fun CreatePrompt(viewer: TubeViewer?, onCreate: () -> Unit) {
    val shape = RoundedCornerShape(PROMPT_RADIUS)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.xxl)
            .clip(shape)
            .background(UsTheme.extended.glassBg)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
            .padding(UsTheme.spacing.xxl)
            .testTag("tube_you_create"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            UsAvatar(
                name = viewer?.name ?: "You",
                seed = viewer?.userId ?: "you",
                size = UsAvatarSize.Medium,
                imageUrl = viewer?.avatarUrl,
            )
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                Text(
                    text = "Create your channel",
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                )
                Text(
                    text = "Videos post under a channel: a name and an @handle of its own.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
            }
        }
        UsFollowButton(text = "Create channel", onClick = onCreate, modifier = Modifier.width(PROMPT_BUTTON_WIDTH))
    }
}

@Composable
private fun ViewerRow(viewer: TubeViewer?) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.xxl)
            .testTag("tube_you_profile"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        UsAvatar(
            name = viewer?.name ?: "You",
            seed = viewer?.userId ?: "you",
            size = UsAvatarSize.Medium,
            imageUrl = viewer?.avatarUrl,
        )
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(
                text = viewer?.name ?: "You",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            viewer?.handle?.let { handle ->
                Text(
                    text = handle,
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                )
            }
        }
    }
}

/** "Your videos": the viewer's own as tiles, or — when there are none — the invitation to post one. */
private fun LazyStaggeredGridScope.ownVideosSection(own: LazyPagingItems<FeedItem>, actions: YouActions) {
    item(key = "own:header", span = StaggeredGridItemSpan.FullLine) {
        SectionTitle(title = "Your videos", modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal))
    }
    val refresh = own.loadState.refresh
    when {
        refresh is LoadState.Loading && own.itemCount == 0 -> item(
            key = "own:loading",
            span = StaggeredGridItemSpan.FullLine,
        ) {
            TubeGridSkeleton(modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal))
        }
        own.itemCount == 0 -> item(key = "own:empty", span = StaggeredGridItemSpan.FullLine) {
            NoVideosYet(onPost = actions.postVideo)
        }
        else -> {
            items(count = own.itemCount, key = own.itemKey { "own:${it.id}" }) { index ->
                val item = own[index] ?: return@items
                GridCard(
                    item = item,
                    thumb = actions.thumbFor(item),
                    onClick = { actions.open.own(item) },
                    onMore = { actions.more(item) },
                )
            }
            appendFooter(own)
        }
    }
}

/** "Continue watching" as the ring row; absent when there is nothing unfinished. */
private fun LazyStaggeredGridScope.continueWatchingSection(rows: List<ContinueWatching>?, actions: YouActions) {
    when {
        rows == null -> item(key = "continue:loading", span = StaggeredGridItemSpan.FullLine) {
            Column(modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal)) {
                SectionTitle(title = "Continue watching")
                ShelfSkeleton()
            }
        }
        rows.isEmpty() -> Unit
        else -> item(key = "continue", span = StaggeredGridItemSpan.FullLine) {
            ContinueRow(
                rows = rows,
                thumbFor = actions.thumbFor,
                onOpen = actions.open.resume,
                modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal),
            )
        }
    }
}

/** "Saved": the viewer's bookmarked videos; absent when there are none or the list cannot be read. */
private fun LazyStaggeredGridScope.savedSection(saved: LazyPagingItems<FeedItem>, actions: YouActions) {
    val refresh = saved.loadState.refresh
    when {
        refresh is LoadState.Loading && saved.itemCount == 0 -> {
            item(key = "saved:header", span = StaggeredGridItemSpan.FullLine) {
                SectionTitle(title = "Saved", modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal))
            }
            item(key = "saved:loading", span = StaggeredGridItemSpan.FullLine) {
                ShelfSkeleton(modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal))
            }
        }
        saved.itemCount == 0 -> Unit
        else -> {
            item(key = "saved:header", span = StaggeredGridItemSpan.FullLine) {
                SectionTitle(title = "Saved", modifier = Modifier.bleed(UsTheme.spacing.pageHorizontal))
            }
            items(count = saved.itemCount, key = saved.itemKey { "saved:${it.id}" }) { index ->
                val item = saved[index] ?: return@items
                GridCard(
                    item = item,
                    thumb = actions.thumbFor(item),
                    onClick = { actions.open.saved(item) },
                    onMore = { actions.more(item) },
                )
            }
        }
    }
}

@Composable
private fun NoVideosYet(onPost: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.l)
            .testTag("tube_you_empty"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Text(
            text = "You haven't posted a video yet",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
        )
        UsFollowButton(text = "Post a video", onClick = onPost, modifier = Modifier.width(POST_BUTTON_WIDTH))
    }
}

/** "3 videos", "1 video", "No videos yet". */
internal fun videoCountLabel(count: Int): String = when (count) {
    0 -> "No videos yet"
    1 -> "1 video"
    else -> "$count videos"
}

private const val ABOUT_LINES = 3
private val HAIRLINE = 1.dp
private val PROMPT_RADIUS = 20.dp
private val PROMPT_BUTTON_WIDTH = 160.dp
private val POST_BUTTON_WIDTH = 140.dp
