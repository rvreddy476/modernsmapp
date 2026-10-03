package com.us.android.feature.feed.ui.reels

import androidx.activity.compose.BackHandler
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
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.sizeIn
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.pager.PagerState
import androidx.compose.foundation.pager.VerticalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SuggestionChip
import androidx.compose.material3.SuggestionChipDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
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
import androidx.compose.ui.graphics.drawscope.Stroke
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
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.C
import androidx.media3.common.Player
import androidx.media3.common.Tracks
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.compose.PlayerSurface
import androidx.media3.ui.compose.SURFACE_TYPE_SURFACE_VIEW
import androidx.paging.LoadState
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import coil3.compose.AsyncImage
import com.us.android.core.analytics.WatchProbe
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsBadgedIcon
import com.us.android.core.designsystem.component.UsFollowButton
import com.us.android.core.designsystem.component.UsHeaderCorner
import com.us.android.core.designsystem.component.UsHeaderCornerAction
import com.us.android.core.designsystem.component.UsHomeTopBar
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.usNotificationsDescription
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.engagement.data.likeCountOr
import com.us.android.core.engagement.data.reactedOr
import com.us.android.core.feed.offline.OfflineEntry
import com.us.android.core.feed.offline.OfflinePhase
import com.us.android.core.feed.offline.OfflineState
import com.us.android.core.feed.offline.WAITING_FOR_NETWORK
import com.us.android.core.feed.ui.comments.CommentsSheet
import com.us.android.core.feed.ui.more.PostMoreSheetHost
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.feed.ui.sound.ReelSoundViewModel
import com.us.android.core.media.Playback
import com.us.android.core.media.PlaybackKind
import com.us.android.core.media.PlayerPool
import com.us.android.core.media.sound.ReelSoundPlayer
import com.us.android.core.media.sound.SoundTrack
import com.us.android.core.media.sound.appliedVolume
import com.us.android.core.media.ui.OfflineCopyBadge
import com.us.android.core.media.ui.OfflineSaveRing
import com.us.android.core.media.ui.VideoLoadingIndicator
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.canUseSound
import com.us.android.core.notifications.ui.UnreadBadgeViewModel
import com.us.android.core.ui.HideShellBottomBar
import com.us.android.core.ui.LightStatusBarGlyphs
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsReelMoreState
import com.us.android.core.ui.UsReelQuality
import com.us.android.core.ui.reelQualityOptions
import com.us.android.core.ui.rememberPostSharer
import com.us.android.feature.feed.ui.watchProbe
import java.io.File
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive

/**
 * The reels surface: Instagram Reels on Momentum's palette (founder,
 * 2026-09-04). A full-screen vertical pager of short video that fills the
 * frame from the very top (the shell hands this tab no status-bar inset);
 * the header — search and More, white — translucent over the top
 * of the video on its own scrim; the right rail — like, comment, share,
 * save, mute — bottom-right; the author, Follow and
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
 * exists. No banner anywhere — the item IS the progress. The same slot
 * holds a reel a feed tap asked for that the ranked pages did not have
 * ([OpenOnEntry]).
 *
 * Reels opens MUTED (founder, 2026-09-30, like the web), and once the viewer
 * turns the sound on with the rail's speaker it stays on — across reels,
 * across visits and across restarts — until they mute again. The first reel
 * waits for that stored choice to be read ([reelMayPlay]), so a viewer who
 * chose sound never hears a muted reel flip on.
 *
 * A reel may play an ADDED sound: the audio of another creator's reel
 * (original sounds, 2026-09-30). It is a second file on a second, audio-only
 * player ([ReelSoundPlayer]) kept in step with the settled page's video; the
 * mute switch moves both. The author block names the sound under the
 * hashtags, and the More sheet offers "Use this sound".
 *
 * From YouTube Shorts (founder, 2026-09-04, "combine both"): every rail
 * control carries a label — the count where there is one ([railControls]);
 * the author row is a 36dp avatar, "@handle" and a white Follow pill; and a
 * 2dp playhead line runs along the bottom of the frame ([ProgressLine]).
 *
 * The header's More (founder, 2026-09-05; the three dots since 2026-10-02; it was the rail's ⋮ before)
 * opens the same "more" sheet the feed card opens ([PostMoreSheetHost]),
 * driven by [more], for the reel the pager has SETTLED on, with the reel's
 * own group on top — Description, Clear screen / Show controls, Quality (the
 * HLS ladder the settled player reports; the pick is held for the session
 * by the ViewModel and applied to every page's player). What the sheet
 * leaves behind ("We'll show you fewer posts like this") is shown over the
 * reel once it has gone. The header's other glyphs are search and, since
 * 2026-10-02, the bell with the unread count Home's bell shows; no wordmark
 * and no messages over a reel.
 */
@Composable
fun ReelsScreen(
    pool: PlayerPool,
    onOpenAuthor: (userId: String) -> Unit,
    /** The header's search glyph. Required: a header glyph that does nothing must not ship again. */
    onOpenSearch: () -> Unit,
    /** A hashtag chip under the reel's title; `:app` pushes that tag's posts. */
    onOpenHashtag: (tag: String) -> Unit,
    /** The reel's sound line; `:app` pushes that sound's page. */
    onOpenSound: (soundId: String) -> Unit,
    /** "Use this sound": the sound is already in `SoundEntry`; `:app` opens the reel create flow. */
    onCreateWithSound: () -> Unit,
    /** "Offline", from the More sheet: `:app` pushes the page of what this device keeps. */
    onOpenOffline: () -> Unit,
    /** The header's bell. Required for the reason [onOpenSearch] is. */
    onOpenNotifications: () -> Unit,
    viewModel: ReelsViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
    sound: ReelSoundViewModel = hiltViewModel(),
    // The same count Home's bell shows: one singleton behind both, refreshed when the page appears.
    badge: UnreadBadgeViewModel = hiltViewModel(),
) {
    val unread by badge.count.collectAsStateWithLifecycle()
    LaunchedEffect(Unit) { badge.refresh() }
    val head by viewModel.head.collectAsStateWithLifecycle()
    val view = rememberReelsViewState(viewModel)
    val chrome = view.chrome
    val overlays by viewModel.overlays.collectAsStateWithLifecycle()
    val followEdges by viewModel.followEdges.collectAsStateWithLifecycle()
    val subscriptionEdges by viewModel.subscriptionEdges.collectAsStateWithLifecycle()
    val items = viewModel.items.collectAsLazyPagingItems()
    val pagerState = rememberReelsPager(viewModel, items, head)
    var commentsFor by rememberSaveable { mutableStateOf<String?>(null) }
    var moreFor by remember { mutableStateOf<FeedItem?>(null) }
    // The reel the pager has settled on — what the header's More opens
    // the more sheet FOR. Null over the pending head, where there is no
    // reel yet to describe or report; More then does nothing.
    var settledReel by remember { mutableStateOf<FeedItem?>(null) }
    // The player of the page the pager has settled on — what the more
    // sheet's Quality row reads its ladder from. Screen state, never the
    // ViewModel's: a player is an Android object the pool owns and recycles.
    var settledPlayer by remember { mutableStateOf<Player?>(null) }
    val trackHeights = rememberVideoHeights(settledPlayer)
    val share = rememberPostSharer()
    // Recorded only AFTER the chooser was launched, and shared by the rail's
    // glyph and the more sheet's row so the count cannot be taken twice.
    val onShare: (FeedItem) -> Unit = { item ->
        share(item.text, item.author.nameForDisplay)
        viewModel.onExternalShared(item.id)
    }

    ReleaseOnLifecycle(pool, sound.player)
    // The mute switch moves BOTH players: the pool's through each page, the
    // sound's here. Set before anything is attached, so the first sound a
    // page plays already knows it.
    LaunchedEffect(view.muted, sound) { sound.player.setMuted(view.muted) }
    SoundDestinations(viewModel = viewModel, onOpenSound = onOpenSound, onCreateWithSound = onCreateWithSound)

    // The shell's bar follows the mode, and everything is given back when
    // the screen is left: the bar by HideShellBottomBar's own dispose, the
    // mode and the pause by the reset here — so a tab switch out of full
    // mode lands on a Home with its bar, and the next visit to Reels opens
    // in normal mode with a moving reel.
    HideShellBottomBar(hidden = !chrome.showBottomBar)
    // The reel runs under the status bar and is a dark stage on either theme,
    // so the bar's glyphs stay light here whatever the device's setting.
    LightStatusBarGlyphs()
    // Back in full mode brings the controls back; it never leaves the tab.
    // Without this the system Back reached the root and closed the app
    // from a screen that had hidden every other way out (founder, 2026-09-04).
    BackHandler(enabled = view.fullMode) { viewModel.toggleMode() }
    DisposableEffect(viewModel) {
        onDispose { viewModel.resetView() }
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(UsTheme.extended.stage),
    ) {
        ReelsBody(
            items = items,
            head = head,
            pagerState = pagerState,
            pool = pool,
            sound = sound.player,
            view = view,
            overlays = overlays,
            followEdges = followEdges,
            subscriptionEdges = subscriptionEdges,
            ownUserId = viewModel.ownUserId,
            playbackFor = viewModel::playback,
            soundTrackFor = viewModel::soundTrack,
            actions = reelActions(
                viewModel = viewModel,
                onOpenAuthor = onOpenAuthor,
                onShare = onShare,
                onComment = { commentsFor = it },
                onOpenHashtag = onOpenHashtag,
                onSettledReel = { settledReel = it },
                onSettledPlayer = { settledPlayer = it },
            ),
        )

        ScreenChrome(
            paused = view.paused,
            showHeader = chrome.showHeader,
            // With no reel to open the sheet for (the feed did not load, as with no
            // network), More goes straight to what the device keeps.
            onOpenMenu = { settledReel?.let { moreFor = it } ?: onOpenOffline() },
            onOpenSearch = onOpenSearch,
            unreadCount = unread,
            onOpenNotifications = onOpenNotifications,
        )
        ReelsMessages(viewModel = viewModel, more = more)
    }

    // Comments open over the reel rather than navigating away: the reel keeps
    // playing behind the conversation about it.
    commentsFor?.let { postId ->
        CommentsSheet(postId = postId, onDismiss = { commentsFor = null })
    }

    moreFor?.let { item ->
        ReelMoreSheet(
            item = item,
            viewModel = viewModel,
            more = more,
            trackHeights = trackHeights,
            onShare = onShare,
            onDismiss = { moreFor = null },
            onOpenOffline = onOpenOffline,
        )
    }
}

/**
 * The same more sheet the feed card opens, over the playing reel, with the
 * reel's own rows among the rest: the caption to unfold, full mode, the
 * ladder the settled player reports — Auto alone for an original MP4, which
 * has no ladder to pick from — and "Use this sound" when it is offered.
 */
@Suppress("LongParameterList") // The sheet's collaborators, hoisted from the screen.
@Composable
private fun ReelMoreSheet(
    item: FeedItem,
    viewModel: ReelsViewModel,
    more: PostMoreViewModel,
    trackHeights: List<Int>,
    onShare: (FeedItem) -> Unit,
    onDismiss: () -> Unit,
    onOpenOffline: () -> Unit,
) {
    val mode by viewModel.mode.collectAsStateWithLifecycle()
    val quality by viewModel.quality.collectAsStateWithLifecycle()
    val overlays by viewModel.overlays.collectAsStateWithLifecycle()
    val followEdges by viewModel.followEdges.collectAsStateWithLifecycle()
    PostMoreSheetHost(
        item = item,
        overlay = overlays[item.id] ?: EngagementOverlay(),
        followEdge = followEdges[item.author.id],
        ownUserId = viewModel.ownUserId,
        onShare = onShare,
        onDismiss = onDismiss,
        viewModel = more,
        reel = reelMoreState(
            item = item,
            mode = mode,
            trackHeights = trackHeights,
            quality = quality,
            playback = viewModel.playback(item),
            canUseSound = item.canUseSound(item.isOwnedBy(viewModel.ownUserId)),
        ),
        onClearScreen = viewModel::toggleMode,
        onSelectQuality = viewModel::selectQuality,
        onUseSound = { reel -> viewModel.onUseSound(reel, SoundIntent.CREATE) },
        onOpenOffline = onOpenOffline,
    )
}

/** The switches the viewer flips and the mode's chrome, collected once, as the one value every page reads. */
@Composable
private fun rememberReelsViewState(viewModel: ReelsViewModel): ReelsViewState {
    val muted by viewModel.muted.collectAsStateWithLifecycle()
    val soundChoiceRead by viewModel.soundChoiceRead.collectAsStateWithLifecycle()
    val paused by viewModel.paused.collectAsStateWithLifecycle()
    val mode by viewModel.mode.collectAsStateWithLifecycle()
    val quality by viewModel.quality.collectAsStateWithLifecycle()
    val offline by viewModel.offlineState.collectAsStateWithLifecycle()
    return ReelsViewState(
        muted = muted,
        paused = paused,
        chrome = mode.chrome(),
        quality = quality,
        soundChoiceRead = soundChoiceRead,
        fullMode = mode == ReelsMode.FULL,
        offline = offline,
    )
}

/** One line at a time over the reel: why "use this sound" was refused, else what the More sheet left behind. */
@Composable
private fun BoxScope.ReelsMessages(viewModel: ReelsViewModel, more: PostMoreViewModel) {
    val soundMessage by viewModel.soundMessage.collectAsStateWithLifecycle()
    val engagementMessage by viewModel.engagementMessage.collectAsStateWithLifecycle()
    val moreMessage by more.message.collectAsStateWithLifecycle()
    UsMessageHost(
        message = soundMessage ?: engagementMessage ?: moreMessage,
        onDismiss = {
            viewModel.dismissSoundMessage()
            viewModel.dismissEngagementMessage()
            more.dismissMessage()
        },
    )
}

/**
 * Goes where "use this sound" decided, once: the reel create flow, with the
 * sound already waiting for it in `SoundEntry`, or the sound's own page.
 */
@Composable
private fun SoundDestinations(
    viewModel: ReelsViewModel,
    onOpenSound: (soundId: String) -> Unit,
    onCreateWithSound: () -> Unit,
) {
    val destination by viewModel.soundDestination.collectAsStateWithLifecycle()
    LaunchedEffect(destination) {
        when (val target = destination) {
            null -> return@LaunchedEffect
            SoundDestination.Create -> onCreateWithSound()
            is SoundDestination.Page -> onOpenSound(target.soundId)
        }
        viewModel.onSoundDestinationTaken()
    }
}

/**
 * The pager's state — the SCREEN's, not the pager's, because a feed tap
 * asks for a scroll to a reel and the request is answered here, where the
 * ViewModel's entry meets the pages ([OpenOnEntry]). One page per ranked
 * reel, plus one for the head when there is one.
 */
@Composable
private fun rememberReelsPager(
    viewModel: ReelsViewModel,
    items: LazyPagingItems<FeedItem>,
    head: ReelsHead?,
): PagerState {
    val pageCount = items.itemCount + if (head != null) 1 else 0
    val pagerState = rememberPagerState(pageCount = { pageCount })
    OpenOnEntry(viewModel = viewModel, items = items, head = head, pagerState = pagerState)
    return pagerState
}

/**
 * Answers a feed tap (founder, 2026-09-05): Reels opens AT the tapped reel.
 *
 * Two steps, each its own effect. Once the pages have settled — the first
 * load finished, well or badly — the ViewModel is handed what they hold
 * and takes the entry: the reel is either among them or fetched and
 * pinned as the head ([ReelsViewModel.resolveEntry]). Then, as soon as the
 * target is on a page ([entryPage]), the pager jumps there — no animation,
 * because the viewer tapped THAT reel and should see it, not a flick past
 * the ones before it — and the target is let go.
 */
@Composable
private fun OpenOnEntry(
    viewModel: ReelsViewModel,
    items: LazyPagingItems<FeedItem>,
    head: ReelsHead?,
    pagerState: PagerState,
) {
    val entry by viewModel.entry.collectAsStateWithLifecycle()
    val target by viewModel.entryTarget.collectAsStateWithLifecycle()
    val refresh = items.loadState.refresh
    val headId = (head as? ReelsHead.Live)?.item?.id
    LaunchedEffect(entry, refresh, items.itemCount, headId) {
        if (entry == null || refresh is LoadState.Loading) return@LaunchedEffect
        viewModel.resolveEntry(items.itemSnapshotList.items.map { it.id } + listOfNotNull(headId))
    }
    LaunchedEffect(target, headId, items.itemCount) {
        val postId = target ?: return@LaunchedEffect
        val page = entryPage(postId, headId, items.itemSnapshotList.items.map { it.id }) ?: return@LaunchedEffect
        pagerState.scrollToPage(page)
        viewModel.onEntryShown()
    }
}

/**
 * The reel's rows of the more sheet: the caption, the mode, the ladder — Auto
 * alone for an original MP4 — and whether "Use this sound" is offered.
 */
@Suppress("LongParameterList") // One fact per row of the sheet.
private fun reelMoreState(
    item: FeedItem,
    mode: ReelsMode,
    trackHeights: List<Int>,
    quality: UsReelQuality,
    playback: Playback?,
    canUseSound: Boolean,
) = UsReelMoreState(
    description = item.text,
    fullMode = mode == ReelsMode.FULL,
    qualities = reelQualityOptions(heights = trackHeights, adaptive = playback?.kind == PlaybackKind.Hls),
    selected = quality,
    canUseSound = canUseSound,
    // The creator turned sharing off: the rail has no share glyph, and the sheet no Share row.
    shareHidden = item.controls.hideShare,
)

/**
 * The heights of every playable video track the player knows about, kept
 * current: read when the player changes hands and again on every
 * `onTracksChanged`, which is when an HLS master's ladder becomes known —
 * a beat after prepare, not at it.
 */
@Composable
private fun rememberVideoHeights(player: Player?): List<Int> {
    var heights by remember(player) { mutableStateOf(player?.currentTracks?.videoHeights().orEmpty()) }
    DisposableEffect(player) {
        if (player == null) return@DisposableEffect onDispose {}
        val listener = object : Player.Listener {
            override fun onTracksChanged(tracks: Tracks) {
                heights = tracks.videoHeights()
            }
        }
        player.addListener(listener)
        onDispose { player.removeListener(listener) }
    }
    return heights
}

/** Every supported video track's height, in the order the playlist listed them; the picker sorts. */
private fun Tracks.videoHeights(): List<Int> = groups
    .filter { it.type == C.TRACK_TYPE_VIDEO }
    .flatMap { group ->
        (0 until group.length)
            .filter(group::isTrackSupported)
            .map { group.getTrackFormat(it).height }
    }
    .filter { it > 0 }

/**
 * The session's quality choice, applied to one player. Auto lifts every size
 * constraint and lets ABR choose; a height pins BOTH bounds to it so the
 * selector takes that rung and no other — max alone would still let it drop
 * on a stall. A rung the item does not have falls back to the selector's own
 * nearest, which is what a viewer who asked for 720p on a 360p reel expects.
 *
 * A change on a PREPARED player re-prepares it where it is — stop, prepare,
 * seek back, same play state — rather than letting the selector swap
 * renditions inside the running decoder. A swap in place reuses the codec,
 * and a codec that mishandles a smaller frame arriving mid-stream (the
 * emulator's does: 720p → 360p drew a torn top third until the rung changed
 * again, 2026-09-04) shows the viewer exactly the thing they just asked to
 * fix. The re-prepare costs a short rebuffer from the cache and starts the
 * new rung clean on its own decoder — the beat YouTube also takes on a
 * manual pick. An unchanged choice is left alone: no re-prepare, no hiccup.
 */
private fun Player.applyQuality(quality: UsReelQuality) {
    val before = trackSelectionParameters
    val builder = before.buildUpon()
    when (quality) {
        UsReelQuality.Auto -> builder.clearVideoSizeConstraints().setMinVideoSize(0, 0)
        is UsReelQuality.Height -> {
            builder.setMaxVideoSize(Int.MAX_VALUE, quality.height)
            builder.setMinVideoSize(0, quality.height)
        }
    }
    val after = builder.build()
    if (after == before) return
    trackSelectionParameters = after
    if (playbackState == Player.STATE_IDLE) return
    val position = currentPosition
    val wasPlaying = playWhenReady
    stop()
    prepare()
    seekTo(position)
    playWhenReady = wasPlaying
}

/**
 * What every page reads from the screen, bundled: the three switches the
 * viewer flips and the mode's chrome. One value through the pager rather
 * than four parameters, and one identity per change.
 */
internal data class ReelsViewState(
    val muted: Boolean,
    val paused: Boolean,
    val chrome: ReelsChrome,
    val quality: UsReelQuality,
    /** The viewer's stored choice of sound has been read; until then nothing starts. */
    val soundChoiceRead: Boolean = true,
    /** Full mode is on: Back brings the controls back rather than leaving the tab. */
    val fullMode: Boolean = false,
    /** Where each reel's offline copy stands on this device (2026-10-02). */
    val offline: OfflineState = OfflineState(),
) {
    /** The settled reel may run: not paused, and the viewer's choice of sound is known. */
    val mayPlay: Boolean get() = reelMayPlay(paused, soundChoiceRead)
}

/**
 * What the SCREEN draws over the pager, as opposed to what a page draws over
 * its own video: the paused mark and the Momentum header.
 *
 * The paused mark is a play glyph, centred, only while paused. It sits above
 * the pager and below the header so it never covers a control, and it is not
 * itself tappable — the video under it is.
 *
 * The header — search, the bell and More (founder, 2026-10-02)
 * — rides its own top scrim and pads itself under the status bar the shell
 * left uncovered. It leaves upward in full mode, the same 200ms as the bar
 * leaves down.
 */
@Composable
private fun BoxScope.ScreenChrome(
    paused: Boolean,
    showHeader: Boolean,
    onOpenMenu: () -> Unit,
    onOpenSearch: () -> Unit,
    unreadCount: Int,
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
        ReelsHeader(
            unreadCount = unreadCount,
            onOpenMenu = onOpenMenu,
            onOpenSearch = onOpenSearch,
            onOpenNotifications = onOpenNotifications,
            modifier = Modifier.testTag("reels_header"),
        )
    }
}

/**
 * Three white glyphs over the translucent top scrim: search, the bell with
 * its unread count, and then, at the corner, the three-dots More, which
 * opens the settled reel's More sheet. The order and the glyphs are
 * [UsHeaderCorner]'s (founder, 2026-10-02: Search, Notifications, More at
 * the corner, the same on every page but Home). No wordmark (over a video
 * the brand is the video) and no messages: that one stays on Home's
 * header. The same [UsHomeTopBar] as Home so the scrim, the height and the
 * status-bar padding are one drawing, not two.
 */
@Composable
private fun ReelsHeader(
    unreadCount: Int,
    onOpenMenu: () -> Unit,
    onOpenSearch: () -> Unit,
    onOpenNotifications: () -> Unit,
    modifier: Modifier = Modifier,
) {
    UsHomeTopBar(
        modifier = modifier,
        translucent = true,
        showWordmark = false,
        actions = {
            UsHeaderCorner.forEach { action ->
                val (onClick, tag) = when (action) {
                    UsHeaderCornerAction.SEARCH -> onOpenSearch to "reels_header:search"
                    UsHeaderCornerAction.NOTIFICATIONS -> onOpenNotifications to "reels_header:notifications"
                    UsHeaderCornerAction.MORE -> onOpenMenu to "reels_header:menu"
                    // Home's alone; the standard corner never carries it.
                    UsHeaderCornerAction.MESSAGES -> return@forEach
                }
                if (action == UsHeaderCornerAction.NOTIFICATIONS) {
                    IconButton(
                        onClick = onClick,
                        modifier = Modifier
                            .testTag(tag)
                            .semantics { contentDescription = usNotificationsDescription(unreadCount) },
                    ) {
                        UsBadgedIcon(icon = action.icon, count = unreadCount, tint = UsTheme.extended.onMedia)
                    }
                } else {
                    IconButton(onClick = onClick, modifier = Modifier.testTag(tag)) {
                        Icon(
                            imageVector = action.icon,
                            contentDescription = action.description,
                            tint = UsTheme.extended.onMedia,
                        )
                    }
                }
            }
        },
    )
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
    /** A hashtag chip: that tag's posts. */
    val onOpenHashtag: (tag: String) -> Unit,
    /** The sound line: the sound's page, made first when the reel plays only its own audio. */
    val onSoundLine: (FeedItem) -> Unit,
    val onFollow: (authorId: String) -> Unit,
    /** Subscribe to the author's channel, when the reel carries one ([reelRelationship]). */
    val onSubscribe: (channelId: String) -> Unit,
    /**
     * The pager settled on this reel.
     *
     * The probe is how analytics reads the player without `:core:analytics`
     * depending on media3 — the ViewModel passes it straight through, and the
     * tracker polls it on its own cadence. Null when the page has no player
     * (a reel still transcoding), which means the view is not counted. The
     * last argument is the pager's page, for the view's rank.
     */
    val onShown: (FeedItem, (suspend () -> WatchProbe)?, Int) -> Unit,
    /** The reel of the settled page, or null when the settled page has none (the pending head). */
    val onSettledReel: (FeedItem?) -> Unit,
    /** The player of the settled page, or null when the settled page has none (the pending head). */
    val onSettledPlayer: (Player?) -> Unit,
    val onRetryPublish: () -> Unit,
    val onDiscardPublish: () -> Unit,
)

/**
 * The bundle for [ReelsScreen]: everything the ViewModel answers directly,
 * plus the five things only the screen can do — push a profile, open the
 * system share sheet, open the comments sheet, and hold the settled reel
 * and its player (the header's more sheet opens on the former, reads its
 * ladder from the latter).
 */
private fun reelActions(
    viewModel: ReelsViewModel,
    onOpenAuthor: (String) -> Unit,
    onShare: (FeedItem) -> Unit,
    onComment: (postId: String) -> Unit,
    onOpenHashtag: (tag: String) -> Unit,
    onSettledReel: (FeedItem?) -> Unit,
    onSettledPlayer: (Player?) -> Unit,
) = ReelActions(
    onToggleMute = viewModel::toggleMuted,
    onTogglePause = viewModel::togglePaused,
    onToggleMode = viewModel::toggleMode,
    onOpenAuthor = onOpenAuthor,
    onReact = viewModel::onReact,
    onBookmark = viewModel::onBookmark,
    onComment = onComment,
    onShare = onShare,
    onOpenHashtag = onOpenHashtag,
    onSoundLine = { reel -> viewModel.onUseSound(reel, SoundIntent.PAGE) },
    onFollow = viewModel::onFollow,
    onSubscribe = viewModel::onSubscribe,
    onShown = viewModel::onReelShown,
    onSettledReel = onSettledReel,
    onSettledPlayer = onSettledPlayer,
    onRetryPublish = viewModel::retryPublish,
    onDiscardPublish = viewModel::discardPublish,
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
    pagerState: PagerState,
    pool: PlayerPool,
    sound: ReelSoundPlayer,
    view: ReelsViewState,
    overlays: Map<String, EngagementOverlay>,
    followEdges: Map<String, FollowStatus>,
    subscriptionEdges: Map<String, ChannelSubscription>,
    ownUserId: String,
    playbackFor: (FeedItem) -> Playback?,
    /** The added sound a reel's page plays, from its stored copy when the reel plays from the device. */
    soundTrackFor: (FeedItem) -> SoundTrack?,
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
            pagerState = pagerState,
            pool = pool,
            sound = sound,
            view = view,
            overlays = overlays,
            followEdges = followEdges,
            subscriptionEdges = subscriptionEdges,
            ownUserId = ownUserId,
            playbackFor = playbackFor,
            soundTrackFor = soundTrackFor,
            actions = actions,
        )
    }
}

@Suppress("LongParameterList")
@Composable
private fun ReelsPager(
    items: LazyPagingItems<FeedItem>,
    head: ReelsHead?,
    pagerState: PagerState,
    pool: PlayerPool,
    sound: ReelSoundPlayer,
    view: ReelsViewState,
    overlays: Map<String, EngagementOverlay>,
    followEdges: Map<String, FollowStatus>,
    subscriptionEdges: Map<String, ChannelSubscription>,
    ownUserId: String,
    playbackFor: (FeedItem) -> Playback?,
    /** The added sound a reel's page plays, from its stored copy when the reel plays from the device. */
    soundTrackFor: (FeedItem) -> SoundTrack?,
    actions: ReelActions,
) {
    val pageCount = pagerState.pageCount
    // Read when the effect below RUNS, not when it was keyed: the viewer's
    // choice of sound may have been read in between.
    val soundChoiceRead by rememberUpdatedState(view.soundChoiceRead)

    // peek, not get: a neighbour lookup must not trigger a page load.
    fun reelAt(page: Int): FeedItem? = (pageAt(page, head, items, load = false) as? ReelsPage.Reel)?.item

    // Keyed on the SETTLED page rather than the scroll offset: a fast flick
    // through five reels must start one playback, not five.
    LaunchedEffect(pagerState.settledPage, pageCount, head) {
        val current = pagerState.settledPage
        val reel = reelAt(current)
        val player = reel?.let(playbackFor)?.let { pool.acquire(current, it) }
        actions.onSettledReel(reel)
        actions.onSettledPlayer(player)
        // The added sound serves the settled page and no other. It is
        // attached BEFORE the video is told to play, so the first frame
        // already has the creator's mix; a reel without one lets it go.
        val track = reel?.let(soundTrackFor)
        if (player != null && track != null) sound.attach(player, track, reel.soundMix()) else sound.detach()
        reel?.let { actions.onShown(it, player?.let(::watchProbe), current) }
        // The pause is the reel's it was made on, and onShown has cleared
        // it; what may still hold the reel is the choice of sound.
        if (soundChoiceRead) pool.playOnly(current)
        listOf(current - 1, current + 1).forEach { index ->
            reelAt(index)?.let(playbackFor)?.let { pool.preload(index, it) }
        }
    }

    // The single-tap pause, and the wait for the viewer's choice of sound,
    // applied to whichever page is settled. A separate effect from the one
    // above, keyed on the answer alone: folding it in would re-run onShown
    // on every tap, and onShown is what CLEARS a pause when the pager moves
    // on. The sound player follows the video, so it is not told here.
    LaunchedEffect(view.mayPlay) {
        if (view.mayPlay) pool.playOnly(pagerState.settledPage) else pool.pauseAll()
    }

    VerticalPager(
        state = pagerState,
        beyondViewportPageCount = 1,
        modifier = Modifier
            .fillMaxSize()
            .background(UsTheme.extended.stage),
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
                val relationship = reelRelationship(item)
                ReelPage(
                    item = item,
                    playback = playbackFor(item),
                    overlay = overlays[item.id] ?: EngagementOverlay(),
                    relationship = relationship,
                    offersRelationship = offersReelRelationship(relationship, ownUserId, followEdges, subscriptionEdges),
                    soundLine = soundLine(item, item.isOwnedBy(ownUserId)),
                    pool = pool,
                    sound = sound,
                    page = page,
                    settled = pagerState.settledPage == page,
                    view = view,
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
            .background(UsTheme.extended.stage)
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
                color = UsTheme.extended.onMedia,
                trackColor = UsTheme.extended.onMedia.copy(alpha = LOADER_TRACK_ALPHA),
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
                    color = UsTheme.extended.onMedia,
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
            .background(UsTheme.extended.stage.copy(alpha = STRIP_PLATE_ALPHA))
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .testTag("reel_pending_failure"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = "Couldn't post",
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.onMedia,
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
        color = UsTheme.extended.onMedia.copy(alpha = DIM_ALPHA),
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
    relationship: ReelRelationship,
    offersRelationship: Boolean,
    /** What the sound line under the hashtags says, or null when there is none. */
    soundLine: SoundLine?,
    pool: PlayerPool,
    sound: ReelSoundPlayer,
    page: Int,
    /** The pager has settled on THIS page: it is the one playing, and the one the progress line follows. */
    settled: Boolean,
    view: ReelsViewState,
    actions: ReelActions,
) {
    val chrome = view.chrome
    // How far the reel has played, 0..1, read off the player while this page
    // is settled and playing; the author's avatar ring draws it.
    var progress by remember(item.id) { mutableFloatStateOf(0f) }
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
                detectTapGestures(onTap = { actions.onTogglePause() }, onDoubleTap = { actions.onToggleMode() })
            },
    ) {
        if (playback != null) {
            ReelVideo(
                playback = playback,
                mixed = settled && item.sound != null,
                pool = pool,
                sound = sound,
                page = page,
                polling = settled && !view.paused,
                view = view,
                offline = view.offline.copies[item.id],
                onProgress = { progress = it },
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
                relationship = relationship,
                offersRelationship = offersRelationship,
                soundLine = soundLine,
                progress = progress,
                actions = ReelOverlayActions(
                    onOpenAuthor = actions.onOpenAuthor,
                    onRelationship = {
                        when (relationship) {
                            is ReelRelationship.Follow -> actions.onFollow(relationship.ref)
                            is ReelRelationship.Subscribe -> actions.onSubscribe(relationship.ref)
                        }
                    },
                    onOpenHashtag = actions.onOpenHashtag,
                    onSoundLine = { actions.onSoundLine(item) },
                ),
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
                muted = view.muted,
                actions = actions,
            )
        }
    }
}

/**
 * The video of one page: the pool's player for it, its volume and quality,
 * the surface it draws on, the buffering indicator, and the progress poll.
 *
 * [mixed] is the settled page of a reel that plays an added sound: its
 * volume is then set by the sound player — the creator's mix, from the
 * planner — and by nothing else. Every other page follows the mute switch
 * alone, by the same rule the planner's answer is applied with.
 */
@OptIn(UnstableApi::class)
@Suppress("LongParameterList") // One fact per thing the video needs.
@Composable
private fun ReelVideo(
    playback: Playback,
    mixed: Boolean,
    pool: PlayerPool,
    sound: ReelSoundPlayer,
    page: Int,
    polling: Boolean,
    view: ReelsViewState,
    /** Where this reel's offline copy stands, for the mark at the top left of the page. */
    offline: OfflineEntry?,
    onProgress: (Float) -> Unit,
) {
    val player = remember(page, playback) { pool.acquire(page, playback) }
    LaunchedEffect(view.muted, player, mixed) {
        if (!mixed) player.volume = appliedVolume(PLAIN_LEVEL, view.muted)
    }
    // The session's quality on every player as it is prepared — the
    // neighbours too, so a swipe lands on the chosen rung, not on a
    // beat of Auto before it corrects.
    LaunchedEffect(view.quality, player) { player.applyQuality(view.quality) }
    // SURFACE_VIEW, not TEXTURE_VIEW. A TextureView goes through the
    // view hierarchy's compositor, costing a full-screen copy every
    // frame; the difference is visible on mid-range hardware and this
    // is the surface the whole native migration was justified by.
    PlayerSurface(
        player = player,
        surfaceType = SURFACE_TYPE_SURFACE_VIEW,
        modifier = Modifier.fillMaxSize(),
    )
    // A reel that has not drawn its first frame is a BLACK page —
    // there is no poster behind a reel — so this is the surface the
    // "stuck" complaint was loudest about. It only ever draws on the
    // page the viewer is on: the pool preloads the neighbours with
    // playWhenReady false, and a paused reel shows the play glyph
    // instead. Retry re-prepares the player where it stands.
    VideoLoadingIndicator(player = player, onRetry = player::prepare)
    // An offline copy says so, and one being saved shows how far it is (2026-10-02).
    ReelOfflineStatus(playsOffline = playback.kind == PlaybackKind.Offline, entry = offline)
    // The same four-a-second poll is the sound's running clock: a
    // drift too small to hear is left alone, a larger one corrected.
    TrackProgress(player = player, polling = polling) {
        onProgress(it)
        sound.tick()
    }
}

/**
 * The playhead, as a 2dp line along the very bottom of the frame (YouTube
 * Shorts; founder, 2026-09-04): white at 25% for the track, the ember
 * gradient for what has played. Read from the player four times a second
 * while [polling] — this page settled and not paused — and left where it
 * was otherwise, so a paused reel shows the line where it stopped under the
 * centred glyph. The pager's page ends where the shell's bar begins, so the
 * line sits above the bar in normal mode and on the screen's edge in full
 * mode without knowing which it is in.
 */
@Composable
private fun TrackProgress(player: Player, polling: Boolean, onProgress: (Float) -> Unit) {
    LaunchedEffect(player, polling) {
        while (polling && isActive) {
            onProgress(progressFraction(player.currentPosition, player.duration))
            delay(PROGRESS_POLL_MILLIS)
        }
    }
}

/**
 * The playhead as a ring around the author's avatar: a faint white track,
 * and the ember arc starting at twelve o'clock and sweeping clockwise as
 * the reel plays — the avatar reads as a tiny player (founder, 2026-09-05,
 * in place of the line along the bottom of the frame).
 */
@Composable
private fun ProgressRing(progress: Float, modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    val track = UsTheme.extended.onMedia.copy(alpha = PROGRESS_TRACK_ALPHA)
    val played = UsTheme.extended.ctaGradient
    Box(
        modifier = modifier
            .drawBehind {
                val stroke = RING_STROKE.toPx()
                val inset = stroke / 2
                val arcSize = Size(size.width - stroke, size.height - stroke)
                drawArc(
                    color = track,
                    startAngle = RING_START_ANGLE,
                    sweepAngle = FULL_SWEEP,
                    useCenter = false,
                    topLeft = Offset(inset, inset),
                    size = arcSize,
                    style = Stroke(width = stroke),
                )
                if (progress > 0f) {
                    drawArc(
                        brush = played,
                        startAngle = RING_START_ANGLE,
                        sweepAngle = FULL_SWEEP * progress.coerceIn(0f, 1f),
                        useCenter = false,
                        topLeft = Offset(inset, inset),
                        size = arcSize,
                        style = Stroke(width = stroke, cap = StrokeCap.Round),
                    )
                }
            }
            .padding(RING_STROKE + RING_GAP)
            .testTag("reel_progress"),
        contentAlignment = Alignment.Center,
    ) {
        content()
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
            .background(UsTheme.extended.stage.copy(alpha = PAUSE_DISC_ALPHA))
            .semantics { contentDescription = "Paused" }
            .testTag("reel_paused"),
    ) {
        Icon(
            imageVector = UsIcons.Play,
            contentDescription = null,
            tint = UsTheme.extended.onMedia,
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
            .background(
                Brush.verticalGradient(
                    listOf(Color.Transparent, UsTheme.extended.stage.copy(alpha = SCRIM_ALPHA)),
                ),
            ),
    )
}

/**
 * The vertical control strip over a reel, YouTube Shorts' idiom on
 * Instagram's order (founder, 2026-09-04, "combine both"): like, comment,
 * share, save — each glyph with a one-line label under it, the count
 * where there is one (share and save too, when the row carries theirs,
 * 2026-09-30) — then mute on its own, unlabelled. The ⋮ left the
 * rail for the header's More (founder, 2026-09-05). 56dp from the
 * bottom, 20dp between controls. Plain white glyphs on the bottom scrim —
 * no discs; the scrim carries the contrast for the whole strip.
 *
 * Which controls, in what order, saying what, is [railControls]'s rule;
 * this only draws it. Comment and share follow the author's switches.
 */
@Composable
private fun ReelActionRail(
    item: FeedItem,
    overlay: EngagementOverlay,
    muted: Boolean,
    actions: ReelActions,
    modifier: Modifier = Modifier,
) {
    val reacted = overlay.reactedOr(item.viewer.hasReacted)
    val bookmarked = overlay.bookmarkedOr(item.viewer.isBookmarked)
    val controls = railControls(
        controls = item.controls,
        likes = overlay.likeCountOr(item.counts.likes, item.viewer.hasReacted),
        comments = item.counts.comments,
        saved = bookmarked,
        shares = item.counts.shares,
        saves = layeredSaves(item.counts.saves, serverSaved = item.viewer.isBookmarked, saved = bookmarked),
    )
    Column(
        modifier = modifier
            .padding(end = UsTheme.spacing.m, bottom = RAIL_BOTTOM_INSET)
            .testTag("reel_rail"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(RAIL_GAP),
    ) {
        controls.forEach { control ->
            RailControlButton(
                control = control,
                item = item,
                reacted = reacted,
                bookmarked = bookmarked,
                actions = actions,
            )
        }
        RailButton(
            icon = if (muted) UsIcons.SoundOff else UsIcons.SoundOn,
            description = if (muted) "Unmute" else "Mute",
            label = null,
            onClick = actions.onToggleMute,
        )
    }
}

/** One rail control drawn from its [RailControl]: the glyph and tint by kind and state, the label as given. */
@Composable
private fun RailControlButton(
    control: RailControl,
    item: FeedItem,
    reacted: Boolean,
    bookmarked: Boolean,
    actions: ReelActions,
) {
    when (control.kind) {
        RailKind.LIKE -> RailButton(
            icon = if (reacted) UsIcons.HeartFilled else UsIcons.HeartOutline,
            description = if (reacted) "Liked" else "Like",
            label = control.label,
            tint = if (reacted) UsTheme.extended.liveRed else UsTheme.extended.onMedia,
            onClick = { actions.onReact(item.id, item.viewer.hasReacted) },
        )
        RailKind.COMMENT -> RailButton(
            icon = UsIcons.Comment,
            description = "Comments",
            label = control.label,
            onClick = { actions.onComment(item.id) },
        )
        RailKind.SHARE -> RailButton(
            icon = UsIcons.Share,
            description = "Share",
            label = control.label,
            onClick = { actions.onShare(item) },
        )
        RailKind.SAVE -> RailButton(
            icon = if (bookmarked) UsIcons.BookmarkFilled else UsIcons.BookmarkOutline,
            description = if (bookmarked) "Saved" else "Save",
            label = control.label,
            tint = if (bookmarked) UsTheme.extended.accent else UsTheme.extended.onMedia,
            onClick = { actions.onBookmark(item.id, item.viewer.isBookmarked) },
        )
    }
}

/**
 * A 28dp glyph over an 11sp Figtree label, one line. The label is what the
 * control says about itself — the count, or its name — and the description
 * is what a screen reader says, which folds the two into one phrase.
 */
@Composable
private fun RailButton(
    icon: ImageVector,
    description: String,
    label: String?,
    onClick: () -> Unit,
    tint: Color = UsTheme.extended.onMedia,
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = Modifier
            .pressScale(onClick)
            .sizeIn(minWidth = RAIL_TARGET, minHeight = RAIL_TARGET)
            .clearAndSetSemantics {
                contentDescription = when (label) {
                    null, description -> description
                    else -> "$description, $label"
                }
                role = Role.Button
            }
            .testTag("reel_rail:${description.lowercase()}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = tint,
            modifier = Modifier.size(RAIL_ICON),
        )
        if (label != null) {
            Text(
                text = label,
                style = MaterialTheme.typography.labelMedium,
                fontSize = RAIL_LABEL_SIZE,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.onMedia,
                maxLines = 1,
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
 * Bottom-left, YouTube Shorts' row (founder, 2026-09-04): a 36dp avatar,
 * "@username" at 15sp, the WHITE relationship pill (Follow, or Subscribe
 * when the reel carries its author's channel; only when the viewer is
 * known not to have the edge, never on the viewer's own reel, which is
 * [offersReelRelationship]'s rule), then the reel's title, the caption
 * clamped to two lines with "more" that opens it in place, the hashtags as
 * chips that open that tag's posts, and the sound line (2026-09-30).
 *
 * The sound line is a note and a name, and it is drawn only when it can be
 * followed ([soundLine]): an added sound opens its page; "Original sound -
 * <creator>" makes the reel's own audio a sound and then opens its page.
 *
 * Holds no rail controls. Text and targets interleaved in one column made
 * the caption look tappable and the buttons look like part of the sentence.
 */
@Composable
private fun ReelOverlay(
    item: FeedItem,
    relationship: ReelRelationship,
    offersRelationship: Boolean,
    soundLine: SoundLine?,
    /** 0..1 of the reel played; drawn as the ring around the avatar. */
    progress: Float,
    actions: ReelOverlayActions,
    modifier: Modifier = Modifier,
) {
    val username = reelAuthorLabel(item.author.username, item.author.nameForDisplay)
    val onOpenAuthor = actions.onOpenAuthor
    val onRelationship = actions.onRelationship
    val title = reelTitle(item)
    val hashtags = reelHashtags(item)
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
            // The avatar and the name are the way to the author's profile;
            // the ring around the avatar is the playhead.
            ProgressRing(progress = progress) {
                UsAvatar(
                    name = item.author.nameForDisplay,
                    seed = item.author.id,
                    size = UsAvatarSize.Post,
                    modifier = Modifier.clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                    ) { onOpenAuthor(item.author.id) },
                )
            }
            Text(
                text = username,
                style = MaterialTheme.typography.bodyMedium,
                fontSize = NAME_SIZE,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.onMedia,
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
            if (offersRelationship) {
                FollowPill(label = relationship.label, onClick = onRelationship)
            }
        }
        if (title != null) {
            Text(
                text = title,
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.onMedia,
                maxLines = TITLE_LINES,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.testTag("reel_title"),
            )
        }
        if (item.text.isNotBlank()) ReelCaption(item = item)
        if (hashtags.isNotEmpty()) ReelHashtags(tags = hashtags, onOpenHashtag = actions.onOpenHashtag)
        if (soundLine != null) ReelSoundLine(line = soundLine, onClick = actions.onSoundLine)
    }
}

/** The caption, clamped to two lines with "more" that opens it in place and "less" that folds it. */
@Composable
private fun ReelCaption(item: FeedItem) {
    var expanded by rememberSaveable(item.id) { mutableStateOf(false) }
    var overflowed by remember(item.id) { mutableStateOf(false) }
    Text(
        text = item.text,
        style = MaterialTheme.typography.bodyMedium,
        fontSize = CAPTION_SIZE,
        color = UsTheme.extended.onMedia,
        maxLines = if (expanded) Int.MAX_VALUE else CAPTION_LINES,
        overflow = TextOverflow.Ellipsis,
        onTextLayout = { if (!expanded) overflowed = it.hasVisualOverflow },
    )
    if (overflowed || expanded) {
        Text(
            text = if (expanded) "less" else "more",
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.onMedia.copy(alpha = DIM_ALPHA),
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

/** What the author block can ask for: the author, the relationship, a tag's posts, the sound. */
internal class ReelOverlayActions(
    val onOpenAuthor: (String) -> Unit,
    val onRelationship: () -> Unit,
    val onOpenHashtag: (tag: String) -> Unit,
    val onSoundLine: () -> Unit,
)

/**
 * The reel's hashtags, one line of chips that scrolls sideways; a chip opens
 * that tag's posts. Material's chip, coloured from the over-media tokens: a
 * faint white plate and white type, because what is under it is the video
 * and its scrim, never the theme's surface.
 */
@Composable
private fun ReelHashtags(tags: List<String>, onOpenHashtag: (tag: String) -> Unit, modifier: Modifier = Modifier) {
    Row(
        modifier = modifier
            .horizontalScroll(rememberScrollState())
            .testTag("reel_hashtags"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        tags.forEach { tag ->
            SuggestionChip(
                onClick = { onOpenHashtag(tag) },
                label = {
                    Text(
                        text = hashtagLabel(tag),
                        style = MaterialTheme.typography.labelMedium,
                        maxLines = 1,
                    )
                },
                shape = RoundedCornerShape(UsTheme.radii.full),
                colors = SuggestionChipDefaults.suggestionChipColors(
                    containerColor = UsTheme.extended.onMedia.copy(alpha = CHIP_PLATE_ALPHA),
                    labelColor = UsTheme.extended.onMedia,
                ),
                border = null,
                modifier = Modifier
                    .height(CHIP_HEIGHT)
                    .testTag("reel_hashtag:$tag"),
            )
        }
    }
}

/**
 * A note and the sound's name, small, on one line. The whole line is the
 * target: it opens the sound's page, making the sound first when the reel
 * plays only its own audio.
 */
@Composable
private fun ReelSoundLine(line: SoundLine, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Row(
        modifier = modifier
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                role = Role.Button,
                onClickLabel = if (line.added) "Open sound" else "Use this sound",
                onClick = onClick,
            )
            .sizeIn(minHeight = SOUND_LINE_TARGET)
            .semantics(mergeDescendants = true) { contentDescription = "Sound: ${line.label}" }
            .testTag(if (line.added) "reel_sound_line:added" else "reel_sound_line:original"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Icon(
            imageVector = UsIcons.Music,
            contentDescription = null,
            tint = UsTheme.extended.onMedia,
            modifier = Modifier.size(SOUND_LINE_ICON),
        )
        Text(
            text = line.label,
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.onMedia,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Preview
@Composable
private fun ReelOverlayWithSoundPreview() {
    UsTheme {
        Box(modifier = Modifier.background(UsTheme.extended.stage)) {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                ReelHashtags(tags = listOf("reels", "monsoon", "walk"), onOpenHashtag = {})
                ReelSoundLine(line = SoundLine(label = "Original sound - Asha", added = true), onClick = {})
                ReelSoundLine(line = SoundLine(label = "Original sound - Ravi", added = false), onClick = {})
            }
        }
    }
}

/**
 * Shorts' solid Follow: a white pill, 32dp tall, the label in Momentum's
 * navy — the canvas token, which is the navy the whole design sits on — at
 * 14sp semibold. Solid rather than outlined because over video a hairline
 * disappears into a bright frame and the one control that grows the
 * viewer's feed must never do that.
 */
@Composable
private fun FollowPill(label: String, onClick: () -> Unit) {
    // The design system's one follow button — ember here as on the post
    // header, so the same action never wears two colours (founder,
    // 2026-09-04). Solid, so it never sinks into a bright frame. The label
    // is the relationship's: Follow, or Subscribe toward a channel.
    UsFollowButton(text = label, onClick = onClick, modifier = Modifier.testTag("reel_follow"))
}

/**
 * Pauses on background and releases on destroy.
 *
 * Both halves matter. Without the pause, audio keeps playing over whatever the
 * user switched to. Without the release, every visit to this screen leaks four
 * decoder sessions, and the device exhausts them long before the process ends.
 */
@Composable
private fun ReleaseOnLifecycle(pool: PlayerPool, sound: ReelSoundPlayer) {
    val owner = LocalLifecycleOwner.current
    DisposableEffect(owner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) {
                pool.pauseAll()
                sound.pause()
            }
        }
        owner.lifecycle.addObserver(observer)
        onDispose {
            owner.lifecycle.removeObserver(observer)
            // The sound first: letting it go gives the video back at its
            // plain level, which needs the video's player still alive.
            sound.release()
            pool.release()
        }
    }
}

/** Instagram clamps the reel caption to two lines before "more". */
private const val CAPTION_LINES = 2

/** The title is a line, not a paragraph: the caption under it is where the words go. */
private const val TITLE_LINES = 1

/** The level of a page that plays no added sound: the viewer's, in full. */
private const val PLAIN_LEVEL = 1.0

/** A hashtag chip: 32dp tall on a white plate at 16%. */
private val CHIP_HEIGHT = 32.dp
private const val CHIP_PLATE_ALPHA = 0.16f

/** The sound line: a 14dp note, and a row tall enough to be tapped on purpose. */
private val SOUND_LINE_ICON = 14.dp
private val SOUND_LINE_TARGET = 32.dp

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

/** The label under each rail glyph: Figtree 11sp semibold, one line. */
private val RAIL_LABEL_SIZE = 11.sp

private val OVERLAY_SIDE = 16.dp
private val OVERLAY_BOTTOM = 56.dp

/** The overlay stops short of the rail so a long name never runs under it. */
private val OVERLAY_RAIL_CLEARANCE = 72.dp
private val NAME_SIZE = 15.sp
private val CAPTION_SIZE = 14.sp

/** The playhead line: 2dp, the track at 25% white, read four times a second. */
private val RING_STROKE = 3.dp
private val RING_GAP = 2.dp
private const val RING_START_ANGLE = -90f
private const val FULL_SWEEP = 360f
private const val PROGRESS_TRACK_ALPHA = 0.25f
private const val PROGRESS_POLL_MILLIS = 250L

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

/**
 * What a reel's page says about its offline copy (2026-10-02), under the
 * header at the top left: "Offline copy" while the stored copy is what
 * plays, the save's ring while one is being made, nothing otherwise. The
 * same two marks as the long video's watch screen.
 */
@Composable
private fun ReelOfflineStatus(playsOffline: Boolean, entry: OfflineEntry?, modifier: Modifier = Modifier) {
    val place = modifier
        .statusBarsPadding()
        .padding(start = UsTheme.spacing.pageHorizontal, top = OFFLINE_STATUS_TOP)
    when {
        playsOffline -> OfflineCopyBadge(modifier = place)
        entry == null || entry.phase == OfflinePhase.STORED -> Unit
        else -> OfflineSaveRing(
            progress = entry.progress,
            waiting = WAITING_FOR_NETWORK.takeIf { entry.phase == OfflinePhase.WAITING },
            modifier = place,
        )
    }
}

/** Clear of the header's two glyphs. */
private val OFFLINE_STATUS_TOP = 56.dp
