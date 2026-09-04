package com.us.android.feature.feed.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.exoplayer.ExoPlayer
import androidx.paging.LoadState
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.engagement.data.likeCountOr
import com.us.android.core.engagement.data.reactedOr
import com.us.android.core.engagement.data.repostCountOr
import com.us.android.core.engagement.data.repostedOr
import com.us.android.core.feed.data.offersFollow
import com.us.android.core.feed.ui.comments.CommentsSheet
import com.us.android.core.feed.ui.more.PostMoreSheetHost
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.media.Playback
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.DEFAULT_MEDIA_ASPECT
import com.us.android.core.ui.EngagementFailureBar
import com.us.android.core.ui.PostActionState
import com.us.android.core.ui.PostCard
import com.us.android.core.ui.PostCardMediaPage
import com.us.android.core.ui.PostCardPoll
import com.us.android.core.ui.PostCardPollOption
import com.us.android.core.ui.PostCardState
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.rememberPostSharer

/**
 * The Home tab.
 *
 * Momentum's header, then the "For You | Following | HashTag" row, then
 * whichever the row selects: a timeline for the first two, the trending-tag
 * list for the third. Friends and a tag's posts are the SAME timeline body
 * ([FeedContent]) under a different top bar — see [FriendsFeedScreen] and
 * [HashtagPostsScreen].
 */
@Composable
fun FeedScreen(
    onOpenAuthor: (userId: String) -> Unit,
    onOpenMessages: () -> Unit,
    onOpenNotifications: () -> Unit,
    /** Momentum's header search glyph — :app decides it opens the Explore tab. */
    onOpenSearch: () -> Unit,
    /** A trending tag was tapped; :app pushes that tag's posts. */
    onOpenHashtag: (tag: String) -> Unit,
    /** A video was tapped; :app switches to the Reels tab, which opens on it. */
    onOpenReels: () -> Unit,
    viewModel: FeedViewModel = hiltViewModel<FeedViewModel, FeedViewModel.Factory>(
        creationCallback = { factory -> factory.create(FeedMode.Home) },
    ),
) {
    val tab by viewModel.tab.collectAsStateWithLifecycle()
    val trending by viewModel.trending.collectAsStateWithLifecycle()
    // One scroll position per timeline. For You's offset applied to
    // Following's rows would land the reader mid-list in a feed they have
    // not scrolled.
    val listStates = remember { FeedTab.entries.associateWith { LazyListState() } }

    UsScaffold(
        // Momentum's header: search, Messages, the bell — the same header
        // Reels, Friends and Me wear. Each callback is a REQUIRED parameter so
        // none can be re-added inert. Create lives on the bar's centre button.
        topBar = {
            MomentumHeader(
                onOpenSearch = onOpenSearch,
                onOpenMessages = onOpenMessages,
                onOpenNotifications = onOpenNotifications,
            )
        },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding)) {
            FeedTabsRow(selected = tab, onSelect = viewModel::selectTab)
            when (tab) {
                FeedTab.HASHTAG -> TrendingHashtagsList(
                    state = trending,
                    onOpenHashtag = onOpenHashtag,
                    onRetry = viewModel::refreshTrending,
                )

                FeedTab.FOR_YOU, FeedTab.FOLLOWING -> FeedContent(
                    viewModel = viewModel,
                    onOpenAuthor = onOpenAuthor,
                    onOpenReels = onOpenReels,
                    empty = if (tab == FeedTab.FOLLOWING) FOLLOWING_EMPTY else FOR_YOU_EMPTY,
                    listState = listStates.getValue(tab),
                )
            }
        }
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
 * A timeline body: the engagement failure bar, the paged post list, and the
 * comments sheet and the post viewer over it — everything below a top bar
 * that every feed surface shares. The ViewModel decides WHICH timeline; this
 * only renders it.
 *
 * Video rows play by themselves, muted, one at a time — the most visible
 * one ([rememberAutoplayTarget]) — through the one feed player made here
 * and handed to the list. It pauses while the comments sheet or the viewer
 * is up and whenever the screen is not resumed.
 *
 * Two things leave the screen: the author's name, and a tap on a VIDEO,
 * which goes to the Reels tab at that reel, with sound ([onOpenReels],
 * after [FeedPlaybackViewModel.openInReels] has left the id for Reels to
 * find). A photo's media opens [FeedPostViewer] in place — a pager over
 * these SAME rows, starting at the tapped one — and comments open a sheet.
 *
 * The ⋮ on every post opens the "more" sheet ([PostMoreSheetHost]) over
 * the list, driven by [more]; the confirmation it leaves behind ("We'll
 * show you fewer posts like this") is shown here, under the list, once the
 * sheet has gone.
 */
@Suppress("LongMethod") // One body per timeline: the state, the list, and the three surfaces over it.
@Composable
internal fun FeedContent(
    viewModel: FeedViewModel,
    onOpenAuthor: (userId: String) -> Unit,
    onOpenReels: () -> Unit,
    empty: FeedEmptyCopy,
    modifier: Modifier = Modifier,
    listState: LazyListState = rememberLazyListState(),
    more: PostMoreViewModel = hiltViewModel(),
    playback: FeedPlaybackViewModel = hiltViewModel(),
) {
    val items = viewModel.items.collectAsLazyPagingItems()
    val overlays by viewModel.overlays.collectAsStateWithLifecycle()
    val pollVotes by viewModel.pollVotes.collectAsStateWithLifecycle()
    val failures by viewModel.failures.collectAsStateWithLifecycle()
    val followEdges by viewModel.followEdges.collectAsStateWithLifecycle()
    var commentsFor by rememberSaveable { mutableStateOf<String?>(null) }
    // The post whose ⋮ was tapped. Plain remember, like the viewer: a sheet
    // that reopened itself after a process death would be about a row the
    // reader may no longer be looking at.
    var moreFor by remember { mutableStateOf<FeedItem?>(null) }
    // The row whose media is open in the viewer, by id AND index. Plain
    // remember, not saveable: a viewer that reopened itself after a process
    // death over a feed that has since refreshed would show a row the reader
    // may no longer be looking at.
    var viewing by remember { mutableStateOf<ViewerTarget?>(null) }
    val share = rememberPostSharer()
    val feedPlayer = rememberFeedPlayer(playback)
    // The feed plays only while it is what the reader is looking at: the
    // screen resumed, and neither the comments sheet nor the viewer over it.
    val autoplayAllowed = rememberIsResumed() && commentsFor == null && viewing == null
    val openReel: (FeedItem) -> Unit = { item ->
        playback.openInReels(item)
        onOpenReels()
    }
    val autoplay = remember(feedPlayer, autoplayAllowed, playback) {
        FeedAutoplay(
            player = feedPlayer,
            allowed = autoplayAllowed,
            playbackFor = playback::playback,
            load = playback::load,
        )
    }

    ReconcileHydration(items = items, viewModel = viewModel)

    val callbacks = remember(viewModel, onOpenAuthor, share) {
        FeedRowCallbacks(
            onOpenAuthor = onOpenAuthor,
            onOpenComments = { commentsFor = it },
            onReact = viewModel::onReact,
            onBookmark = viewModel::onBookmark,
            onRepost = viewModel::onRepost,
            onVotePoll = viewModel::onVotePoll,
            onFollow = viewModel::onFollow,
            onShare = { item ->
                share(item.text, item.author.nameForDisplay)
                // Recorded only after the chooser was actually launched, and
                // only here — the repost endpoint already records repost and
                // quote shares, so counting this one through both would
                // double it.
                viewModel.onExternalShared(item.id)
            },
            onMore = { moreFor = it },
        )
    }

    Box(modifier = modifier) {
        Column(modifier = Modifier.fillMaxSize()) {
            EngagementFailureBar(failures, onRetry = viewModel::retryFailure, onDismiss = viewModel::dismissFailure)
            FeedList(
                items = items,
                overlays = overlays,
                pollVotes = pollVotes,
                followEdges = followEdges,
                ownUserId = viewModel.ownUserId,
                // A video goes to Reels; a photo opens the viewer in place.
                onOpenMedia = { index, item ->
                    if (playback.isVideo(item)) openReel(item) else viewing = ViewerTarget(item.id, index)
                },
                callbacks = callbacks,
                posterUrl = viewModel::posterUrl,
                mediaPages = viewModel::mediaPages,
                listState = listState,
                empty = empty,
                autoplay = autoplay,
            )
        }
        MoreSheetMessage(more)
    }

    // Comments open over the feed rather than navigating away, so the reader
    // keeps their place in the list and the post stays visible above the
    // conversation about it.
    commentsFor?.let { postId ->
        CommentsSheet(postId = postId, onDismiss = { commentsFor = null })
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

    // Likewise the viewer: full window over the feed, back to the same row.
    // It pages over the SAME items, so a like made inside it is the same
    // like the row shows when it closes. A video page swiped to inside it
    // plays muted like the feed, and a tap on it goes to Reels the same way.
    viewing?.let { target ->
        FeedPostViewer(
            items = items,
            startPage = viewerStartPage(items.itemSnapshotList.items.map { it.id }, target.postId, target.index),
            overlays = overlays,
            pollVotes = pollVotes,
            followEdges = followEdges,
            ownUserId = viewModel.ownUserId,
            callbacks = callbacks,
            posterUrl = viewModel::posterUrl,
            mediaPages = viewModel::mediaPages,
            onOpenReel = { item ->
                viewing = null
                openReel(item)
            },
            onClose = { viewing = null },
            viewModel = playback,
        )
    }
}

/**
 * What the list needs to play its most visible video: the feed's one
 * player, whether it may play right now, and how to find and load a row's
 * video. One remembered-by-value bundle through the list rather than four
 * parameters — the list already carries as many as it can read.
 */
internal data class FeedAutoplay(
    val player: ExoPlayer,
    val allowed: Boolean,
    val playbackFor: (FeedItem) -> Playback?,
    val load: (ExoPlayer, Playback) -> Unit,
)

/**
 * What the more sheet left behind, once it has gone: "We'll show you fewer
 * posts like this", "Blocked @x", or the server's refusal.
 */
@Composable
private fun BoxScope.MoreSheetMessage(more: PostMoreViewModel) {
    val message by more.message.collectAsStateWithLifecycle()
    UsMessageHost(message = message, onDismiss = more::dismissMessage)
}

/**
 * The more sheet for [item], over the list AND over the viewer: it is a
 * modal window of its own, so it sits above whichever is showing.
 */
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
 * rows must NOT be reprocessed. Reconciling the whole snapshot on append
 * is what made a confirmed like revert on scroll.
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

/** The post the viewer opened on: its id, and the index it was tapped at. */
private data class ViewerTarget(val postId: String, val index: Int)

/**
 * Every row callback the list and the viewer share, hoisted ONCE.
 *
 * A bundle rather than flat parameters here on purpose: it is remembered, so
 * its identity is stable across recompositions and the rows do not recompose
 * for it. The per-row lambdas that close over an item are still created in
 * the row, which is unavoidable and cheap.
 */
// One parameter per row action: the bundle IS the parameter list.
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
 * The page the viewer opens on.
 *
 * By id first: the pager is over the live paging snapshot, and between the
 * tap and the viewer's first frame a refresh can shift every index. The
 * tapped index is the fallback when the id is no longer in the list at all
 * (the row was removed), clamped so the pager never asks for a page past
 * the end.
 */
internal fun viewerStartPage(ids: List<String>, tappedId: String, tappedIndex: Int): Int {
    val byId = ids.indexOf(tappedId)
    if (byId >= 0) return byId
    return tappedIndex.coerceIn(0, (ids.size - 1).coerceAtLeast(0))
}

// Flat callbacks rather than a bundled object: a data class of lambdas gets a
// new identity on every recomposition, which would recompose every visible row.
@Suppress("LongParameterList", "LongMethod")
@Composable
private fun FeedList(
    items: LazyPagingItems<FeedItem>,
    overlays: Map<String, EngagementOverlay>,
    pollVotes: Map<String, Set<String>>,
    followEdges: Map<String, FollowStatus>,
    ownUserId: String,
    /** The row's media was tapped: open the viewer at that index. */
    onOpenMedia: (index: Int, item: FeedItem) -> Unit,
    callbacks: FeedRowCallbacks,
    posterUrl: (FeedItem) -> String?,
    mediaPages: (FeedItem) -> List<PostCardMediaPage>,
    listState: LazyListState,
    empty: FeedEmptyCopy,
    autoplay: FeedAutoplay,
    modifier: Modifier = Modifier,
) {
    val refresh = items.loadState.refresh
    val playingId = feedAutoplay(listState, items, autoplay)

    when {
        refresh is LoadState.Loading && items.itemCount == 0 ->
            UsLoadingState(modifier = modifier, label = "Loading feed")

        // Only when nothing is on screen. A refresh failure with rows already
        // loaded must never clear them — the reader would lose their place to
        // a transient network blip.
        refresh is LoadState.Error && items.itemCount == 0 -> UsErrorState(
            message = refresh.error.feedMessage(),
            modifier = modifier,
            onRetry = items::retry,
        )

        refresh is LoadState.NotLoading && items.itemCount == 0 -> UsEmptyState(
            title = empty.title,
            detail = empty.detail,
            modifier = modifier,
        )

        // A scrollable column of Instagram-laid-out posts, not a full-screen
        // pager. The immersive one-post-per-screen presentation belongs to
        // Reels, where every item is video; on a mixed feed it turns a
        // two-line text post into a full screen of empty space.
        else -> LazyColumn(
            state = listState,
            modifier = modifier.fillMaxSize(),
            // No side gutter, no top inset (founder, 2026-09-04): the posts run
            // edge to edge like Instagram's, and the tabs row above is already
            // an edge. 12dp between neighbours is the only separation.
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            // `key` is what lets Compose keep an item's state across a page
            // append. Without it every append re-keys by index and the whole
            // visible list recomposes, which is the classic feed jank.
            items(
                count = items.itemCount,
                key = { index -> items.peek(index)?.id ?: index },
            ) { index ->
                val item = items[index] ?: return@items
                val overlay = overlays[item.id] ?: EngagementOverlay()
                val poster = posterUrl(item)
                val video = autoplaySlot(
                    item = item,
                    playing = item.id == playingId,
                    player = autoplay.player,
                    posterUrl = poster,
                    onClick = { onOpenMedia(index, item) },
                )
                PostCard(
                    state = item.toCardState(
                        overlay,
                        poster,
                        mediaPages(item),
                        pollVotes[item.id].orEmpty(),
                    ),
                    // A tap on the media: a video goes to Reels, a photo opens
                    // the viewer in place at this row; a text-only post has no
                    // media and never fires it.
                    onClick = { onOpenMedia(index, item) },
                    mediaOverride = video,
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
                    onFollow = if (offersFollow(ownUserId, item.author.id, followEdges[item.author.id])) {
                        { callbacks.onFollow(item.author.id) }
                    } else {
                        null
                    },
                    onMore = callbacks.onMore?.let { more -> { more(item) } },
                )
            }

            item(key = "append_state") {
                AppendState(state = items.loadState.append, onRetry = items::retry)
            }
        }
    }
}

/**
 * Which video card plays, and the player kept on it: the most visible one,
 * by the list's own layout info ([rememberAutoplayTarget]). Null — nothing
 * on screen clears the bar, or the list is not showing — parks the player.
 */
@Composable
private fun feedAutoplay(
    listState: LazyListState,
    items: LazyPagingItems<FeedItem>,
    autoplay: FeedAutoplay,
): String? {
    val playingId by rememberAutoplayTarget(listState, items, autoplay.playbackFor)
    val playingItem = playingId?.let { id -> items.itemSnapshotList.items.firstOrNull { it.id == id } }
    DriveFeedPlayer(
        player = autoplay.player,
        playback = playingItem?.let(autoplay.playbackFor),
        allowed = autoplay.allowed,
        load = autoplay.load,
    )
    return playingId
}

/**
 * What a row draws in its 4:5 frame: the feed player, when this is the
 * playing card; nothing — the poster, as usual — otherwise. The override
 * carries the same tap the poster would: to Reels.
 */
private fun autoplaySlot(
    item: FeedItem,
    playing: Boolean,
    player: ExoPlayer,
    posterUrl: String?,
    onClick: () -> Unit,
): (@Composable () -> Unit)? {
    if (!playing) return null
    return {
        FeedVideo(
            player = player,
            posterUrl = posterUrl,
            contentDescription = item.media.firstOrNull()?.contentDescription,
            onClick = onClick,
        )
    }
}

@Composable
private fun AppendState(state: LoadState, onRetry: () -> Unit) {
    when (state) {
        is LoadState.Loading -> Box(
            modifier = Modifier
                .fillMaxWidth()
                .padding(UsTheme.spacing.xxl),
            contentAlignment = Alignment.Center,
        ) {
            CircularProgressIndicator(color = MaterialTheme.colorScheme.primary)
        }

        is LoadState.Error -> Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(UsTheme.spacing.xxl),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            // A failed APPEND must not clear the list. The rows already on
            // screen are still valid; only the next page is missing.
            Text(
                text = state.error.feedMessage(),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
            )
            UsSecondaryButton(text = "Try again", onClick = onRetry, modifier = Modifier.fillMaxWidth())
        }

        is LoadState.NotLoading -> Unit
    }
}

/**
 * Layers this session's local taps over the server's reported state.
 *
 * Membership in the set means "the user changed this since the page loaded",
 * which is why it is an XOR against the server value rather than a replacement.
 */
internal fun FeedItem.toCardState(
    overlay: EngagementOverlay,
    posterUrl: String?,
    mediaPages: List<PostCardMediaPage>,
    localPollVotes: Set<String> = emptySet(),
) = PostCardState(
    postId = id,
    authorId = author.id,
    authorHandle = author.username?.let { "@$it" },
    // Real author identity, embedded by the server as of 2026-08-17. This was
    // a truncated user id until the feed carried `author` — resolving it
    // per-row would have been an N+1 fired from inside a scrolling list.
    authorName = author.nameForDisplay,
    text = text,
    timestamp = formatRelativeTime(createdAt),
    postType = postType,
    mediaCount = media.size,
    mediaUrl = posterUrl,
    mediaAspectRatio = media.firstOrNull()?.aspectRatio() ?: DEFAULT_MEDIA_ASPECT,
    // Slice C / C-CLB-3. The feed is where most images are seen, so this is
    // the surface where a dropped description costs the most.
    mediaContentDescription = media.firstOrNull()?.contentDescription,
    // The ordered carousel — Creator Studio P0-A.
    mediaPages = mediaPages,
    isPinned = isPinned,
    poll = poll?.let { p ->
        // A vote cast THIS session is layered onto the server's counts the
        // same way engagement taps are — one-step, derived, never accumulated.
        val localOnly = localPollVotes - p.viewerVotedOptionIds.toSet()
        PostCardPoll(
            options = p.options.map { option ->
                PostCardPollOption(
                    id = option.id,
                    label = option.label,
                    voteCount = option.voteCount + if (option.id in localOnly) 1 else 0,
                    percentage = option.percentage,
                )
            },
            totalVotes = p.totalVotes + localOnly.size,
            votedOptionIds = p.viewerVotedOptionIds.toSet() + localPollVotes,
            hasEnded = p.hasEnded,
        )
    },
    actions = PostActionState(
        // Server value plus at most a one-step correction for local intent.
        // Derived rather than accumulated, so repeated taps cannot drift the
        // number and an unlike at zero cannot go negative.
        likeCount = overlay.likeCountOr(counts.likes, viewer.hasReacted),
        commentCount = counts.comments,
        repostCount = overlay.repostCountOr(counts.reposts, viewer.hasReposted),
        hasReacted = overlay.reactedOr(viewer.hasReacted),
        hasReposted = overlay.repostedOr(viewer.hasReposted),
        isBookmarked = overlay.bookmarkedOr(viewer.isBookmarked),
        canRepost = isRepostable,
    ),
)

@Preview(name = "Feed — empty", showBackground = true, heightDp = 400)
@Composable
private fun FeedEmptyPreview() {
    UsTheme {
        UsEmptyState(
            title = FOR_YOU_EMPTY.title,
            detail = FOR_YOU_EMPTY.detail,
        )
    }
}

/**
 * Aspect ratio from the server's real dimensions.
 *
 * Guards against a zero height: a still-processing asset can report 0x0, and
 * dividing by it yields Infinity, which Compose resolves to a zero-height box
 * that then jumps to full size when the real frame arrives.
 */
internal fun FeedMedia.aspectRatio(): Float =
    if (width > 0 && height > 0) width.toFloat() / height.toFloat() else DEFAULT_MEDIA_ASPECT
