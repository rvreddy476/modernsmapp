package com.us.android.feature.feed.ui.reels

import androidx.annotation.OptIn
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.sizeIn
import androidx.compose.foundation.pager.VerticalPager
import androidx.compose.foundation.pager.rememberPagerState
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
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
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
import androidx.compose.ui.unit.sp
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
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.engagement.data.likeCountOr
import com.us.android.core.engagement.data.reactedOr
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerPool
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.HideShellBottomBar
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.formatCount
import com.us.android.core.ui.rememberPostSharer
import com.us.android.feature.feed.data.offersFollow
import com.us.android.feature.feed.ui.MomentumHeader
import com.us.android.feature.feed.ui.comments.CommentsSheet
import com.us.android.feature.feed.ui.more.PostMoreSheetHost
import com.us.android.feature.feed.ui.more.PostMoreViewModel
import java.io.File

/**
 * The reels surface: Instagram Reels on Momentum's palette (founder,
 * 2026-09-04). A full-screen vertical pager of short video that fills the
 * frame from the very top (the shell hands this tab no status-bar inset);
 * the Momentum header — wordmark, search, messages, the bell — translucent
 * over the top of the video on its own scrim; the right rail — like,
 * comment, share, save, more, mute — bottom-right; the author, Follow and
 * the caption bottom-left over a bottom scrim. No For You / Following tabs:
 * Reels is one surface.
 *
 * Two gestures on the video (founder, 2026-09-04, from the phone):
 *
 *  - A SINGLE tap pauses the reel, and a second one plays it again; a
 *    centred play glyph marks the held frame. Mute stays on the rail.
 *  - A DOUBLE-tap is full mode: it hides ONLY the header and the app's
 *    bottom bar — the rail and the author block stay, they belong to the
 *    reel — and a second double-tap brings them back. The rule is
 *    [ReelsMode.chrome]; the bar is hidden through the shell
 *    ([HideShellBottomBar]) because only the shell owns it. A double-tap
 *    never likes the reel — the rail's heart is the one way to do that.
 *
 * Reels OPENS in normal mode, playing, every time: both are reset when the
 * screen is left.
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
 * A reel the viewer just posted sits ABOVE page 0 as the [ReelsHead]: its
 * cover under a round loader while it posts, the real reel the moment it
 * exists. No banner anywhere — the item IS the progress.
 *
 * The rail's ⋮ opens the same "more" sheet the feed card opens
 * ([PostMoreSheetHost]), driven by [more]; what it leaves behind ("We'll
 * show you fewer posts like this") is shown over the reel once it has gone.
 */
@Suppress("LongParameterList")
@Composable
fun ReelsScreen(
    pool: PlayerPool,
    onOpenAuthor: (userId: String) -> Unit,
    /** The header's three controls. Required: a header glyph that does nothing must not ship again. */
    onOpenSearch: () -> Unit,
    onOpenMessages: () -> Unit,
    onOpenNotifications: () -> Unit,
    viewModel: ReelsViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
) {
    val head by viewModel.head.collectAsStateWithLifecycle()
    val muted by viewModel.muted.collectAsStateWithLifecycle()
    val paused by viewModel.paused.collectAsStateWithLifecycle()
    val mode by viewModel.mode.collectAsStateWithLifecycle()
    val chrome = mode.chrome()
    val overlays by viewModel.overlays.collectAsStateWithLifecycle()
    val followEdges by viewModel.followEdges.collectAsStateWithLifecycle()
    val moreMessage by more.message.collectAsStateWithLifecycle()
    val items = viewModel.items.collectAsLazyPagingItems()
    var commentsFor by rememberSaveable { mutableStateOf<String?>(null) }
    var moreFor by remember { mutableStateOf<FeedItem?>(null) }
    val share = rememberPostSharer()
    // Recorded only AFTER the chooser was launched, and shared by the rail's
    // glyph and the more sheet's row so the count cannot be taken twice.
    val onShare: (FeedItem) -> Unit = { item ->
        share(item.text, item.author.nameForDisplay)
        viewModel.onExternalShared(item.id)
    }

    ReleaseOnLifecycle(pool)

    // The shell's bar follows the mode, and everything is given back when
    // the screen is left: the bar by HideShellBottomBar's own dispose, the
    // mode and the pause by the reset here — so a tab switch out of full
    // mode lands on a Home with its bar, and the next visit to Reels opens
    // in normal mode with a moving reel.
    HideShellBottomBar(hidden = !chrome.showBottomBar)
    DisposableEffect(viewModel) {
        onDispose { viewModel.resetView() }
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(UsTheme.extended.bgCanvas),
    ) {
        ReelsBody(
            items = items,
            head = head,
            pool = pool,
            muted = muted,
            paused = paused,
            chrome = chrome,
            overlays = overlays,
            followEdges = followEdges,
            ownUserId = viewModel.ownUserId,
            playbackFor = viewModel::playback,
            actions = ReelActions(
                onToggleMute = viewModel::toggleMuted,
                onTogglePause = viewModel::togglePaused,
                onToggleMode = viewModel::toggleMode,
                onOpenAuthor = onOpenAuthor,
                onReact = viewModel::onReact,
                onBookmark = viewModel::onBookmark,
                onComment = { commentsFor = it },
                onShare = onShare,
                onFollow = viewModel::onFollow,
                onMore = { moreFor = it },
                onShown = viewModel::onReelShown,
                onRetryPublish = viewModel::retryPublish,
                onDiscardPublish = viewModel::discardPublish,
            ),
        )

        ScreenChrome(
            paused = paused,
            showHeader = chrome.showHeader,
            onOpenSearch = onOpenSearch,
            onOpenMessages = onOpenMessages,
            onOpenNotifications = onOpenNotifications,
        )
        UsMessageHost(message = moreMessage, onDismiss = more::dismissMessage)
    }

    // Comments open over the reel rather than navigating away: the reel keeps
    // playing behind the conversation about it.
    commentsFor?.let { postId ->
        CommentsSheet(postId = postId, onDismiss = { commentsFor = null })
    }

    // The same more sheet the feed card opens, over the playing reel.
    moreFor?.let { item ->
        PostMoreSheetHost(
            item = item,
            overlay = overlays[item.id] ?: EngagementOverlay(),
            followEdge = followEdges[item.author.id],
            ownUserId = viewModel.ownUserId,
            onShare = onShare,
            onDismiss = { moreFor = null },
            viewModel = more,
        )
    }
}

/**
 * What the SCREEN draws over the pager, as opposed to what a page draws over
 * its own video: the paused mark and the Momentum header.
 *
 * The paused mark is a play glyph, centred, only while paused. It sits above
 * the pager and below the header so it never covers a control, and it is not
 * itself tappable — the video under it is.
 *
 * The header — wordmark, search (scoped to reels by the host), messages, the
 * bell — rides its own top scrim and pads itself under the status bar the
 * shell left uncovered. It leaves upward in full mode, the same 200ms as the
 * bar leaves down.
 */
@Composable
private fun BoxScope.ScreenChrome(
    paused: Boolean,
    showHeader: Boolean,
    onOpenSearch: () -> Unit,
    onOpenMessages: () -> Unit,
    onOpenNotifications: () -> Unit,
) {
    AnimatedVisibility(
        visible = paused,
        modifier = Modifier.align(Alignment.Center),
        enter = fadeIn(tween(CHROME_ANIM_MILLIS)) +
            scaleIn(tween(CHROME_ANIM_MILLIS), initialScale = PAUSE_GLYPH_FROM),
        exit = fadeOut(tween(CHROME_ANIM_MILLIS)),
    ) {
        PausedGlyph()
    }
    AnimatedVisibility(
        visible = showHeader,
        modifier = Modifier.align(Alignment.TopCenter),
        enter = fadeIn(tween(CHROME_ANIM_MILLIS)) + slideInVertically(tween(CHROME_ANIM_MILLIS)) { -it / 2 },
        exit = fadeOut(tween(CHROME_ANIM_MILLIS)) + slideOutVertically(tween(CHROME_ANIM_MILLIS)) { -it / 2 },
    ) {
        MomentumHeader(
            onOpenSearch = onOpenSearch,
            onOpenMessages = onOpenMessages,
            onOpenNotifications = onOpenNotifications,
            translucent = true,
            modifier = Modifier.testTag("reels_header"),
        )
    }
}

/**
 * Every per-reel callback, hoisted once. A class rather than flat lambdas
 * through three layers of pager: the bundle is built once per screen and
 * its identity is stable, so it is not what recomposes a page.
 */
// One parameter per reel action: the bundle IS the parameter list.
@Suppress("LongParameterList")
internal class ReelActions(
    val onToggleMute: () -> Unit,
    /** A single tap on the video: pause ↔ play. */
    val onTogglePause: () -> Unit,
    /** A double-tap on the video: normal ↔ full mode. Never a like. */
    val onToggleMode: () -> Unit,
    val onOpenAuthor: (String) -> Unit,
    val onReact: (postId: String, serverReacted: Boolean) -> Unit,
    val onBookmark: (postId: String, serverBookmarked: Boolean) -> Unit,
    val onComment: (postId: String) -> Unit,
    val onShare: (FeedItem) -> Unit,
    val onFollow: (authorId: String) -> Unit,
    val onMore: ((FeedItem) -> Unit)?,
    /** The pager settled on this reel. */
    val onShown: (FeedItem) -> Unit,
    val onRetryPublish: () -> Unit,
    val onDiscardPublish: () -> Unit,
)

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
    paused: Boolean,
    chrome: ReelsChrome,
    overlays: Map<String, EngagementOverlay>,
    followEdges: Map<String, FollowStatus>,
    ownUserId: String,
    playbackFor: (FeedItem) -> Playback?,
    actions: ReelActions,
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
            paused = paused,
            chrome = chrome,
            overlays = overlays,
            followEdges = followEdges,
            ownUserId = ownUserId,
            playbackFor = playbackFor,
            actions = actions,
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
    paused: Boolean,
    chrome: ReelsChrome,
    overlays: Map<String, EngagementOverlay>,
    followEdges: Map<String, FollowStatus>,
    ownUserId: String,
    playbackFor: (FeedItem) -> Playback?,
    actions: ReelActions,
) {
    val pageCount = items.itemCount + if (head != null) 1 else 0
    val pagerState = rememberPagerState(pageCount = { pageCount })

    // peek, not get: a neighbour lookup must not trigger a page load.
    fun reelAt(page: Int): FeedItem? = (pageAt(page, head, items, load = false) as? ReelsPage.Reel)?.item

    // Keyed on the SETTLED page rather than the scroll offset: a fast flick
    // through five reels must start one playback, not five.
    LaunchedEffect(pagerState.settledPage, pageCount, head) {
        val current = pagerState.settledPage
        reelAt(current)?.let { reel ->
            playbackFor(reel)?.let { pool.acquire(current, it) }
            actions.onShown(reel)
        }
        pool.playOnly(current)
        listOf(current - 1, current + 1).forEach { index ->
            reelAt(index)?.let(playbackFor)?.let { pool.preload(index, it) }
        }
    }

    // The single-tap pause, applied to whichever page is settled. A separate
    // effect from the one above, keyed on the pause alone: folding it in
    // would re-run onShown on every tap, and onShown is what CLEARS a pause
    // when the pager moves on.
    LaunchedEffect(paused) {
        if (paused) pool.pauseAll() else pool.playOnly(pagerState.settledPage)
    }

    VerticalPager(
        state = pagerState,
        beyondViewportPageCount = 1,
        modifier = Modifier
            .fillMaxSize()
            .background(UsTheme.extended.bgCanvas),
    ) { page ->
        when (val content = pageAt(page, head, items, load = true)) {
            null -> Unit
            is ReelsPage.Pending ->
                PendingReelPage(
                    head = content.head,
                    onRetry = actions.onRetryPublish,
                    onDiscard = actions.onDiscardPublish,
                    onToggleMode = actions.onToggleMode,
                )

            is ReelsPage.Reel -> {
                val item = content.item
                ReelPage(
                    item = item,
                    playback = playbackFor(item),
                    overlay = overlays[item.id] ?: EngagementOverlay(),
                    offersFollow = offersFollow(ownUserId, item.author.id, followEdges[item.author.id]),
                    pool = pool,
                    page = page,
                    muted = muted,
                    chrome = chrome,
                    actions = actions,
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
    onToggleMode: () -> Unit,
) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(UsTheme.extended.bgCanvas)
            // The same double-tap as a real reel, so full mode is one gesture
            // wherever the pager is; the cover has no rail to hide.
            .pointerInput(onToggleMode) { detectTapGestures(onDoubleTap = { onToggleMode() }) }
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
        BottomScrim(modifier = Modifier.align(Alignment.BottomCenter))
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
                .padding(horizontal = OVERLAY_SIDE)
                .padding(bottom = OVERLAY_BOTTOM),
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
                    maxLines = CAPTION_LINES,
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
            .background(Color.Black.copy(alpha = STRIP_PLATE_ALPHA))
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
        color = Color.White.copy(alpha = DIM_ALPHA),
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
    offersFollow: Boolean,
    pool: PlayerPool,
    page: Int,
    muted: Boolean,
    chrome: ReelsChrome,
    actions: ReelActions,
) {
    Box(
        modifier = Modifier
            .fillMaxSize()
            // Both taps live on the PAGE, under the rail and the author
            // block, so a tap that lands on a control is that control's and
            // never a pause or a mode change. A single tap waits out the
            // double-tap window before it pauses — the same beat Instagram
            // has — so a double-tap is one mode change, not a pause and a
            // mode change.
            .pointerInput(actions) {
                detectTapGestures(
                    onTap = { actions.onTogglePause() },
                    onDoubleTap = { actions.onToggleMode() },
                )
            },
    ) {
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

        // The bottom 40% of the page darkens under BOTH the caption and the
        // rail. The scrim goes on the page, not the text, so it also covers
        // the padding — a caption whose descenders fall outside the dark area
        // is exactly as unreadable as one with no scrim at all. It follows
        // the blocks it serves: on while either is on, which under today's
        // rule is always — full mode keeps both.
        AnimatedVisibility(
            visible = chrome.showAuthor || chrome.showRail,
            modifier = Modifier.align(Alignment.BottomCenter),
            enter = fadeIn(tween(CHROME_ANIM_MILLIS)),
            exit = fadeOut(tween(CHROME_ANIM_MILLIS)),
        ) {
            BottomScrim()
        }

        // Each block leaves towards its own edge — the author block down,
        // the rail right — and fades on the way, ~200ms, the same as the
        // shell's bar below them, so the three read as one gesture.
        AnimatedVisibility(
            visible = chrome.showAuthor,
            modifier = Modifier.align(Alignment.BottomStart),
            enter = fadeIn(tween(CHROME_ANIM_MILLIS)) + slideInVertically(tween(CHROME_ANIM_MILLIS)) { it / 2 },
            exit = fadeOut(tween(CHROME_ANIM_MILLIS)) + slideOutVertically(tween(CHROME_ANIM_MILLIS)) { it / 2 },
        ) {
            ReelOverlay(
                item = item,
                offersFollow = offersFollow,
                onOpenAuthor = actions.onOpenAuthor,
                onFollow = { actions.onFollow(item.author.id) },
            )
        }

        // The rail sits on the right edge, clear of the caption, because that
        // is where a thumb rests while the other hand holds nothing. Putting
        // controls under the caption means reaching across the video to use
        // them.
        AnimatedVisibility(
            visible = chrome.showRail,
            modifier = Modifier.align(Alignment.BottomEnd),
            enter = fadeIn(tween(CHROME_ANIM_MILLIS)) + slideInHorizontally(tween(CHROME_ANIM_MILLIS)) { it / 2 },
            exit = fadeOut(tween(CHROME_ANIM_MILLIS)) + slideOutHorizontally(tween(CHROME_ANIM_MILLIS)) { it / 2 },
        ) {
            ReelActionRail(
                item = item,
                overlay = overlay,
                muted = muted,
                actions = actions,
            )
        }
    }
}

/** The paused mark: a white play triangle on a soft dark disc, centred over the held frame. */
@Composable
private fun PausedGlyph() {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(PAUSE_DISC)
            .clip(CircleShape)
            .background(Color.Black.copy(alpha = PAUSE_DISC_ALPHA))
            .semantics { contentDescription = "Paused" }
            .testTag("reel_paused"),
    ) {
        Icon(
            imageVector = UsIcons.Play,
            contentDescription = null,
            tint = Color.White,
            modifier = Modifier.size(PAUSE_GLYPH),
        )
    }
}

/** Transparent at 60% of the height, black at 70% by the bottom edge. */
@Composable
private fun BottomScrim(modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .fillMaxHeight(SCRIM_FRACTION)
            .background(Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = SCRIM_ALPHA)))),
    )
}

/**
 * The vertical control strip over a reel, Instagram's order: like with its
 * count, comment with its count, share, save, ⋮, then mute. 56dp from the
 * bottom, 20dp between controls. Plain white glyphs on the bottom scrim —
 * no discs; the scrim carries the contrast for the whole strip.
 *
 * Comment and share follow the author's switches — see [railVisibility].
 */
@Composable
private fun ReelActionRail(
    item: FeedItem,
    overlay: EngagementOverlay,
    muted: Boolean,
    actions: ReelActions,
    modifier: Modifier = Modifier,
) {
    val rail = item.controls.railVisibility()
    val reacted = overlay.reactedOr(item.viewer.hasReacted)
    val bookmarked = overlay.bookmarkedOr(item.viewer.isBookmarked)
    Column(
        modifier = modifier
            .padding(end = UsTheme.spacing.m, bottom = RAIL_BOTTOM_INSET)
            .testTag("reel_rail"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(RAIL_GAP),
    ) {
        RailButton(
            icon = if (reacted) UsIcons.HeartFilled else UsIcons.HeartOutline,
            description = if (reacted) "Liked" else "Like",
            count = overlay.likeCountOr(item.counts.likes, item.viewer.hasReacted),
            tint = if (reacted) UsTheme.extended.liveRed else Color.White,
            onClick = { actions.onReact(item.id, item.viewer.hasReacted) },
        )
        if (rail.showComment) {
            RailButton(
                icon = UsIcons.Comment,
                description = "Comments",
                count = item.counts.comments,
                onClick = { actions.onComment(item.id) },
            )
        }
        if (rail.showShare) {
            RailButton(
                icon = UsIcons.Share,
                description = "Share",
                count = null,
                onClick = { actions.onShare(item) },
            )
        }
        RailButton(
            icon = if (bookmarked) UsIcons.BookmarkFilled else UsIcons.BookmarkOutline,
            description = if (bookmarked) "Saved" else "Save",
            count = null,
            tint = if (bookmarked) UsTheme.extended.statusWarning else Color.White,
            onClick = { actions.onBookmark(item.id, item.viewer.isBookmarked) },
        )
        actions.onMore?.let { more ->
            RailButton(
                icon = UsIcons.More,
                description = "More",
                count = null,
                onClick = { more(item) },
            )
        }
        RailButton(
            icon = if (muted) UsIcons.SoundOff else UsIcons.SoundOn,
            description = if (muted) "Unmute" else "Mute",
            count = null,
            onClick = actions.onToggleMute,
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
            modifier = Modifier.size(RAIL_ICON),
        )
        if (count != null && count > 0) {
            Text(
                text = formatCount(count),
                style = MaterialTheme.typography.labelMedium,
                fontSize = RAIL_COUNT_SIZE,
                fontWeight = FontWeight.SemiBold,
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
 * Bottom-left, Instagram's order: a 32dp avatar, the username, the outlined
 * Follow pill (only when the viewer is known not to follow, never on the
 * viewer's own reel), then the caption clamped to two lines with "more".
 *
 * No "♪ Original audio" line: a feed row carries no audio metadata today,
 * and printing a label for a fact the server has not stated would be
 * decoration pretending to be information. It appears the day the row
 * carries a track.
 *
 * Holds no rail controls. Text and targets interleaved in one column made
 * the caption look tappable and the buttons look like part of the sentence.
 */
@Composable
private fun ReelOverlay(
    item: FeedItem,
    offersFollow: Boolean,
    onOpenAuthor: (String) -> Unit,
    onFollow: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var expanded by rememberSaveable(item.id) { mutableStateOf(false) }
    var overflowed by remember(item.id) { mutableStateOf(false) }
    val username = item.author.username?.takeIf { it.isNotBlank() } ?: item.author.nameForDisplay
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(start = OVERLAY_SIDE, end = OVERLAY_RAIL_CLEARANCE, bottom = OVERLAY_BOTTOM)
            .testTag("reel_overlay"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            // The avatar and the name are the way to the author's profile.
            UsAvatar(
                name = item.author.nameForDisplay,
                seed = item.author.id,
                size = UsAvatarSize.Small,
                modifier = Modifier.clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                ) { onOpenAuthor(item.author.id) },
            )
            Text(
                text = username,
                style = MaterialTheme.typography.bodyMedium,
                fontSize = NAME_SIZE,
                fontWeight = FontWeight.SemiBold,
                color = Color.White,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier
                    .weight(1f, fill = false)
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                    ) { onOpenAuthor(item.author.id) }
                    .semantics { role = Role.Button },
            )
            if (offersFollow) {
                FollowPill(onClick = onFollow)
            }
        }
        if (item.text.isNotBlank()) {
            Text(
                text = item.text,
                style = MaterialTheme.typography.bodyMedium,
                fontSize = NAME_SIZE,
                color = Color.White,
                maxLines = if (expanded) Int.MAX_VALUE else CAPTION_LINES,
                overflow = TextOverflow.Ellipsis,
                onTextLayout = { if (!expanded) overflowed = it.hasVisualOverflow },
            )
            if (overflowed || expanded) {
                Text(
                    text = if (expanded) "less" else "more",
                    style = MaterialTheme.typography.labelLarge,
                    color = Color.White.copy(alpha = DIM_ALPHA),
                    modifier = Modifier
                        .clickable(
                            interactionSource = remember { MutableInteractionSource() },
                            indication = null,
                        ) { expanded = !expanded }
                        .semantics { role = Role.Button }
                        .testTag("reel_caption_toggle"),
                )
            }
        }
    }
}

/** Instagram's outlined Follow: a 1dp white hairline, white label, 6dp corners. */
@Composable
private fun FollowPill(onClick: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.pill)
    Text(
        text = "Follow",
        style = MaterialTheme.typography.labelMedium,
        fontSize = PILL_TEXT,
        fontWeight = FontWeight.SemiBold,
        color = Color.White,
        modifier = Modifier
            .clip(shape)
            .border(PILL_BORDER, Color.White, shape)
            .pressScale(onClick)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .semantics { role = Role.Button }
            .testTag("reel_follow"),
    )
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

/** Instagram clamps the reel caption to two lines before "more". */
private const val CAPTION_LINES = 2

/** Chrome in and out — the rail, the author block, the scrim — matched to the shell's bar. */
private const val CHROME_ANIM_MILLIS = 200

/** The bottom scrim covers the lowest 40% of the page. */
private const val SCRIM_FRACTION = 0.4f
private const val SCRIM_ALPHA = 0.7f

/** The rail's bottom edge: 56dp up from the page's bottom. */
private val RAIL_BOTTOM_INSET = 56.dp

/** 20dp between rail controls. */
private val RAIL_GAP = 20.dp

/** Comfortably past the 48dp minimum — this is a one-thumb surface. */
private val RAIL_TARGET = 48.dp

private val RAIL_ICON = 28.dp
private val RAIL_COUNT_SIZE = 12.sp

private val OVERLAY_SIDE = 16.dp
private val OVERLAY_BOTTOM = 56.dp

/** The overlay stops short of the rail so a long name never runs under it. */
private val OVERLAY_RAIL_CLEARANCE = 72.dp
private val NAME_SIZE = 14.sp
private val PILL_TEXT = 12.sp
private val PILL_BORDER = 1.dp

/**
 * Dark enough to read as neutral over ANY frame.
 *
 * Started at 0.32 and it was too weak: over a yellow frame the disc tinted
 * olive and looked like a rendering artefact rather than a control. The plate
 * has to dominate the pixels behind it or it should not be there.
 */
private const val STRIP_PLATE_ALPHA = 0.55f

private const val PRESS_SCALE = 0.85f
private const val PRESS_STIFFNESS = 1200f

/** Secondary text over video: legible, clearly quieter than the caption. */
private const val DIM_ALPHA = 0.7f

/** The paused mark: a 72dp disc at 45% black under a 36dp play triangle, arriving from 80%. */
private val PAUSE_DISC = 72.dp
private val PAUSE_GLYPH = 36.dp
private const val PAUSE_DISC_ALPHA = 0.45f
private const val PAUSE_GLYPH_FROM = 0.8f

/** The pending item's round loader: a 48dp ring, no number in it. */
private val LOADER_SIZE = 48.dp
private val LOADER_STROKE = 3.dp
private const val LOADER_TRACK_ALPHA = 0.25f
