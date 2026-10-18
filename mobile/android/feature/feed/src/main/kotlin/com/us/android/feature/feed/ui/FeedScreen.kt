package com.us.android.feature.feed.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.asPaddingValues
import androidx.compose.foundation.layout.statusBars
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.pager.PagerState
import androidx.compose.foundation.pager.VerticalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.material3.pulltorefresh.PullToRefreshDefaults
import androidx.compose.material3.pulltorefresh.rememberPullToRefreshState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.paging.LoadState
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import coil3.SingletonImageLoader
import coil3.request.ImageRequest
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.feed.data.offersFollow
import com.us.android.core.feed.ui.comments.CommentsSheet
import com.us.android.core.feed.ui.more.PostMoreSheetHost
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.feed.ui.share.PostShareSheetHost
import com.us.android.core.feed.ui.share.PostShareViewModel
import com.us.android.core.media.PlayerPool
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FollowStatus
import com.us.android.core.notifications.ui.UnreadBadgeViewModel
import com.us.android.core.ui.DEFAULT_MEDIA_ASPECT
import com.us.android.core.ui.EngagementFailureBar
import com.us.android.core.ui.LightSystemBarGlyphs
import com.us.android.core.ui.LocalChromeVisibility
import com.us.android.core.ui.StageTopScrim
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.rememberPostSharer
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.launch

/**
 * The Home tab: a full-screen vertical feed (post-screens parity,
 * 2026-10-18). One post per screen — a video plays with sound, a photo post
 * is a sideways carousel, a text post a large card — under a header of the
 * three tabs (For You, Following, HashTag) with the menu at the left and
 * search at the right, all white over the media. The shell draws no inset
 * above this tab and its bar goes dark under it.
 *
 * HashTag is not a timeline: it is the day's trending tags, drawn in the
 * dark theme on the same stage, each opening its tag's posts.
 *
 * Refresh is the reference feed's: pull down at the top for a fresh batch,
 * tap Home again for a fresh batch from the top, a fresh feed on a cold
 * start, the same place when coming back within ten minutes. Nothing is
 * inserted above a post being watched; Following wears a dot when people
 * the viewer follows have posted.
 */
@Composable
fun FeedScreen(
    onOpenAuthor: (userId: String) -> Unit,
    onOpenMessages: () -> Unit,
    onOpenNotifications: () -> Unit,
    /** The header's search glyph — :app decides where search lives. */
    onOpenSearch: () -> Unit,
    /** A trending tag, or a tag under a post, was tapped; :app pushes that tag's posts. */
    onOpenHashtag: (tag: String) -> Unit,
    /** A post's sound was tapped; :app pushes that sound's page. */
    onOpenSound: (soundId: String) -> Unit,
    /** A row of the header's More menu was picked; :app opens that screen. */
    onOpenMenuRow: (HomeMenuRow) -> Unit,
    viewModel: FeedViewModel = hiltViewModel<FeedViewModel, FeedViewModel.Factory>(
        creationCallback = { factory -> factory.create(FeedMode.Home) },
    ),
    badge: UnreadBadgeViewModel = hiltViewModel(),
) {
    var menuOpen by rememberSaveable { mutableStateOf(false) }
    val tab by viewModel.tab.collectAsStateWithLifecycle()
    val trending by viewModel.trending.collectAsStateWithLifecycle()
    val followingDot by viewModel.followingDot.collectAsStateWithLifecycle()
    val unread by badge.count.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { badge.refresh() }
    // Each timeline keeps its own place: For You's page applied to Following
    // would land the reader mid-feed in a list they have not swiped.
    val tabStates = rememberSaveableStateHolder()
    // Bumped when Following starts over on a fresh generation (its dot was lit).
    var followingStartOver by remember { mutableStateOf(0) }
    val reselected = LocalChromeVisibility.current?.reselected ?: emptyFlow()

    // The dot's poll runs while Home is resumed — every minute — and stops in the background.
    val scope = rememberCoroutineScope()
    LifecycleResumeEffect(viewModel) {
        val watch = scope.launch { viewModel.watchFollowing() }
        onPauseOrDispose { watch.cancel() }
    }
    // A dark stage under BOTH system bars whatever the theme.
    LightSystemBarGlyphs()

    val topInset = HEADER_HEIGHT
    Box(modifier = Modifier.fillMaxSize().background(UsTheme.extended.stage).testTag("home_stage")) {
        when (tab) {
            FeedTab.HASHTAG -> UsTheme(darkTheme = true) {
                TrendingHashtagsList(
                    state = trending,
                    onOpenHashtag = onOpenHashtag,
                    onRetry = viewModel::refreshTrending,
                    modifier = Modifier.statusBarsPadding().padding(top = topInset),
                )
            }

            FeedTab.FOR_YOU, FeedTab.FOLLOWING -> tabStates.SaveableStateProvider(tab) {
                FeedContent(
                    viewModel = viewModel,
                    onOpenAuthor = onOpenAuthor,
                    empty = if (tab == FeedTab.FOLLOWING) FOLLOWING_EMPTY else FOR_YOU_EMPTY,
                    onOpenHashtag = onOpenHashtag,
                    onOpenSound = onOpenSound,
                    topInset = topInset,
                    underStatusBar = true,
                    reselected = reselected,
                    startOver = if (tab == FeedTab.FOLLOWING) followingStartOver else 0,
                )
            }
        }
        StageTopScrim(modifier = Modifier.align(Alignment.TopCenter))
        HomeStageHeader(
            selected = tab,
            followingDot = followingDot,
            unreadCount = unread,
            onSelect = { picked -> if (viewModel.selectTab(picked)) followingStartOver++ },
            onOpenMore = { menuOpen = true },
            onOpenNotifications = onOpenNotifications,
            onOpenSearch = onOpenSearch,
            modifier = Modifier.align(Alignment.TopCenter),
        )
    }
    if (menuOpen) {
        HomeMoreSheet(
            onRow = { row -> if (row == HomeMenuRow.MESSAGES) onOpenMessages() else onOpenMenuRow(row) },
            onDismiss = { menuOpen = false },
        )
    }
}

/** What an empty timeline says. Each surface has its own honest reason. */
internal data class FeedEmptyCopy(val title: String, val detail: String)

private val FOR_YOU_EMPTY = FeedEmptyCopy(
    title = "Nothing here yet",
    detail = "Posts from people you follow will show up here.",
)

private val FOLLOWING_EMPTY = FeedEmptyCopy(
    title = "Nothing from people you follow yet",
    detail = "Follow a few accounts and their posts will show up here.",
)

/**
 * A timeline body, full screen: the vertical pager of posts and the
 * comments, share and More sheets over it — everything below a header that
 * every feed surface shares (Home, Friends, a tag's posts). The ViewModel
 * decides WHICH timeline; this only renders it.
 *
 * Playback: the SETTLED page plays, with sound, on this body's own player
 * pool ([FeedPlaybackViewModel.newPool]); the neighbours are prepared, the
 * next pages' photos decoded ([prefetchPages]). It holds on a tap, and
 * while the comments sheet is up or the screen is not resumed. The pool is
 * released when the body leaves.
 *
 * Analytics, per page: the settled video is the watch view
 * ([FeedViewModel.onAutoplayChanged]), and every settled post — photo, poll
 * and text included — is the dwell ([FeedViewModel.onDwellChanged]).
 */
@Suppress("LongMethod", "LongParameterList", "CyclomaticComplexMethod")
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun FeedContent(
    viewModel: FeedViewModel,
    onOpenAuthor: (userId: String) -> Unit,
    empty: FeedEmptyCopy,
    modifier: Modifier = Modifier,
    onOpenHashtag: ((String) -> Unit)? = null,
    onOpenSound: ((String) -> Unit)? = null,
    /** How much of the top a header over the stage covers (below the status bar when [underStatusBar]). */
    topInset: Dp = 0.dp,
    /** The stage runs under the status bar, so what sits under the header pads itself below it. */
    underStatusBar: Boolean = false,
    /** Taps on the current tab's bar item: back to the first post and a fresh batch. */
    reselected: Flow<Unit> = emptyFlow(),
    /** Bumped when the timeline was started over on a fresh generation: back to the first post. */
    startOver: Int = 0,
    more: PostMoreViewModel = hiltViewModel(),
    playback: FeedPlaybackViewModel = hiltViewModel(),
    sharing: PostShareViewModel = hiltViewModel(),
) {
    val items = viewModel.items.collectAsLazyPagingItems()
    val head by viewModel.head.collectAsStateWithLifecycle()
    val overlays by viewModel.overlays.collectAsStateWithLifecycle()
    val pollVotes by viewModel.pollVotes.collectAsStateWithLifecycle()
    val failures by viewModel.failures.collectAsStateWithLifecycle()
    val followEdges by viewModel.followEdges.collectAsStateWithLifecycle()
    var commentsFor by rememberSaveable { mutableStateOf<String?>(null) }
    // Plain remember: a sheet that reopened itself after a process death
    // would be about a post the reader may no longer be looking at.
    var moreFor by remember { mutableStateOf<FeedItem?>(null) }
    var shareFor by remember { mutableStateOf<FeedItem?>(null) }
    var paused by remember { mutableStateOf(false) }
    val share = rememberPostSharer()

    // This body's own players, released the moment it leaves the screen.
    val pool = remember(playback) { playback.newPool() }
    DisposableEffect(pool) { onDispose { pool.release() } }

    val append = items.loadState.append
    val pageCount = stagePageCount(items.itemCount, head != null, appendFailed = append is LoadState.Error)
    val pagerState = rememberPagerState(pageCount = { pageCount })
    val itemAt: (Int) -> FeedItem? = { page -> stageItemAt(page, head, items.itemCount) { items.peek(it) } }

    // Plays only while it is what the reader is looking at: resumed, no
    // comments over it, not held by a tap.
    val allowed = rememberIsResumed() && commentsFor == null
    ReconcileHydration(items = items, viewModel = viewModel)
    val refresher = rememberFeedRefresher(items, pagerState)
    RefreshTriggers(refresher = refresher, reselected = reselected, pagerState = pagerState)
    LaunchedEffect(startOver) { if (startOver > 0) pagerState.scrollToPage(0) }
    // The post just published sits at page 0: the reader came back from
    // posting it, so the feed opens there — they were not watching anything.
    val headId = head?.id
    LaunchedEffect(headId) { if (headId != null) pagerState.scrollToPage(0) }

    SettledPageEffects(
        pagerState = pagerState,
        pageCount = pageCount,
        headId = headId,
        itemAt = itemAt,
        hasHead = head != null,
        itemCount = items.itemCount,
        pool = pool,
        playbackOf = playback::playback,
        mediaPages = viewModel::mediaPages,
        playing = allowed && !paused,
        running = allowed,
        viewModel = viewModel,
        onSettled = { paused = false },
    )

    val callbacks = remember(viewModel, onOpenAuthor) {
        FeedRowCallbacks(
            onOpenAuthor = onOpenAuthor,
            onOpenComments = { commentsFor = it },
            onReact = viewModel::onReact,
            onBookmark = viewModel::onBookmark,
            onRepost = viewModel::onRepost,
            onVotePoll = viewModel::onVotePoll,
            onFollow = viewModel::onFollow,
            onShare = { item -> shareFor = item },
            onMore = { moreFor = it },
        )
    }
    val onSystemShare: (FeedItem) -> Unit = { item ->
        share(item.id, item.text, item.author.nameForDisplay)
        // Recorded only after the chooser was actually launched.
        viewModel.onExternalShared(item.id)
    }

    val headerPad = Modifier.then(if (underStatusBar) Modifier.statusBarsPadding() else Modifier).padding(top = topInset)
    val statusTop = WindowInsets.statusBars.asPaddingValues().calculateTopPadding()
    val pageTopInset = if (underStatusBar) statusTop + topInset else topInset
    val pullState = rememberPullToRefreshState()
    Box(modifier = modifier.fillMaxSize().background(UsTheme.extended.stage)) {
        val refresh = items.loadState.refresh
        val empties = items.itemCount == 0 && head == null
        UsTheme(darkTheme = true) {
            when {
                refresh is LoadState.Loading && empties -> UsLoadingState(label = "Loading feed")
                // Only when nothing is on screen: a failed refresh over loaded
                // posts must never take them away.
                refresh is LoadState.Error && empties -> UsErrorState(
                    message = refresh.error.feedMessage(),
                    onRetry = items::retry,
                )
                refresh is LoadState.NotLoading && empties -> UsEmptyState(title = empty.title, detail = empty.detail)
                else -> PullToRefreshBox(
                    isRefreshing = refresher.inFlight && refresh is LoadState.Loading,
                    onRefresh = refresher::refresh,
                    modifier = Modifier.fillMaxSize(),
                    state = pullState,
                    // Under the header, not under the status bar.
                    indicator = {
                        PullToRefreshDefaults.Indicator(
                            state = pullState,
                            isRefreshing = refresher.inFlight && refresh is LoadState.Loading,
                            modifier = Modifier.align(Alignment.TopCenter).then(headerPad),
                        )
                    },
                ) {
                    VerticalPager(
                        state = pagerState,
                        // The neighbours are composed — and their videos
                        // prepared — so a swipe shows a first frame, not a spinner.
                        beyondViewportPageCount = 1,
                        key = { page -> stagePageKey(page, head, items.itemCount) { items.peek(it)?.id } },
                        modifier = Modifier.fillMaxSize().testTag("feed_pager"),
                    ) { page ->
                        val item = stageItemAt(page, head, items.itemCount) { items[it] }
                        if (item == null) {
                            AppendRetryPage(state = append, onRetry = items::retry)
                        } else {
                            FeedStagePage(
                                item = item,
                                page = page,
                                state = StagePageState(
                                    overlay = overlays[item.id] ?: EngagementOverlay(),
                                    pollVotes = pollVotes[item.id].orEmpty(),
                                    offersFollow = offersFollow(viewModel.ownUserId, item.author.id, followEdges[item.author.id]),
                                    pages = viewModel.mediaPages(item),
                                    poster = viewModel.posterUrl(item),
                                    playback = playback.playback(item),
                                    active = pagerState.settledPage == page,
                                    paused = paused,
                                    topInset = pageTopInset,
                                ),
                                pool = pool,
                                callbacks = callbacks,
                                onTogglePause = { paused = !paused },
                                onOpenHashtag = onOpenHashtag,
                                onOpenSound = onOpenSound,
                            )
                        }
                    }
                }
            }
        }
        if (append is LoadState.Loading && pagerState.settledPage >= pageCount - 1 && pageCount > 0) {
            CircularProgressIndicator(
                color = UsTheme.extended.onMedia,
                modifier = Modifier.align(Alignment.BottomCenter).padding(bottom = UsTheme.spacing.xxl),
            )
        }
        Column(modifier = headerPad.fillMaxWidth()) {
            EngagementFailureBar(failures, onRetry = viewModel::retryFailure, onDismiss = viewModel::dismissFailure)
        }
        MoreSheetMessage(more, sharing)
    }

    shareFor?.let { item ->
        PostShareSheetHost(item = item, viewModel = sharing, onMore = onSystemShare, onDismiss = { shareFor = null })
    }
    commentsFor?.let { postId ->
        CommentsSheet(
            postId = postId,
            onDismiss = { commentsFor = null },
            postAuthorId = itemById(postId, head, items)?.author?.id.orEmpty(),
        )
    }
    moreFor?.let { item ->
        FeedMoreSheet(
            item = item,
            overlays = overlays,
            followEdges = followEdges,
            ownUserId = viewModel.ownUserId,
            onShare = callbacks.onShare,
            onDismiss = { moreFor = null },
            more = more,
        )
    }
}

/** A post the sheets are about, wherever it sits: the head, or a paged row. */
private fun itemById(postId: String, head: FeedItem?, items: LazyPagingItems<FeedItem>): FeedItem? =
    head?.takeIf { it.id == postId } ?: items.itemSnapshotList.items.firstOrNull { it.id == postId }

/**
 * What happens when the pager settles on a page: the video there gets its
 * player and the others stop, the watch view and the dwell move to it, the
 * next pages are warmed, and a tap's hold is let go. Keyed on the SETTLED
 * page, so a fast flick through five posts starts one playback, not five.
 */
@Suppress("LongParameterList")
@Composable
private fun SettledPageEffects(
    pagerState: PagerState,
    pageCount: Int,
    headId: String?,
    itemAt: (Int) -> FeedItem?,
    hasHead: Boolean,
    itemCount: Int,
    pool: PlayerPool,
    playbackOf: (FeedItem) -> com.us.android.core.media.Playback?,
    mediaPages: (FeedItem) -> List<com.us.android.core.ui.PostCardMediaPage>,
    playing: Boolean,
    running: Boolean,
    viewModel: FeedViewModel,
    onSettled: () -> Unit,
) {
    val context = LocalContext.current
    val settled = pagerState.settledPage
    val currentPlaying by rememberUpdatedState(playing)
    val currentRunning by rememberUpdatedState(running)
    LaunchedEffect(settled, pageCount, headId) {
        onSettled()
        val item = itemAt(settled)
        val player = item?.let(playbackOf)?.let { pool.acquire(settled, it) }
        viewModel.onAutoplayChanged(item?.takeIf { player != null }, player?.let(::watchProbe), muted = false)
        if (currentPlaying) pool.playOnly(settled) else pool.pauseAll()
        viewModel.onDwellChanged(item, rankedPosition(settled, hasHead, itemCount), player != null, currentRunning)
        val loader = SingletonImageLoader.get(context)
        prefetchPages(settled, pageCount).forEachIndexed { order, page ->
            val next = itemAt(page) ?: return@forEachIndexed
            val video = playbackOf(next)
            // Two videos ahead buffered (the pool holds four players); every
            // warmed page's first photo decoded.
            if (video != null && order < VIDEO_PRELOAD_AHEAD) pool.preload(page, video)
            mediaPages(next).firstOrNull()?.url?.let { url ->
                loader.enqueue(ImageRequest.Builder(context).data(url).build())
            }
        }
    }
    LaunchedEffect(playing) { if (playing) pool.playOnly(pagerState.settledPage) else pool.pauseAll() }
    LaunchedEffect(running) {
        val item = itemAt(pagerState.settledPage)
        viewModel.onDwellChanged(
            item,
            rankedPosition(pagerState.settledPage, hasHead, itemCount),
            item?.let(playbackOf) != null,
            running,
        )
    }
}

/** A refresh the reader asked for, and the trip back to the first post when it lands. */
internal class FeedRefresher(private val items: LazyPagingItems<FeedItem>) {
    var inFlight by mutableStateOf(false)
        private set

    fun refresh() {
        inFlight = true
        items.refresh()
    }

    fun landed() {
        inFlight = false
    }
}

@Composable
private fun rememberFeedRefresher(items: LazyPagingItems<FeedItem>, pagerState: PagerState): FeedRefresher {
    val refresher = remember(items) { FeedRefresher(items) }
    // A fresh batch replaces what was ahead: once it is in, the reader is on its first post.
    val refresh = items.loadState.refresh
    LaunchedEffect(refresh, refresher.inFlight) {
        if (refresher.inFlight && refresh is LoadState.NotLoading) {
            pagerState.scrollToPage(0)
            refresher.landed()
        } else if (refresher.inFlight && refresh is LoadState.Error) {
            refresher.landed()
        }
    }
    return refresher
}

/**
 * The other two ways a fresh batch is asked for: the bar's item for the tab
 * on screen tapped again (back to the top first, then fresh), and a return
 * to the app after more than [KEEP_POSITION_MILLIS] away.
 */
@Composable
private fun RefreshTriggers(refresher: FeedRefresher, reselected: Flow<Unit>, pagerState: PagerState) {
    LaunchedEffect(reselected, refresher) {
        reselected.collect {
            pagerState.animateScrollToPage(0)
            refresher.refresh()
        }
    }
    var stoppedAt by rememberSaveable { mutableStateOf<Long?>(null) }
    LifecycleEventEffect(Lifecycle.Event.ON_STOP) { stoppedAt = System.currentTimeMillis() }
    LifecycleEventEffect(Lifecycle.Event.ON_START) {
        if (shouldRefreshOnReturn(stoppedAt, System.currentTimeMillis())) refresher.refresh()
        stoppedAt = null
    }
}

/** The page after the last post when the next page failed: why, and a way to try again. */
@Composable
private fun AppendRetryPage(state: LoadState, onRetry: () -> Unit) {
    UsTheme(darkTheme = true) {
        Column(
            modifier = Modifier.fillMaxSize().padding(UsTheme.spacing.xxl),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l, Alignment.CenterVertically),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                text = (state as? LoadState.Error)?.error?.feedMessage() ?: "We couldn't load more posts.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.onMedia,
            )
            UsSecondaryButton(text = "Try again", onClick = onRetry, modifier = Modifier.fillMaxWidth())
        }
    }
}

/**
 * What the more sheet left behind, once it has gone: "We'll show you fewer
 * posts like this", "Blocked @x", or the server's refusal.
 */
@Composable
private fun BoxScope.MoreSheetMessage(more: PostMoreViewModel, sharing: PostShareViewModel) {
    val message by more.message.collectAsStateWithLifecycle()
    val shareMessage by sharing.message.collectAsStateWithLifecycle()
    UsMessageHost(
        message = message ?: shareMessage,
        onDismiss = {
            more.dismissMessage()
            sharing.dismissMessage()
        },
    )
}

/** The more sheet for [item]: a modal window of its own, over the stage. */
@Suppress("LongParameterList")
@Composable
private fun FeedMoreSheet(
    item: FeedItem,
    overlays: Map<String, EngagementOverlay>,
    followEdges: Map<String, FollowStatus>,
    ownUserId: String,
    onShare: (FeedItem) -> Unit,
    onDismiss: () -> Unit,
    more: PostMoreViewModel,
) {
    PostMoreSheetHost(
        item = item,
        overlay = overlays[item.id] ?: EngagementOverlay(),
        followEdge = followEdges[item.author.id],
        ownUserId = ownUserId,
        onShare = onShare,
        onDismiss = onDismiss,
        viewModel = more,
    )
}

/**
 * Hands each freshly loaded page to the ViewModel for reconciliation.
 *
 * Two effects, deliberately. A refresh replaces the whole list with values
 * just fetched, so all of them are server authority. An append leaves the
 * earlier pages in the snapshot exactly as they were loaded — including a
 * `has_reacted=false` captured before the viewer liked the row — so those
 * rows must NOT be reprocessed.
 */
@Composable
private fun ReconcileHydration(items: LazyPagingItems<FeedItem>, viewModel: FeedViewModel) {
    LaunchedEffect(items.loadState.refresh) {
        if (items.loadState.refresh is LoadState.NotLoading && items.itemCount > 0) {
            viewModel.onRefreshHydrated(items.itemSnapshotList.items)
        }
    }
    LaunchedEffect(items.loadState.append, items.itemCount) {
        if (items.loadState.append is LoadState.NotLoading && items.itemCount > 0) {
            viewModel.onAppendHydrated(items.itemSnapshotList.items)
        }
    }
}

/**
 * Every page callback, hoisted ONCE: remembered, so its identity is stable
 * across recompositions and the pages do not recompose for it.
 */
// One parameter per page action: the bundle IS the parameter list.
@Suppress("LongParameterList")
internal class FeedRowCallbacks(
    val onOpenAuthor: (String) -> Unit,
    val onOpenComments: (String) -> Unit,
    val onReact: (postId: String, serverReacted: Boolean) -> Unit,
    val onBookmark: (postId: String, serverBookmarked: Boolean) -> Unit,
    val onRepost: (postId: String, serverReposted: Boolean) -> Unit,
    val onVotePoll: (postId: String, optionId: String) -> Unit,
    val onFollow: (authorId: String) -> Unit,
    val onShare: (FeedItem) -> Unit,
    val onMore: ((FeedItem) -> Unit)?,
)

/**
 * Aspect ratio from the server's real dimensions.
 *
 * Guards against a zero height: a still-processing asset can report 0x0, and
 * dividing by it yields Infinity.
 */
internal fun FeedMedia.aspectRatio(): Float =
    if (width > 0 && height > 0) width.toFloat() / height.toFloat() else DEFAULT_MEDIA_ASPECT

/** Two videos ahead are buffered; the pool's four players are those, the settled one and the one behind. */
private const val VIDEO_PRELOAD_AHEAD = 2
