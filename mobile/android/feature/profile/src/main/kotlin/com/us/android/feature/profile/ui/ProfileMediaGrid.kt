package com.us.android.feature.profile.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.paging.LoadState
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.ui.channel.CreateChannelSheet
import com.us.android.core.media.publish.PublishRing
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ring
import com.us.android.core.media.publish.ringLabel
import com.us.android.core.media.publish.ringPercentLabel
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import java.io.File

/**
 * The profile's media grid (2026-09-05): three glass tabs — Posts, Reels,
 * Videos — over three columns of tiles, read through Paging. On the
 * viewer's own profile the videos they are posting sit FIRST on their
 * tab, each under an ember ring with the percent inside it while the
 * bytes go up and a spin while the server works, a clock when it is
 * scheduled; then the posts the server is holding for a later time, each
 * with "Scheduled · 6 Sep 18:30"; then the real tiles. A failure shows
 * the tile dimmed, and a tap opens Retry / Discard (or "Create channel",
 * when that is what the server asked for).
 *
 * Drawn as rows inside the profile's scrolling column rather than as a lazy
 * grid of its own: a lazy grid cannot live inside a vertical scroll, and
 * the rows still read their tiles through [LazyPagingItems], which is what
 * asks for the next page as they are laid out.
 */
@Composable
internal fun ProfileMediaGrid(
    onOpenPost: ((postId: String, contentType: String) -> Unit)?,
    /**
     * A publish the viewer watched here has landed, and its content type says
     * where it lives now. `:app` carries them on to that feed (founder,
     * 2026-09-06); null leaves them on the profile with the finished tile.
     */
    onPublished: ((contentType: String) -> Unit)?,
    modifier: Modifier = Modifier,
    viewModel: ProfileGridViewModel = hiltViewModel(),
) {
    val tab by viewModel.tab.collectAsStateWithLifecycle()
    val pending by viewModel.pending.collectAsStateWithLifecycle()
    val scheduled by viewModel.scheduled.collectAsStateWithLifecycle()
    val reloads by viewModel.reloads.collectAsStateWithLifecycle()
    val posts = viewModel.posts.collectAsLazyPagingItems()
    val reels = viewModel.reels.collectAsLazyPagingItems()
    val longVideos = viewModel.longVideos.collectAsLazyPagingItems()
    var failureKey by rememberSaveable { mutableStateOf<String?>(null) }
    var createChannelKey by rememberSaveable { mutableStateOf<String?>(null) }

    LaunchedEffect(reloads) {
        if ((reloads[ProfileGridTab.POSTS] ?: 0) > 0) posts.refresh()
        if ((reloads[ProfileGridTab.REELS] ?: 0) > 0) reels.refresh()
        if ((reloads[ProfileGridTab.VIDEOS] ?: 0) > 0) longVideos.refresh()
    }

    // The last leg of the journey. Collected here rather than watched as
    // state because arriving at a feed HAPPENS ONCE — a state would fire it
    // again on the next recomposition and every return to this screen.
    if (onPublished != null) {
        LaunchedEffect(viewModel) {
            viewModel.published.collect { onPublished(it.contentType) }
        }
    }

    Column(modifier = modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        GridTabs(selected = tab, onSelect = viewModel::select)
        val items = when (tab) {
            ProfileGridTab.POSTS -> posts
            ProfileGridTab.REELS -> reels
            ProfileGridTab.VIDEOS -> longVideos
        }
        val heads = pending.filter { it.tab == tab }.map<PendingVideoTile, GridHead> { GridHead.Pending(it) } +
            scheduled.filter { it.tab == tab }.map { GridHead.Scheduled(it) }
        GridRows(
            tab = tab,
            items = items,
            heads = heads,
            thumbFor = viewModel::thumb,
            onOpen = { item -> onOpenPost?.invoke(item.id, item.feedContentType) },
            onPendingTap = { tile -> if (tile.failure != null) failureKey = tile.creationKey },
        )
    }

    val failing = pending.firstOrNull { it.creationKey == failureKey }
    val failure = failing?.failure
    if (failing != null && failure != null) {
        PublishFailureSheet(
            failure = failure,
            onRetry = {
                failureKey = null
                viewModel.retryPublish(failing.creationKey)
            },
            onDiscard = {
                failureKey = null
                viewModel.discardPublish(failing.creationKey)
            },
            onCreateChannel = {
                failureKey = null
                createChannelKey = failing.creationKey
            },
            onDismiss = { failureKey = null },
        )
    }
    createChannelKey?.let { key ->
        CreateChannelSheet(
            onCreated = {
                createChannelKey = null
                viewModel.retryPublish(key)
            },
            onDismiss = { createChannelKey = null },
        )
    }
}

/**
 * "Uploaded successfully", in green, over the profile (founder, 2026-09-06:
 * "no OK button needed — just show the message, it disappears, and then go
 * to that post, video or reel").
 *
 * Drawn by the profile as an OVERLAY rather than as a row of the grid: the
 * viewer has just landed here and the grid may well be below the fold, and
 * a message nobody sees is not a message. It carries no control at all —
 * not even a close — because the one thing the founder said about it twice
 * is that it takes itself away.
 *
 * Its ViewModel is the grid's: `hiltViewModel()` in the same destination
 * returns the same instance, so this is the same publish the tiles above
 * were drawing, not a second reading of the queue.
 */
@Composable
internal fun PublishSuccessBanner(
    modifier: Modifier = Modifier,
    viewModel: ProfileGridViewModel = hiltViewModel(),
) {
    val success by viewModel.success.collectAsStateWithLifecycle()
    // Held so the words stay put while the banner fades out, after the
    // state that carried them has already gone.
    var message by remember { mutableStateOf("") }
    success?.let { message = it.message }

    AnimatedVisibility(
        visible = success != null,
        enter = fadeIn() + slideInVertically { -it },
        exit = fadeOut(),
        modifier = modifier,
    ) {
        val shape = RoundedCornerShape(UsTheme.radii.full)
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            modifier = Modifier
                .padding(UsTheme.spacing.l)
                .clip(shape)
                .background(UsTheme.extended.statusSuccess)
                .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.m)
                .semantics { liveRegion = LiveRegionMode.Polite }
                .testTag("profile_publish_success"),
        ) {
            Icon(
                imageVector = UsIcons.Check,
                contentDescription = null,
                tint = UsTheme.extended.brandNavy,
                modifier = Modifier.size(SUCCESS_GLYPH),
            )
            Text(
                text = message,
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.brandNavy,
                maxLines = 1,
            )
        }
    }
}

/** A tile ahead of the paged rows: a video still posting, or a post held for later. */
private sealed interface GridHead {
    data class Pending(val tile: PendingVideoTile) : GridHead
    data class Scheduled(val tile: ScheduledTile) : GridHead
}

/** Three glass pills; the selected one is white with navy text — the app's selection rule. */
@Composable
private fun GridTabs(selected: ProfileGridTab, onSelect: (ProfileGridTab) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .testTag("profile_grid_tabs"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        ProfileGridTab.entries.forEach { tab ->
            val active = tab == selected
            val shape = RoundedCornerShape(UsTheme.radii.full)
            Box(
                contentAlignment = Alignment.Center,
                modifier = Modifier
                    .height(TAB_HEIGHT)
                    .clip(shape)
                    .background(if (active) Color.White else UsTheme.extended.glassBg)
                    .border(HAIRLINE, if (active) Color.White else UsTheme.extended.glassBorder, shape)
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                        onClick = { onSelect(tab) },
                    )
                    .semantics {
                        role = Role.Tab
                        this.selected = active
                    }
                    .padding(horizontal = TAB_HORIZONTAL)
                    .testTag("profile_grid_tab:${tab.name.lowercase()}"),
            ) {
                Text(
                    text = tab.label,
                    style = MaterialTheme.typography.labelLarge,
                    fontWeight = FontWeight.SemiBold,
                    color = if (active) UsTheme.extended.brandNavy else UsTheme.extended.textPrimary,
                    maxLines = 1,
                )
            }
        }
    }
}

/**
 * The tiles in rows of three: the head tiles first when there are any,
 * then the page's rows, then the list's own state — a loader, an error
 * with Retry, or the empty message.
 */
@Composable
private fun GridRows(
    tab: ProfileGridTab,
    items: LazyPagingItems<FeedItem>,
    heads: List<GridHead>,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
    onPendingTap: (PendingVideoTile) -> Unit,
) {
    val slots = heads.size + items.itemCount
    Column(verticalArrangement = Arrangement.spacedBy(GRID_GAP)) {
        for (rowStart in 0 until slots step GRID_COLUMNS) {
            Row(horizontalArrangement = Arrangement.spacedBy(GRID_GAP)) {
                for (slot in rowStart until rowStart + GRID_COLUMNS) {
                    val cell = Modifier
                        .weight(1f)
                        .aspectRatio(tileAspect(tab))
                    when {
                        slot >= slots -> Spacer(cell)
                        slot < heads.size -> when (val head = heads[slot]) {
                            is GridHead.Pending -> PendingTile(
                                tile = head.tile,
                                onClick = { onPendingTap(head.tile) },
                                modifier = cell,
                            )
                            is GridHead.Scheduled -> ScheduledTileView(
                                tile = head.tile,
                                thumb = thumbFor(head.tile.item),
                                onClick = { onOpen(head.tile.item) },
                                modifier = cell,
                            )
                        }
                        else -> PagedTile(
                            item = items[slot - heads.size],
                            thumbFor = thumbFor,
                            onOpen = onOpen,
                            modifier = cell,
                        )
                    }
                }
            }
        }
        GridState(tab = tab, items = items, hasHead = heads.isNotEmpty())
    }
}

/** A tile for a row Paging has loaded; a placeholder-sized gap while it has not. */
@Composable
private fun PagedTile(
    item: FeedItem?,
    thumbFor: (FeedItem) -> VideoThumb,
    onOpen: (FeedItem) -> Unit,
    modifier: Modifier,
) {
    if (item != null) {
        MediaTile(item = item, thumb = thumbFor(item), onClick = { onOpen(item) }, modifier = modifier)
    } else {
        Spacer(modifier)
    }
}

/** The list's own state under the rows: the first load, its failure, the empty message, the next page. */
@Composable
private fun GridState(tab: ProfileGridTab, items: LazyPagingItems<FeedItem>, hasHead: Boolean) {
    val refresh = items.loadState.refresh
    val count = items.itemCount
    Column {
        when {
            refresh is LoadState.Loading && count == 0 -> UsLoadingState(
                label = "Loading ${tab.label.lowercase()}",
                modifier = Modifier
                    .fillMaxWidth()
                    .height(STATE_HEIGHT),
            )
            refresh is LoadState.Error && count == 0 -> UsErrorState(
                message = "We couldn't load ${tab.label.lowercase()}.",
                onRetry = items::retry,
                modifier = Modifier
                    .fillMaxWidth()
                    .height(STATE_HEIGHT),
            )
            refresh is LoadState.NotLoading && count == 0 && !hasHead -> UsEmptyState(
                title = "No ${tab.label.lowercase()} yet",
                modifier = Modifier
                    .fillMaxWidth()
                    .height(STATE_HEIGHT)
                    .testTag("profile_grid_empty"),
            )
            items.loadState.append is LoadState.Loading -> UsLoadingState(
                label = "Loading more",
                modifier = Modifier
                    .fillMaxWidth()
                    .height(APPEND_HEIGHT),
            )
            items.loadState.append is LoadState.Error -> UsSecondaryButton(
                text = "Load more",
                onClick = items::retry,
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

/** Square for posts, 3:4 for reels, 16:9 for videos — the shape the content has. */
internal fun tileAspect(tab: ProfileGridTab): Float = when (tab) {
    ProfileGridTab.POSTS -> SQUARE
    ProfileGridTab.REELS -> PORTRAIT_TILE
    ProfileGridTab.VIDEOS -> LANDSCAPE
}

/** One post as a tile: its still (the cover for a video), a film glyph for the video kinds. */
@Composable
private fun MediaTile(item: FeedItem, thumb: VideoThumb, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(UsTheme.radii.small)
    Box(
        modifier = modifier
            .clip(shape)
            .background(UsTheme.extended.bgCard)
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
            )
            .semantics {
                role = Role.Button
                contentDescription = "Open ${item.title.ifBlank { item.text }.ifBlank { "post" }}"
            }
            .testTag("profile_tile:${item.id}"),
    ) {
        TileStill(item = item, thumb = thumb)
        if (item.feedContentType == ProfileGridTab.REELS.contentType ||
            item.feedContentType == ProfileGridTab.VIDEOS.contentType
        ) {
            Icon(
                imageVector = UsIcons.Play,
                contentDescription = null,
                tint = Color.White,
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(UsTheme.spacing.s)
                    .size(KIND_GLYPH),
            )
        }
    }
}

/** The still behind a tile: the cover or the first media, else the text. */
@Composable
private fun TileStill(item: FeedItem, thumb: VideoThumb) {
    val url = thumb.url ?: item.media.firstOrNull()?.variants?.values?.firstOrNull()
    if (url != null) {
        AsyncImage(
            model = url,
            contentDescription = null,
            contentScale = ContentScale.Crop,
            modifier = Modifier.fillMaxSize(),
        )
    } else if (item.text.isNotBlank()) {
        Text(
            text = item.text,
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textSecondary,
            maxLines = TEXT_TILE_LINES,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(UsTheme.spacing.m),
        )
    }
}

/**
 * A post the server is holding (2026-09-05): its still, a clock badge, and
 * "Scheduled · 6 Sep 18:30" on a scrim along the bottom. It stays until the
 * server publishes it, when the grid's refresh carries the real tile.
 */
@Composable
private fun ScheduledTileView(
    tile: ScheduledTile,
    thumb: VideoThumb,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val shape = RoundedCornerShape(UsTheme.radii.small)
    Box(
        modifier = modifier
            .clip(shape)
            .background(UsTheme.extended.bgCard)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
            )
            .semantics {
                role = Role.Button
                contentDescription = "${tile.label}. ${tile.item.title.ifBlank { tile.item.text }}"
            }
            .testTag("profile_scheduled:${tile.item.id}"),
    ) {
        TileStill(item = tile.item, thumb = thumb)
        ClockBadge(modifier = Modifier.align(Alignment.TopStart))
        Box(
            modifier = Modifier
                .align(Alignment.BottomCenter)
                .fillMaxWidth()
                .background(Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = SCRIM_ALPHA))))
                .padding(horizontal = UsTheme.spacing.s, vertical = UsTheme.spacing.s),
        ) {
            Text(
                text = tile.label,
                style = MaterialTheme.typography.labelSmall,
                fontSize = SCHEDULE_LABEL_SIZE,
                fontWeight = FontWeight.SemiBold,
                color = Color.White,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** A small clock on a dark plate — the mark of a scheduled post. */
@Composable
private fun ClockBadge(modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .padding(UsTheme.spacing.s)
            .size(BADGE_SIZE)
            .clip(CircleShape)
            .background(Color.Black.copy(alpha = PLATE_ALPHA))
            .testTag("profile_clock"),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = UsIcons.Clock,
            contentDescription = "Scheduled",
            tint = Color.White,
            modifier = Modifier.size(BADGE_GLYPH),
        )
    }
}

/**
 * The posting video: its cover, dimmed, with the ring in the middle and
 * the percent inside it while the bytes go up; a clock when it is
 * scheduled; the reason under a stopped one.
 */
@Composable
private fun PendingTile(tile: PendingVideoTile, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(UsTheme.radii.small)
    val failed = tile.failure != null
    Box(
        modifier = modifier
            .clip(shape)
            .background(UsTheme.extended.bgCard)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                enabled = failed,
                onClick = onClick,
            )
            .semantics {
                contentDescription = if (failed) {
                    "Couldn't post ${tile.title}. Tap for options"
                } else {
                    listOfNotNull(tile.state.ringLabel(), tile.scheduleLabel).joinToString(". ")
                }
                if (failed) role = Role.Button
            }
            .testTag("profile_pending:${tile.creationKey}"),
        contentAlignment = Alignment.Center,
    ) {
        tile.coverPath?.let { path ->
            AsyncImage(
                model = File(path),
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(Color.Black.copy(alpha = if (failed) FAILED_DIM else PENDING_DIM)),
        )
        if (tile.publishAt != null) ClockBadge(modifier = Modifier.align(Alignment.TopStart))
        if (failed) {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Icon(
                    imageVector = UsIcons.RotateCcw,
                    contentDescription = null,
                    tint = Color.White,
                    modifier = Modifier.size(FAILED_GLYPH),
                )
                Text(
                    text = "Couldn't post",
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.SemiBold,
                    color = Color.White,
                    modifier = Modifier.padding(top = UsTheme.spacing.xs),
                )
            }
        } else {
            PublishRingView(
                ring = tile.state.ring(),
                label = tile.state.ringPercentLabel(),
                modifier = Modifier.size(RING_SIZE),
            )
        }
    }
}

/**
 * The ember ring: an arc from 12 o'clock for the uploaded fraction, or a
 * short arc that turns while nothing measurable is happening — the Reels
 * avatar ring's drawing, on a dark plate so it reads over any cover. The
 * [label] ("42 %") sits inside the ring while there is one.
 */
@Composable
internal fun PublishRingView(ring: PublishRing, label: String?, modifier: Modifier = Modifier) {
    val played = UsTheme.extended.ctaGradient
    val track = Color.White.copy(alpha = TRACK_ALPHA)
    val spin = rememberInfiniteTransition(label = "publishSpin")
    val angle by spin.animateFloat(
        initialValue = 0f,
        targetValue = FULL_SWEEP,
        animationSpec = infiniteRepeatable(tween(SPIN_MILLIS, easing = LinearEasing), RepeatMode.Restart),
        label = "publishAngle",
    )
    val sweep = when (ring) {
        is PublishRing.Determinate -> FULL_SWEEP * ring.fraction
        PublishRing.Indeterminate -> INDETERMINATE_SWEEP
        PublishRing.None -> 0f
    }
    val rotation = if (ring is PublishRing.Indeterminate) angle else 0f
    Box(
        modifier = modifier
            .clip(CircleShape)
            .background(Color.Black.copy(alpha = PLATE_ALPHA))
            .drawBehind { rotate(rotation) { drawRing(track, played, sweep) } }
            .testTag("profile_pending_ring"),
        contentAlignment = Alignment.Center,
    ) {
        if (label != null) {
            Text(
                text = label,
                style = MaterialTheme.typography.labelSmall,
                fontSize = RING_LABEL_SIZE,
                fontWeight = FontWeight.Bold,
                color = Color.White,
                maxLines = 1,
                modifier = Modifier.testTag("profile_pending_percent"),
            )
        }
    }
}

private fun DrawScope.drawRing(track: Color, played: Brush, sweep: Float) {
    val stroke = RING_STROKE.toPx()
    val inset = stroke / 2 + RING_INSET.toPx()
    val arcSize = Size(size.width - inset * 2, size.height - inset * 2)
    drawArc(
        color = track,
        startAngle = START_ANGLE,
        sweepAngle = FULL_SWEEP,
        useCenter = false,
        topLeft = Offset(inset, inset),
        size = arcSize,
        style = Stroke(width = stroke),
    )
    if (sweep > 0f) {
        drawArc(
            brush = played,
            startAngle = START_ANGLE,
            sweepAngle = sweep,
            useCenter = false,
            topLeft = Offset(inset, inset),
            size = arcSize,
            style = Stroke(width = stroke, cap = StrokeCap.Round),
        )
    }
}

/** Why the post stopped, and what to do: Retry (or Create channel) and Discard. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun PublishFailureSheet(
    failure: ReelPublishState.Failed,
    onRetry: () -> Unit,
    onDiscard: () -> Unit,
    onCreateChannel: () -> Unit,
    onDismiss: () -> Unit,
) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
        modifier = Modifier.testTag("profile_pending_sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.xxl)
                .navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            Text(
                text = "Couldn't post your video",
                style = MaterialTheme.typography.titleLarge.copy(fontSize = SHEET_TITLE_SIZE),
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = failure.message,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
            Spacer(Modifier.height(UsTheme.spacing.s))
            when {
                failure.needsChannel -> UsSecondaryButton(
                    text = "Create channel",
                    onClick = onCreateChannel,
                    modifier = Modifier
                        .fillMaxWidth()
                        .testTag("profile_pending_create_channel"),
                )
                failure.retryable -> UsSecondaryButton(
                    text = "Retry",
                    onClick = onRetry,
                    modifier = Modifier
                        .fillMaxWidth()
                        .testTag("profile_pending_retry"),
                )
            }
            UsSecondaryButton(
                text = "Discard",
                onClick = onDiscard,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("profile_pending_discard"),
            )
        }
    }
}

private const val GRID_COLUMNS = 3
private const val SQUARE = 1f
private const val PORTRAIT_TILE = 3f / 4f
private const val LANDSCAPE = 16f / 9f
private const val TEXT_TILE_LINES = 4
private const val PENDING_DIM = 0.35f
private const val FAILED_DIM = 0.6f
private const val TRACK_ALPHA = 0.25f
private const val PLATE_ALPHA = 0.45f
private const val START_ANGLE = -90f
private const val FULL_SWEEP = 360f
private const val INDETERMINATE_SWEEP = 100f
private const val SPIN_MILLIS = 1_100
private const val SCRIM_ALPHA = 0.55f
private val HAIRLINE = 1.dp
private val GRID_GAP = 3.dp
private val TAB_HEIGHT = 34.dp
private val TAB_HORIZONTAL = 14.dp
private val STATE_HEIGHT = 180.dp
private val APPEND_HEIGHT = 64.dp
private val KIND_GLYPH = 16.dp
private val FAILED_GLYPH = 22.dp
private val SUCCESS_GLYPH = 16.dp
private val RING_SIZE = 48.dp
private val RING_STROKE = 3.dp
private val RING_INSET = 4.dp
private val RING_LABEL_SIZE = 10.sp
private val BADGE_SIZE = 20.dp
private val BADGE_GLYPH = 12.dp
private val SCHEDULE_LABEL_SIZE = 9.sp
private val SHEET_RADIUS = 28.dp
private val SHEET_TITLE_SIZE = 20.sp
