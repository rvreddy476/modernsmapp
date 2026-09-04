package com.us.android.feature.feed.ui.reels

import androidx.annotation.OptIn
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.sizeIn
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.pager.VerticalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
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
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.modifier.usMediaScrim
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.engagement.data.likeCountOr
import com.us.android.core.engagement.data.reactedOr
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerPool
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.formatCount
import com.us.android.core.ui.rememberPostSharer
import com.us.android.feature.feed.ui.comments.CommentsSheet
import java.io.File

/**
 * The reels surface: a full-screen vertical pager of short video, under two
 * labels — For You, Following — laid over the top of the video.
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
 *
 * A reel the viewer just posted sits ABOVE page 0 of either tab as the
 * [ReelsHead]: its cover under a round loader while it posts, the real reel
 * the moment it exists. No banner anywhere — the item IS the progress.
 */
@Composable
fun ReelsScreen(
    pool: PlayerPool,
    onOpenAuthor: (userId: String) -> Unit,
    viewModel: ReelsViewModel = hiltViewModel(),
) {
    val tab by viewModel.tab.collectAsStateWithLifecycle()
    val head by viewModel.head.collectAsStateWithLifecycle()
    val muted by viewModel.muted.collectAsStateWithLifecycle()
    val overlays by viewModel.overlays.collectAsStateWithLifecycle()
    val items = viewModel.items.collectAsLazyPagingItems()
    var commentsFor by rememberSaveable { mutableStateOf<String?>(null) }
    val share = rememberPostSharer()
    // One saved scroll position PER TAB: For You's page applied to Following
    // would drop the viewer mid-list in a feed they have not scrolled.
    val tabStates = rememberSaveableStateHolder()

    ReleaseOnLifecycle(pool)

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black),
    ) {
        tabStates.SaveableStateProvider(tab) {
            ReelsBody(
                items = items,
                head = head,
                pool = pool,
                muted = muted,
                overlays = overlays,
                playbackFor = viewModel::playback,
                onToggleMute = viewModel::toggleMuted,
                onOpenAuthor = onOpenAuthor,
                onReact = viewModel::onReact,
                onBookmark = viewModel::onBookmark,
                onComment = { commentsFor = it },
                onShare = { item ->
                    share(item.text, item.author.nameForDisplay)
                    viewModel.onExternalShared(item.id)
                },
                onRetryPublish = viewModel::retryPublish,
                onDiscardPublish = viewModel::discardPublish,
            )
        }
        ReelsTabsRow(
            selected = tab,
            onSelect = viewModel::selectTab,
            modifier = Modifier
                .align(Alignment.TopCenter)
                .statusBarsPadding(),
        )
    }

    // Comments open over the reel rather than navigating away: the reel keeps
    // playing behind the conversation about it.
    commentsFor?.let { postId ->
        CommentsSheet(postId = postId, onDismiss = { commentsFor = null })
    }
}

/**
 * "For You | Following" over the video, Instagram-style: two labels, the
 * active one white over a short white underline, the other muted. Both are
 * `Role.Tab` in one selectable group so TalkBack reads "For You, tab, 1 of
 * 2, selected" rather than two unrelated buttons.
 */
@Composable
private fun ReelsTabsRow(
    selected: ReelsTab,
    onSelect: (ReelsTab) -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .padding(top = UsTheme.spacing.m)
            .selectableGroup()
            .testTag("reels_tabs"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxxl),
    ) {
        ReelsTab.entries.forEach { tab ->
            val active = tab == selected
            // The column is as wide as its label, so the underline matches it:
            // a full-width bar under a short word reads as a border, not a
            // selection.
            Column(
                modifier = Modifier
                    .width(IntrinsicSize.Max)
                    // No ripple over video — the label's colour is the state.
                    .selectable(
                        selected = active,
                        role = Role.Tab,
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                        onClick = { onSelect(tab) },
                    )
                    .padding(horizontal = UsTheme.spacing.xs, vertical = UsTheme.spacing.s),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Text(
                    text = tab.label,
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = if (active) FontWeight.Bold else FontWeight.SemiBold,
                    color = if (active) Color.White else Color.White.copy(alpha = TAB_INACTIVE_ALPHA),
                    maxLines = 1,
                )
                // Always laid out, transparent when inactive, so the label does
                // not shift by 2dp when the selection moves.
                Box(
                    modifier = Modifier
                        .padding(top = TAB_UNDERLINE_GAP)
                        .width(TAB_UNDERLINE_WIDTH)
                        .height(TAB_UNDERLINE_HEIGHT)
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .background(if (active) Color.White else Color.Transparent),
                )
            }
        }
    }
}

/**
 * The loading / error / empty states, or the pager. A pending or live head
 * always shows the pager: the viewer's own reel is content even when the
 * ranked feed has nothing.
 */
@Suppress("LongParameterList")
@Composable
private fun ReelsBody(
    items: LazyPagingItems<FeedItem>,
    head: ReelsHead?,
    pool: PlayerPool,
    muted: Boolean,
    overlays: Map<String, EngagementOverlay>,
    playbackFor: (FeedItem) -> Playback?,
    onToggleMute: () -> Unit,
    onOpenAuthor: (String) -> Unit,
    onReact: (postId: String, serverReacted: Boolean) -> Unit,
    onBookmark: (postId: String, serverBookmarked: Boolean) -> Unit,
    onComment: (postId: String) -> Unit,
    onShare: (FeedItem) -> Unit,
    onRetryPublish: () -> Unit,
    onDiscardPublish: () -> Unit,
) {
    val refresh = items.loadState.refresh
    val empty = items.itemCount == 0 && head == null
    when {
        refresh is LoadState.Loading && empty -> UsLoadingState(label = "Loading reels")

        refresh is LoadState.Error && empty -> UsErrorState(
            message = "We couldn't load reels.",
            onRetry = items::retry,
        )

        refresh is LoadState.NotLoading && empty -> UsEmptyState(
            title = "No reels yet",
            detail = "Short videos from people you follow will show up here.",
        )

        else -> ReelsPager(
            items = items,
            head = head,
            pool = pool,
            muted = muted,
            overlays = overlays,
            playbackFor = playbackFor,
            onToggleMute = onToggleMute,
            onOpenAuthor = onOpenAuthor,
            onReact = onReact,
            onBookmark = onBookmark,
            onComment = onComment,
            onShare = onShare,
            onRetryPublish = onRetryPublish,
            onDiscardPublish = onDiscardPublish,
        )
    }
}

@Suppress("LongParameterList")
@Composable
private fun ReelsPager(
    items: LazyPagingItems<FeedItem>,
    head: ReelsHead?,
    pool: PlayerPool,
    muted: Boolean,
    overlays: Map<String, EngagementOverlay>,
    playbackFor: (FeedItem) -> Playback?,
    onToggleMute: () -> Unit,
    onOpenAuthor: (String) -> Unit,
    onReact: (postId: String, serverReacted: Boolean) -> Unit,
    onBookmark: (postId: String, serverBookmarked: Boolean) -> Unit,
    onComment: (postId: String) -> Unit,
    onShare: (FeedItem) -> Unit,
    onRetryPublish: () -> Unit,
    onDiscardPublish: () -> Unit,
) {
    val pageCount = items.itemCount + if (head != null) 1 else 0
    val pagerState = rememberPagerState(pageCount = { pageCount })

    // peek, not get: a neighbour lookup must not trigger a page load.
    fun reelAt(page: Int): FeedItem? = (pageAt(page, head, items, load = false) as? ReelsPage.Reel)?.item

    // Keyed on the SETTLED page rather than the scroll offset: a fast flick
    // through five reels must start one playback, not five.
    LaunchedEffect(pagerState.settledPage, pageCount, head) {
        val current = pagerState.settledPage
        reelAt(current)?.let(playbackFor)?.let { pool.acquire(current, it) }
        pool.playOnly(current)
        listOf(current - 1, current + 1).forEach { index ->
            reelAt(index)?.let(playbackFor)?.let { pool.preload(index, it) }
        }
    }

    VerticalPager(
        state = pagerState,
        beyondViewportPageCount = 1,
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black),
    ) { page ->
        when (val content = pageAt(page, head, items, load = true)) {
            null -> Unit
            is ReelsPage.Pending ->
                PendingReelPage(head = content.head, onRetry = onRetryPublish, onDiscard = onDiscardPublish)

            is ReelsPage.Reel -> {
                val item = content.item
                ReelPage(
                    item = item,
                    playback = playbackFor(item),
                    overlay = overlays[item.id] ?: EngagementOverlay(),
                    pool = pool,
                    page = page,
                    muted = muted,
                    onToggleMute = onToggleMute,
                    onOpenAuthor = onOpenAuthor,
                    onReact = { onReact(item.id, item.viewer.hasReacted) },
                    onBookmark = { onBookmark(item.id, item.viewer.isBookmarked) },
                    onComment = { onComment(item.id) },
                    onShare = { onShare(item) },
                )
            }
        }
    }
}

/** What one page of the pager holds: the pending head, or a reel to play. */
private sealed interface ReelsPage {
    data class Pending(val head: ReelsHead.Pending) : ReelsPage
    data class Reel(val item: FeedItem) : ReelsPage
}

/**
 * The head takes page 0 when present and every ranked reel shifts down one.
 * The pool keys players by page and re-prepares when the playback at a page
 * changes, so the shift cannot leave the wrong video on a page.
 *
 * [load] decides between `items[i]`, which asks Paging for the next page
 * when the index nears the end, and `peek`, which never does.
 */
private fun pageAt(page: Int, head: ReelsHead?, items: LazyPagingItems<FeedItem>, load: Boolean): ReelsPage? {
    val offset = if (head != null) 1 else 0
    if (page < 0 || page >= offset + items.itemCount) return null
    if (head != null && page == 0) {
        return when (head) {
            is ReelsHead.Pending -> ReelsPage.Pending(head)
            is ReelsHead.Live -> ReelsPage.Reel(head.item)
        }
    }
    val index = page - offset
    val item = if (load) items[index] else items.peek(index)
    return item?.let { ReelsPage.Reel(it) }
}

/**
 * The viewer's own reel while it posts: the chosen cover, full-bleed, under a
 * ROUND indeterminate loader — no percentage, because the number the user
 * cares about is not "how many bytes" but "is it there yet", and the item
 * turning into the real reel answers that. The caption sits where the reel's
 * caption will.
 *
 * A stopped publish swaps the loader for one small strip: "Couldn't post ·
 * Retry · Discard". Nothing else on the page changes, so the failure reads as
 * a state of THIS reel, not of the screen.
 */
@Composable
private fun PendingReelPage(
    head: ReelsHead.Pending,
    onRetry: () -> Unit,
    onDiscard: () -> Unit,
) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(UsTheme.extended.bgCanvas)
            .testTag("reel_pending"),
    ) {
        if (head.coverPath != null) {
            AsyncImage(
                model = File(head.coverPath),
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
        if (head.failure == null) {
            CircularProgressIndicator(
                color = Color.White,
                trackColor = Color.White.copy(alpha = LOADER_TRACK_ALPHA),
                strokeWidth = LOADER_STROKE,
                modifier = Modifier
                    .align(Alignment.Center)
                    .size(LOADER_SIZE)
                    .semantics { contentDescription = "Posting your reel" },
            )
        }
        Column(
            modifier = Modifier
                .align(Alignment.BottomStart)
                .fillMaxWidth()
                .usMediaScrim()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(top = REEL_SCRIM_RAMP, bottom = UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            head.failure?.let { failure ->
                PublishFailureStrip(failure = failure, onRetry = onRetry, onDiscard = onDiscard)
            }
            if (head.caption.isNotBlank()) {
                Text(
                    text = head.caption,
                    style = MaterialTheme.typography.bodyMedium,
                    color = Color.White,
                    maxLines = CAPTION_MAX_LINES,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

@Composable
private fun PublishFailureStrip(
    failure: PendingFailure,
    onRetry: () -> Unit,
    onDiscard: () -> Unit,
) {
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .background(Color.Black.copy(alpha = RAIL_PLATE_ALPHA))
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .testTag("reel_pending_failure"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = "Couldn't post",
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = Color.White,
            modifier = Modifier.semantics { contentDescription = "Couldn't post. ${failure.message}" },
        )
        if (failure.retryable) {
            StripDot()
            StripAction(label = "Retry", onClick = onRetry)
        }
        StripDot()
        StripAction(label = "Discard", onClick = onDiscard)
    }
}

@Composable
private fun StripDot() {
    Text(
        text = "·",
        style = MaterialTheme.typography.labelLarge,
        color = Color.White.copy(alpha = TAB_INACTIVE_ALPHA),
    )
}

@Composable
private fun StripAction(label: String, onClick: () -> Unit) {
    Text(
        text = label,
        style = MaterialTheme.typography.labelLarge,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.accentSolid,
        modifier = Modifier
            .pressScale(onClick)
            .padding(vertical = UsTheme.spacing.xs)
            .semantics { role = Role.Button },
    )
}

@OptIn(UnstableApi::class)
@Suppress("LongParameterList")
@Composable
private fun ReelPage(
    item: FeedItem,
    playback: Playback?,
    overlay: EngagementOverlay,
    pool: PlayerPool,
    page: Int,
    muted: Boolean,
    onToggleMute: () -> Unit,
    onOpenAuthor: (String) -> Unit,
    onReact: () -> Unit,
    onBookmark: () -> Unit,
    onComment: () -> Unit,
    onShare: () -> Unit,
) {
    Box(modifier = Modifier.fillMaxSize()) {
        if (playback != null) {
            val player = remember(page, playback) { pool.acquire(page, playback) }
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
            overlay = overlay,
            muted = muted,
            onToggleMute = onToggleMute,
            onReact = onReact,
            onBookmark = onBookmark,
            onComment = onComment,
            onShare = onShare,
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
 *
 * Comment and share follow the author's switches — see [railVisibility].
 */
@Suppress("LongParameterList")
@Composable
private fun ReelActionRail(
    item: FeedItem,
    overlay: EngagementOverlay,
    muted: Boolean,
    onToggleMute: () -> Unit,
    onReact: () -> Unit,
    onBookmark: () -> Unit,
    onComment: () -> Unit,
    onShare: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val rail = item.controls.railVisibility()
    val reacted = overlay.reactedOr(item.viewer.hasReacted)
    val bookmarked = overlay.bookmarkedOr(item.viewer.isBookmarked)
    Column(
        modifier = modifier
            .padding(end = UsTheme.spacing.l, bottom = RAIL_BOTTOM_INSET)
            .testTag("reel_rail"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        RailButton(
            icon = if (reacted) UsIcons.HeartFilled else UsIcons.HeartOutline,
            description = if (reacted) "Liked" else "Like",
            count = overlay.likeCountOr(item.counts.likes, item.viewer.hasReacted),
            tint = if (reacted) UsTheme.extended.liveRed else Color.White,
            onClick = onReact,
        )
        if (rail.showComment) {
            RailButton(
                icon = UsIcons.Comment,
                description = "Comments",
                count = item.counts.comments,
                onClick = onComment,
            )
        }
        if (rail.showShare) {
            RailButton(
                icon = UsIcons.Share,
                description = "Share",
                count = null,
                onClick = onShare,
            )
        }
        RailButton(
            icon = if (bookmarked) UsIcons.BookmarkFilled else UsIcons.BookmarkOutline,
            description = if (bookmarked) "Saved" else "Save",
            count = null,
            tint = if (bookmarked) UsTheme.extended.statusWarning else Color.White,
            onClick = onBookmark,
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
            .pressScale(onClick)
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
 * No ripple. Over video the default indication lit a box around each glyph;
 * the press is shown instead by the control dipping to 85% on a spring and
 * springing back — the same gesture as the feed card's action row.
 */
@Composable
private fun Modifier.pressScale(onClick: () -> Unit): Modifier {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(
        targetValue = if (pressed) PRESS_SCALE else 1f,
        animationSpec = spring(dampingRatio = Spring.DampingRatioMediumBouncy, stiffness = PRESS_STIFFNESS),
        label = "railPress",
    )
    return this
        .clickable(interactionSource = interaction, indication = null, onClick = onClick)
        .graphicsLayer {
            scaleX = scale
            scaleY = scale
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
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                ) { onOpenAuthor(item.author.id) }
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

private const val PRESS_SCALE = 0.85f
private const val PRESS_STIFFNESS = 1200f

/** The inactive tab label over video: legible, clearly not the one selected. */
private const val TAB_INACTIVE_ALPHA = 0.6f
private val TAB_UNDERLINE_WIDTH = 24.dp
private val TAB_UNDERLINE_HEIGHT = 2.dp
private val TAB_UNDERLINE_GAP = 6.dp

/** The pending item's round loader: a 48dp ring, no number in it. */
private val LOADER_SIZE = 48.dp
private val LOADER_STROKE = 3.dp
private const val LOADER_TRACK_ALPHA = 0.25f
