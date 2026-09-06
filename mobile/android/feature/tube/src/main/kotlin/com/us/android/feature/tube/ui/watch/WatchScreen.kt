package com.us.android.feature.tube.ui.watch

import android.content.pm.ActivityInfo
import android.content.res.Configuration
import androidx.activity.compose.BackHandler
import androidx.activity.compose.LocalActivity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.platform.testTag
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.Player
import androidx.media3.common.Tracks
import com.us.android.core.analytics.AnalyticsSurface
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.feed.data.offersFollow
import com.us.android.core.feed.ui.comments.CommentsSheet
import com.us.android.core.feed.ui.more.PostMoreSheetHost
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.media.PlaybackKind
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.HideShellBottomBar
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.reelQualityOptions
import com.us.android.core.ui.rememberPostSharer

/**
 * The watch screen (Tube, 2026-09-05): the 16:9 player pinned at the top,
 * everything about the video scrolling under it — title, author, actions,
 * description, comments, "Up next". Fullscreen turns the phone sideways and
 * gives the player the whole screen; Back brings it upright.
 *
 * The shell's bar is asked to go, as Reels' full mode asks: a pushed route
 * has none anyway, and asking makes the intent explicit. Comments and the
 * post "more" sheet are the app's shared ones ([CommentsSheet],
 * [PostMoreSheetHost]) — never a second copy — and More is opened with
 * `suggested = false`: a video the viewer chose to open is not a
 * recommendation to say "Interested" about.
 */
@Composable
fun WatchScreen(
    onBack: () -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    viewModel: WatchViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
) {
    val content by viewModel.content.collectAsStateWithLifecycle()
    val upNext by viewModel.upNext.collectAsStateWithLifecycle()
    val overlays by viewModel.overlays.collectAsStateWithLifecycle()
    val followEdges by viewModel.followEdges.collectAsStateWithLifecycle()
    val moreMessage by more.message.collectAsStateWithLifecycle()
    var fullscreen by rememberSaveable { mutableStateOf(false) }
    val sheets = remember { WatchSheets() }
    val landscape = LocalConfiguration.current.orientation == Configuration.ORIENTATION_LANDSCAPE
    val playhead = rememberPlayhead(viewModel.player, polling = true)
    val share = rememberPostSharer()
    val onShare: (FeedItem) -> Unit = { item ->
        share(item.title.ifBlank { item.text }, item.author.nameForDisplay)
        viewModel.onExternalShared(item.id)
    }

    HideShellBottomBar(hidden = true)
    FullscreenMode(fullscreen)
    PauseOnBackground(viewModel)
    BackHandler(enabled = fullscreen) { fullscreen = false }

    val transport = remember(viewModel, onBack) {
        WatchTransport(
            onTogglePlay = viewModel::togglePlay,
            onSeekBy = viewModel::seekBy,
            onSeekTo = viewModel::seekTo,
            onToggleFullscreen = { fullscreen = !fullscreen },
            onOpenSettings = { sheets.settingsOpen = true },
            onBack = { if (fullscreen) fullscreen = false else onBack() },
        )
    }
    val actions = remember(viewModel, onOpenAuthor, onShare) {
        WatchDetailsActions(
            onOpenAuthor = onOpenAuthor,
            onFollow = viewModel::onFollow,
            onReact = viewModel::onReact,
            onBookmark = viewModel::onBookmark,
            onComment = { sheets.commentsFor = it },
            onShare = onShare,
            onMore = { sheets.moreFor = it },
            onOpenVideo = { viewModel.open(it.id) },
        )
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(UsTheme.extended.bgCanvas)
            .testTag("watch_screen"),
    ) {
        WatchBody(
            content = content,
            fullscreen = fullscreen || landscape,
            playhead = playhead,
            transport = transport,
            upNext = upNext,
            overlays = overlays,
            followEdges = followEdges,
            viewModel = viewModel,
            actions = actions,
        )
        UsMessageHost(message = moreMessage, onDismiss = more::dismissMessage)
    }

    WatchSheetsHost(
        sheets = sheets,
        content = content,
        viewModel = viewModel,
        more = more,
        overlays = overlays,
        followEdges = followEdges,
        onShare = onShare,
    )
}

/**
 * Which of the three sheets is up: the gear's, comments for a post, the
 * "more" sheet for a post. One holder rather than three screen locals so
 * the bundles above can open them without the screen function growing a
 * line per sheet. Screen-local: the Activity handles rotation itself, and
 * a sheet does not need to outlive the process.
 */
@Stable
private class WatchSheets {
    var settingsOpen by mutableStateOf(false)
    var commentsFor by mutableStateOf<String?>(null)
    var moreFor by mutableStateOf<FeedItem?>(null)
}

/** The three sheets, mounted over the screen when asked for. */
@Suppress("LongParameterList")
@Composable
private fun WatchSheetsHost(
    sheets: WatchSheets,
    content: WatchContent,
    viewModel: WatchViewModel,
    more: PostMoreViewModel,
    overlays: Map<String, EngagementOverlay>,
    followEdges: Map<String, FollowStatus>,
    onShare: (FeedItem) -> Unit,
) {
    val quality by viewModel.quality.collectAsStateWithLifecycle()
    val speed by viewModel.speed.collectAsStateWithLifecycle()
    val trackHeights = rememberVideoHeights(viewModel.player)
    if (sheets.settingsOpen) {
        val playback = (content as? WatchContent.Ready)?.playback
        WatchSettingsSheet(
            qualities = reelQualityOptions(heights = trackHeights, adaptive = playback?.kind == PlaybackKind.Hls),
            selectedQuality = quality,
            speed = speed,
            onSelectQuality = viewModel::selectQuality,
            onSelectSpeed = viewModel::selectSpeed,
            onDismiss = { sheets.settingsOpen = false },
        )
    }
    sheets.commentsFor?.let { postId ->
        CommentsSheet(postId = postId, onDismiss = { sheets.commentsFor = null })
    }
    sheets.moreFor?.let { item ->
        PostMoreSheetHost(
            item = item,
            overlay = overlays[item.id] ?: EngagementOverlay(),
            followEdge = followEdges[item.author.id],
            ownUserId = viewModel.ownUserId,
            onShare = onShare,
            onDismiss = { sheets.moreFor = null },
            viewModel = more,
            suggested = false,
            surface = AnalyticsSurface.POSTTUBE,
        )
    }
}

/** The player above the details in portrait; the player alone in fullscreen. */
@Suppress("LongParameterList")
@Composable
private fun WatchBody(
    content: WatchContent,
    fullscreen: Boolean,
    playhead: Playhead,
    transport: WatchTransport,
    upNext: List<FeedItem>,
    overlays: Map<String, EngagementOverlay>,
    followEdges: Map<String, FollowStatus>,
    viewModel: WatchViewModel,
    actions: WatchDetailsActions,
) {
    if (fullscreen) {
        PlayerOrState(content, playhead, fullscreen = true, transport, viewModel, modifier = Modifier.fillMaxSize())
        return
    }
    Column(modifier = Modifier.fillMaxSize()) {
        PlayerOrState(
            content = content,
            playhead = playhead,
            fullscreen = false,
            transport = transport,
            viewModel = viewModel,
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(LANDSCAPE),
        )
        val item = (content as? WatchContent.Ready)?.item ?: return
        LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .testTag("watch_details"),
        ) {
            watchDetails(
                item = item,
                overlay = overlays[item.id] ?: EngagementOverlay(),
                offersFollow = offersFollow(viewModel.ownUserId, item.author.id, followEdges[item.author.id]),
                upNext = upNext,
                thumbFor = viewModel::thumb,
                actions = actions,
            )
        }
    }
}

/**
 * The player, or what stands in for it: a loader while the post is fetched,
 * "still processing" for a video with nothing to play yet, the failure
 * with Back as the way out.
 */
@Suppress("LongParameterList")
@Composable
private fun PlayerOrState(
    content: WatchContent,
    playhead: Playhead,
    fullscreen: Boolean,
    transport: WatchTransport,
    viewModel: WatchViewModel,
    modifier: Modifier = Modifier,
) {
    when (content) {
        WatchContent.Loading -> UsLoadingState(label = "Loading video", modifier = modifier)
        is WatchContent.Failed -> UsErrorState(message = content.message, modifier = modifier, onRetry = null)
        is WatchContent.Ready -> if (content.playback == null) {
            UsEmptyState(
                title = "Still processing",
                detail = "This video isn't ready to play yet.",
                modifier = modifier,
            )
        } else {
            WatchPlayer(
                player = viewModel.player,
                playhead = playhead,
                fullscreen = fullscreen,
                transport = transport,
                modifier = modifier,
            )
        }
    }
}

/**
 * Fullscreen is an orientation: the Activity is turned to landscape and the
 * system bars are hidden (a swipe brings them back for a moment); leaving it
 * turns the Activity upright and shows the bars. When the screen is left
 * altogether the orientation is handed back to the sensor, whatever it was.
 * The Activity handles orientation changes itself (no recreation), so the
 * ViewModel's player plays straight through the turn.
 */
@Composable
private fun FullscreenMode(fullscreen: Boolean) {
    val activity = LocalActivity.current ?: return
    val view = LocalView.current
    DisposableEffect(fullscreen) {
        val controller = WindowCompat.getInsetsController(activity.window, view)
        if (fullscreen) {
            activity.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
            controller.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            controller.hide(WindowInsetsCompat.Type.systemBars())
        } else {
            activity.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_PORTRAIT
            controller.show(WindowInsetsCompat.Type.systemBars())
        }
        onDispose { controller.show(WindowInsetsCompat.Type.systemBars()) }
    }
    DisposableEffect(activity) {
        onDispose { activity.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED }
    }
}

/** The app went behind something: hold the frame and report the playhead. */
@Composable
private fun PauseOnBackground(viewModel: WatchViewModel) {
    val owner = LocalLifecycleOwner.current
    DisposableEffect(owner, viewModel) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) viewModel.onBackground()
        }
        owner.lifecycle.addObserver(observer)
        onDispose { owner.lifecycle.removeObserver(observer) }
    }
}

/**
 * The heights of every playable video track the player knows about, kept
 * current: read when the player changes hands and again on every
 * `onTracksChanged`, which is when an HLS master's ladder becomes known.
 */
@Composable
private fun rememberVideoHeights(player: Player): List<Int> {
    var heights by remember(player) { mutableStateOf(player.currentTracks.videoHeights()) }
    DisposableEffect(player) {
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

private const val LANDSCAPE = 16f / 9f
