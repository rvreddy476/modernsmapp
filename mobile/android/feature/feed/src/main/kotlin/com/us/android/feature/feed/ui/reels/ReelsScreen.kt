package com.us.android.feature.feed.ui.reels

import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.sizeIn
import androidx.compose.foundation.pager.VerticalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.compose.PlayerSurface
import androidx.media3.ui.compose.SURFACE_TYPE_SURFACE_VIEW
import androidx.paging.LoadState
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.modifier.usMediaScrim
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.PlayerPool
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.formatCount
import com.us.android.feature.feed.ui.ReelPublishBanner

/**
 * The reels surface: a full-screen vertical pager of short video.
 *
 * This is the screen the native migration was justified by, and its behaviour
 * is deliberately narrow:
 *
 *  - Exactly ONE player is playing at any moment. [PlayerPool.playOnly] pauses
 *    every other instance, so a swipe can never leave two audio tracks running.
 *  - The immediate neighbours are PREPARED but not played, which is what makes
 *    a swipe show a first frame instead of a spinner.
 *  - `beyondViewportPageCount = 1` keeps exactly those neighbours composed.
 *    Raising it composes pages the user has not reached and spends their data.
 *  - Playback stops when the surface leaves the foreground, and the pool is
 *    released when the screen is destroyed. A leaked ExoPlayer holds a decoder
 *    session and audio focus, which can stop the NEXT video playing at all.
 */
@Composable
fun ReelsScreen(
    pool: PlayerPool,
    onOpenAuthor: (userId: String) -> Unit,
    /** The publish banner's "View": `:app` opens the new reel's post. */
    onOpenPost: (postId: String) -> Unit,
    viewModel: ReelsViewModel = hiltViewModel(),
) {
    val items = viewModel.items.collectAsLazyPagingItems()
    val muted by viewModel.muted.collectAsStateWithLifecycle()
    val refresh = items.loadState.refresh

    Box(modifier = Modifier.fillMaxSize()) {
        when {
            refresh is LoadState.Loading && items.itemCount == 0 ->
                UsLoadingState(label = "Loading reels")

            refresh is LoadState.Error && items.itemCount == 0 -> UsErrorState(
                message = "We couldn't load reels.",
                onRetry = items::retry,
            )

            refresh is LoadState.NotLoading && items.itemCount == 0 -> UsEmptyState(
                title = "No reels yet",
                detail = "Short videos from people you follow will show up here.",
            )

            else -> ReelsPager(
                items = items,
                pool = pool,
                muted = muted,
                urlFor = viewModel::playbackUrl,
                onToggleMute = viewModel::toggleMuted,
                onOpenAuthor = onOpenAuthor,
            )
        }
        // The same strip the home feed pins under its tabs, laid over the
        // top of the pager: a reel posted from here should report here.
        ReelPublishBanner(onOpenPost = onOpenPost, modifier = Modifier.align(Alignment.TopCenter))
    }
}

@Composable
private fun ReelsPager(
    items: LazyPagingItems<FeedItem>,
    pool: PlayerPool,
    muted: Boolean,
    urlFor: (FeedItem) -> String?,
    onToggleMute: () -> Unit,
    onOpenAuthor: (String) -> Unit,
) {
    val pagerState = rememberPagerState(pageCount = { items.itemCount })

    // Keyed on the SETTLED page rather than the scroll offset: a fast flick
    // through five reels must start one playback, not five.
    LaunchedEffect(pagerState.settledPage, items.itemCount) {
        val current = pagerState.settledPage
        items.peek(current)?.let { item -> urlFor(item)?.let { pool.acquire(current, it) } }
        pool.playOnly(current)
        listOf(current - 1, current + 1)
            .filter { it in 0 until items.itemCount }
            .forEach { index ->
                items.peek(index)?.let { item -> urlFor(item)?.let { pool.preload(index, it) } }
            }
    }

    ReleaseOnLifecycle(pool)

    VerticalPager(
        state = pagerState,
        beyondViewportPageCount = 1,
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black),
    ) { page ->
        val item = items[page] ?: return@VerticalPager
        ReelPage(
            item = item,
            url = urlFor(item),
            pool = pool,
            page = page,
            muted = muted,
            onToggleMute = onToggleMute,
            onOpenAuthor = onOpenAuthor,
        )
    }
}

@OptIn(UnstableApi::class)
@Composable
private fun ReelPage(
    item: FeedItem,
    url: String?,
    pool: PlayerPool,
    page: Int,
    muted: Boolean,
    onToggleMute: () -> Unit,
    onOpenAuthor: (String) -> Unit,
) {
    Box(modifier = Modifier.fillMaxSize()) {
        if (url != null) {
            val player = remember(page, url) { pool.acquire(page, url) }
            LaunchedEffect(muted, player) { player.volume = if (muted) 0f else 1f }
            // SURFACE_VIEW, not TEXTURE_VIEW. A TextureView goes through the
            // view hierarchy's compositor, costing a full-screen copy every
            // frame; the difference is visible on mid-range hardware and this
            // is the surface the whole native migration was justified by.
            PlayerSurface(
                player = player,
                surfaceType = SURFACE_TYPE_SURFACE_VIEW,
                modifier = Modifier.fillMaxSize(),
            )
        } else {
            // No playable rendition yet. An asset still processing has no
            // hls_url, so this is an expected state rather than a failure.
            UsEmptyState(
                title = "Still processing",
                detail = "This video isn't ready to play yet.",
                modifier = Modifier.fillMaxSize(),
            )
        }

        ReelOverlay(
            item = item,
            onOpenAuthor = onOpenAuthor,
            modifier = Modifier.align(Alignment.BottomStart),
        )

        // The rail sits on the right edge, clear of the caption, because that
        // is where a thumb rests while the other hand holds nothing. Putting
        // controls under the caption means reaching across the video to use
        // them.
        ReelActionRail(
            item = item,
            muted = muted,
            onToggleMute = onToggleMute,
            modifier = Modifier.align(Alignment.BottomEnd),
        )
    }
}

/**
 * The vertical control strip over a reel.
 *
 * Icons only, each on a translucent dark disc. The first attempt used
 * `Modifier.shadow` on the glyph instead, on the theory that a plate would
 * fight the caption scrim; on a device that drew a hard elevation disc behind
 * the icon that was more obtrusive than any plate. A soft circle is both more
 * legible over a white frame and quieter.
 */
@Composable
private fun ReelActionRail(
    item: FeedItem,
    muted: Boolean,
    onToggleMute: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier.padding(
            end = UsTheme.spacing.l,
            bottom = RAIL_BOTTOM_INSET,
        ),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        RailButton(
            icon = if (item.viewer.hasReacted) UsIcons.HeartFilled else UsIcons.HeartOutline,
            description = if (item.viewer.hasReacted) "Liked" else "Like",
            count = item.counts.likes,
            tint = if (item.viewer.hasReacted) UsTheme.extended.liveRed else Color.White,
            onClick = { },
        )
        RailButton(
            icon = UsIcons.Comment,
            description = "Comments",
            count = item.counts.comments,
            onClick = { },
        )
        RailButton(
            icon = UsIcons.Share,
            description = "Share",
            count = null,
            onClick = { },
        )
        RailButton(
            icon = if (muted) UsIcons.SoundOff else UsIcons.SoundOn,
            description = if (muted) "Unmute" else "Mute",
            count = null,
            onClick = onToggleMute,
        )
    }
}

@Composable
private fun RailButton(
    icon: ImageVector,
    description: String,
    count: Int?,
    onClick: () -> Unit,
    tint: Color = Color.White,
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = Modifier
            .clip(CircleShape)
            .clickable(onClick = onClick)
            .sizeIn(minWidth = RAIL_TARGET, minHeight = RAIL_TARGET)
            .padding(vertical = UsTheme.spacing.s)
            .clearAndSetSemantics {
                contentDescription = if (count != null) "$description, $count" else description
                role = Role.Button
            },
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = tint,
            modifier = Modifier
                // The disc, not the glyph, carries the contrast. A bare white
                // icon vanishes against a white frame no matter its weight.
                .background(Color.Black.copy(alpha = RAIL_PLATE_ALPHA), CircleShape)
                .padding(UsTheme.spacing.m)
                .size(RAIL_ICON),
        )
        if (count != null && count > 0) {
            Text(
                text = formatCount(count),
                style = MaterialTheme.typography.labelSmall,
                color = Color.White,
            )
        }
    }
}

/**
 * Author and caption, bottom-left.
 *
 * Holds no controls. Everything actionable moved to [ReelActionRail]; text and
 * targets interleaved in one column made the caption look tappable and the
 * buttons look like part of the sentence.
 */
@Composable
private fun ReelOverlay(
    item: FeedItem,
    onOpenAuthor: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            // The scrim goes on the container, not the text, so it also covers
            // the padding — a caption whose descenders fall outside the dark
            // area is exactly as unreadable as one with no scrim at all.
            .usMediaScrim()
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            // Extra top room gives the gradient somewhere to ramp, so the
            // scrim fades in instead of starting at a visible line.
            .padding(top = REEL_SCRIM_RAMP, bottom = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        // The name is the way to the author's profile. A separate "View
        // author" button restated what tapping a name already means on every
        // social surface ever shipped.
        Text(
            text = item.author.nameForDisplay,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = Color.White,
            modifier = Modifier
                .clip(RoundedCornerShape(UsTheme.radii.small))
                .clickable { onOpenAuthor(item.author.id) }
                .semantics { role = Role.Button },
        )
        if (item.text.isNotBlank()) {
            Text(
                text = item.text,
                style = MaterialTheme.typography.bodyMedium,
                color = Color.White,
                maxLines = CAPTION_MAX_LINES,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/**
 * Pauses on background and releases on destroy.
 *
 * Both halves matter. Without the pause, audio keeps playing over whatever the
 * user switched to. Without the release, every visit to this screen leaks four
 * decoder sessions, and the device exhausts them long before the process ends.
 */
@Composable
private fun ReleaseOnLifecycle(pool: PlayerPool) {
    val owner = LocalLifecycleOwner.current
    DisposableEffect(owner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) pool.pauseAll()
        }
        owner.lifecycle.addObserver(observer)
        onDispose {
            owner.lifecycle.removeObserver(observer)
            pool.release()
        }
    }
}

private const val CAPTION_MAX_LINES = 3

/**
 * How far above the caption the scrim starts fading in.
 *
 * Not a spacing token: this is the length of a gradient ramp, not a gap between
 * elements, and tying it to a layout token would make it change for reasons
 * that have nothing to do with legibility.
 */
private val REEL_SCRIM_RAMP = 72.dp

/**
 * Keeps the rail clear of the caption block below it.
 *
 * Larger than it looks it needs to be: the caption can run to three lines, and
 * a rail that overlaps the last line of someone's text is worse than one
 * sitting slightly high on a short caption.
 */
private val RAIL_BOTTOM_INSET = 132.dp

/** Comfortably past the 48dp minimum — this is a one-thumb surface. */
private val RAIL_TARGET = 56.dp

private val RAIL_ICON = 26.dp

/**
 * Dark enough to read as neutral over ANY frame.
 *
 * Started at 0.32 and it was too weak: over a yellow frame the disc tinted
 * olive and looked like a rendering artefact rather than a control. The plate
 * has to dominate the pixels behind it or it should not be there.
 */
private const val RAIL_PLATE_ALPHA = 0.55f
