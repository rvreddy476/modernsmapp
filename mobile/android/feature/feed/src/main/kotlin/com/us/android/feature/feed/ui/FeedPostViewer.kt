package com.us.android.feature.feed.ui

import androidx.activity.compose.BackHandler
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.pager.VerticalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.nestedscroll.NestedScrollConnection
import androidx.compose.ui.input.nestedscroll.NestedScrollSource
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Velocity
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.paging.compose.LazyPagingItems
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.feed.data.offersFollow
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.PostCard
import com.us.android.core.ui.PostCardMediaPage

/**
 * The Home feed's in-place viewer: Instagram's post detail, over the feed.
 *
 * ## WHY A PAGER OVER THE SAME ROWS, AND NOT A DESTINATION
 *
 * Tapping a post used to push post detail; then it opened one row's media
 * alone. The founder's call (2026-09-04): the viewer is a full-window
 * vertical pager over the SAME feed items, starting at the tapped post, each
 * page the same Instagram card the list draws — header with username and
 * Follow (no wordmark), the media at 4:5, actions, caption, comments link —
 * and reels play as video with sound. It is state in the feed screen, not a
 * route: a full-window [Dialog] so it covers the header and the bottom bar
 * too, closed by Back, by the arrow, or by pulling the first page down past
 * a third of the screen.
 *
 * Paging over [items] — the live [LazyPagingItems] — rather than a copy is
 * what keeps the two in step: swiping to the last loaded post here asks
 * Paging for the next page exactly as scrolling the list would, and a like
 * made on a page is the same overlay the row reads when the viewer closes.
 *
 * Opened on PHOTOS only, since 2026-09-05: a tap on a feed video goes to
 * Reels instead. A video page is still reachable by swiping, and then it
 * plays the way the feed does — muted, looping, in the card's frame — and
 * a single tap on it goes to Reels too ([onOpenReel]).
 *
 * One player at a time: only the SETTLED page owns a player, created when
 * the page settles and released when it leaves. A flick through five reels
 * starts one playback, not five.
 */
@Suppress("LongParameterList")
@Composable
internal fun FeedPostViewer(
    items: LazyPagingItems<FeedItem>,
    startPage: Int,
    overlays: Map<String, EngagementOverlay>,
    pollVotes: Map<String, Set<String>>,
    followEdges: Map<String, FollowStatus>,
    ownUserId: String,
    callbacks: FeedRowCallbacks,
    posterUrl: (FeedItem) -> String?,
    mediaPages: (FeedItem) -> List<PostCardMediaPage>,
    /** A video page was tapped: the host closes the viewer and opens Reels on it. */
    onOpenReel: (FeedItem) -> Unit,
    onClose: () -> Unit,
    viewModel: FeedPlaybackViewModel = hiltViewModel(),
) {
    Dialog(
        onDismissRequest = onClose,
        properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false),
    ) {
        BackHandler(onBack = onClose)
        val pagerState = rememberPagerState(
            initialPage = startPage.coerceIn(0, (items.itemCount - 1).coerceAtLeast(0)),
            pageCount = { items.itemCount },
        )
        var dragHeight by remember { mutableFloatStateOf(1f) }
        val pull = remember { PullToClose(threshold = { dragHeight * CLOSE_FRACTION }, onClose = onClose) }
        // The whole viewer follows the finger down and fades a little, so the
        // gesture reads as "putting it back"; a release short of the
        // threshold springs it home.
        val settledPull by animateFloatAsState(targetValue = pull.offset, label = "viewerPull")

        Box(
            modifier = Modifier
                .fillMaxSize()
                .onSizeChanged { dragHeight = it.height.toFloat().coerceAtLeast(1f) }
                .graphicsLayer {
                    translationY = settledPull
                    alpha = 1f - (settledPull / dragHeight) * DRAG_FADE
                }
                .background(UsTheme.extended.bgCanvas)
                .nestedScroll(pull)
                .testTag("feed_post_viewer"),
        ) {
            VerticalPager(
                state = pagerState,
                // Nothing off screen is composed: a neighbour page would
                // create a player it cannot play.
                beyondViewportPageCount = 0,
                modifier = Modifier.fillMaxSize(),
            ) { page ->
                val item = items[page]
                if (item != null) {
                    ViewerPage(
                        item = item,
                        active = pagerState.settledPage == page,
                        overlay = overlays[item.id] ?: EngagementOverlay(),
                        pollVotes = pollVotes[item.id].orEmpty(),
                        offersFollow = offersFollow(ownUserId, item.author.id, followEdges[item.author.id]),
                        callbacks = callbacks,
                        posterUrl = posterUrl(item),
                        mediaPages = mediaPages(item),
                        onOpenReel = onOpenReel,
                        viewModel = viewModel,
                    )
                }
            }

            BackButton(
                onClick = onClose,
                modifier = Modifier
                    .align(Alignment.TopStart)
                    .statusBarsPadding()
                    .padding(UsTheme.spacing.m),
            )
        }
    }
}

/**
 * Pull-down-to-close as a nested-scroll participant.
 *
 * The pager and each page's own scroll get first refusal on every drag; only
 * what they leave unconsumed — a downward pull on the first page, or on a
 * page already at its top — moves the viewer. Past [threshold] at release
 * it closes; short of it the offset snaps back to zero. Living BELOW the
 * pager in the nested-scroll chain rather than intercepting touches is what
 * lets a swipe between posts and a pull to dismiss share one finger.
 */
private class PullToClose(
    private val threshold: () -> Float,
    private val onClose: () -> Unit,
) : NestedScrollConnection {
    var offset by mutableFloatStateOf(0f)
        private set

    override fun onPreScroll(available: Offset, source: NestedScrollSource): Offset {
        // Pushing back up while pulled: undo the pull before the pager moves.
        if (offset > 0f && available.y < 0f) {
            val consumed = (-available.y).coerceAtMost(offset)
            offset -= consumed
            return Offset(0f, -consumed)
        }
        return Offset.Zero
    }

    override fun onPostScroll(consumed: Offset, available: Offset, source: NestedScrollSource): Offset {
        if (source != NestedScrollSource.UserInput || available.y <= 0f) return Offset.Zero
        offset += available.y
        return Offset(0f, available.y)
    }

    override suspend fun onPreFling(available: Velocity): Velocity {
        if (offset > threshold()) {
            onClose()
        } else {
            offset = 0f
        }
        return Velocity.Zero
    }
}

/**
 * One page: the Instagram card for [item], scrollable when its caption runs
 * past the window. A video row on the ACTIVE page draws a silently playing
 * player in the card's 4:5 frame — the feed's own presentation — and a tap
 * on it goes to Reels; on any other page, and for a video with no rendition
 * yet, the card draws its poster as it does in the list, and a tap on THAT
 * goes to Reels as well.
 */
@Suppress("LongParameterList")
@Composable
private fun ViewerPage(
    item: FeedItem,
    active: Boolean,
    overlay: EngagementOverlay,
    pollVotes: Set<String>,
    offersFollow: Boolean,
    callbacks: FeedRowCallbacks,
    posterUrl: String?,
    mediaPages: List<PostCardMediaPage>,
    onOpenReel: (FeedItem) -> Unit,
    viewModel: FeedPlaybackViewModel,
) {
    val playback = remember(item.id) { viewModel.playback(item) }
    val video: (@Composable () -> Unit)? = if (active && playback != null) {
        {
            val player = remember(playback) {
                viewModel.createSilentPlayer().also { player ->
                    viewModel.load(player, playback)
                    player.playWhenReady = true
                }
            }
            DisposableEffect(player) { onDispose { player.release() } }
            FeedVideo(
                player = player,
                posterUrl = posterUrl,
                contentDescription = item.media.firstOrNull()?.contentDescription,
                onClick = { onOpenReel(item) },
            )
        }
    } else {
        null
    }
    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .statusBarsPadding()
            .padding(top = HEADER_CLEARANCE)
            .navigationBarsPadding(),
    ) {
        PostCard(
            state = item.toCardState(overlay, posterUrl, mediaPages, pollVotes),
            // A photo is already as open as it gets; a video's poster (a page
            // not yet active, or one with no rendition) goes to Reels.
            onClick = { if (viewModel.isVideo(item)) onOpenReel(item) },
            onAuthorClick = { callbacks.onOpenAuthor(item.author.id) },
            onReact = { callbacks.onReact(item.id, item.viewer.hasReacted) },
            onComment = { callbacks.onOpenComments(item.id) },
            onRepost = { callbacks.onRepost(item.id, item.viewer.hasReposted) },
            onBookmark = { callbacks.onBookmark(item.id, item.viewer.isBookmarked) },
            onShare = { callbacks.onShare(item) },
            onVotePoll = if (item.poll?.hasEnded == false) {
                { optionId -> callbacks.onVotePoll(item.id, optionId) }
            } else {
                null
            },
            onFollow = if (offersFollow) {
                { callbacks.onFollow(item.author.id) }
            } else {
                null
            },
            onMore = callbacks.onMore?.let { more -> { more(item) } },
            mediaOverride = video,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun BackButton(onClick: () -> Unit, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .size(BACK_TARGET)
            .clip(CircleShape)
            .background(UsTheme.extended.bgCanvas.copy(alpha = PLATE_ALPHA))
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = onClick,
            )
            .semantics { role = Role.Button }
            .testTag("viewer_back"),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = UsIcons.Back,
            contentDescription = "Back",
            tint = Color.White,
            modifier = Modifier.size(BACK_GLYPH),
        )
    }
}

/** Past a third of the screen the pull is a decision, not a fidget. */
private const val CLOSE_FRACTION = 0.3f

/** How much the viewer fades over a full-height pull. Subtle: the post stays the subject. */
private const val DRAG_FADE = 0.5f
private const val PLATE_ALPHA = 0.7f
private val BACK_TARGET = 44.dp
private val BACK_GLYPH = 22.dp

/** Room under the status bar for the back arrow before the card's header. */
private val HEADER_CLEARANCE = 44.dp
