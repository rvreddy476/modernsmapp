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
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.blur
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
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
import coil3.compose.AsyncImage
import com.us.android.core.analytics.AnalyticsSurface
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.ui.action.ActionPill
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.likeCountOr
import com.us.android.core.feed.data.VideoLibraryState
import com.us.android.core.feed.data.offersSubscribe
import com.us.android.core.feed.data.videoMediaId
import com.us.android.core.feed.offline.OfflineState
import com.us.android.core.feed.ui.audio.AudioTracksSheet
import com.us.android.core.feed.ui.comments.CommentsSheet
import com.us.android.core.feed.ui.edit.PostEditSheet
import com.us.android.core.feed.ui.more.PostMoreSheetHost
import com.us.android.core.feed.ui.more.PostMoreViewModel
import com.us.android.core.feed.ui.share.PostShareSheetHost
import com.us.android.core.feed.ui.share.PostShareViewModel
import com.us.android.core.media.PlaybackKind
import com.us.android.core.media.ui.CaptionsOverlay
import com.us.android.core.memberships.ui.MembersOnlyLock
import com.us.android.core.memberships.ui.MembersOnlyLockStyle
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.HideShellBottomBar
import com.us.android.core.ui.PLAYBACK_SPEEDS
import com.us.android.core.ui.UsCaptionOption
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsLongVideoMoreState
import com.us.android.core.ui.UsPostDeleteState
import com.us.android.core.ui.UsReelMoreState
import com.us.android.core.ui.reelQualityOptions
import com.us.android.core.ui.rememberPostSharer
import com.us.android.feature.tube.data.SeriesInfo
import com.us.android.feature.tube.ui.collections.CollectionPickerSheet

/**
 * The watch screen (Tube, 2026-09-05): the 16:9 player pinned at the top,
 * everything about the video scrolling under it — title, author, actions,
 * description, comments, "In this series", "Up next". Fullscreen turns the
 * phone sideways and gives the player the whole screen; Back brings it
 * upright. When the video ends the player is covered by the countdown to
 * the next episode, or by the end screen ([WatchEndScreen]) when there is
 * nothing to count down to.
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
    /** A credited name (video credits, 2026-10-23): Tube search for it. */
    onSearchName: (name: String) -> Unit = {},
    /** An accepted credit: that person's channel inside Tube. */
    onOpenChannel: (userId: String) -> Unit = {},
    viewModel: WatchViewModel = hiltViewModel(),
    more: PostMoreViewModel = hiltViewModel(),
    // The Share sheet's (2026-10-17): the screen's, so "Sent" is read here after the sheet has gone.
    sharing: PostShareViewModel = hiltViewModel(),
) {
    val content by viewModel.content.collectAsStateWithLifecycle()
    val upNext by viewModel.upNext.collectAsStateWithLifecycle()
    val series by viewModel.series.collectAsStateWithLifecycle()
    val countdown by viewModel.countdown.collectAsStateWithLifecycle()
    val overlays by viewModel.overlays.collectAsStateWithLifecycle()
    val followEdges by viewModel.followEdges.collectAsStateWithLifecycle()
    val subscriptionEdges by viewModel.subscriptionEdges.collectAsStateWithLifecycle()
    val subscribeBusy by viewModel.subscribeBusy.collectAsStateWithLifecycle()
    val ended by viewModel.ended.collectAsStateWithLifecycle()
    val moreMessage by more.message.collectAsStateWithLifecycle()
    val library by viewModel.library.collectAsStateWithLifecycle()
    val actionMessage by viewModel.actionMessage.collectAsStateWithLifecycle()
    val creditMessage by viewModel.creditMessage.collectAsStateWithLifecycle()
    val offlineState by viewModel.offlineState.collectAsStateWithLifecycle()
    var fullscreen by rememberSaveable { mutableStateOf(false) }
    val sheets = remember { WatchSheets() }
    val landscape = LocalConfiguration.current.orientation == Configuration.ORIENTATION_LANDSCAPE
    val playhead = rememberPlayhead(viewModel.player, polling = true)
    val shareMessage by sharing.message.collectAsStateWithLifecycle()
    val share = rememberPostSharer()
    // The More sheet's Share opens Momentum's Share sheet (2026-10-17), as
    // Reels' does; its "More…" is the system chooser, counted once launched.
    val onShare: (FeedItem) -> Unit = { item -> sheets.shareFor = item }
    val onSystemShare: (FeedItem) -> Unit = { item ->
        share(item.id, item.title.ifBlank { item.text }, item.author.nameForDisplay)
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
    val genres by viewModel.genres.collectAsStateWithLifecycle()
    val actions = remember(viewModel, onOpenAuthor, genres) {
        WatchDetailsActions(
            onSearchName = onSearchName,
            onOpenCreditedUser = onOpenChannel,
            genreLabel = { slug -> genres.firstOrNull { it.slug == slug }?.label },
            ownUserId = viewModel.ownUserId,
            onRemoveMeFromCredit = viewModel::removeMeFromCredit,
            onOpenAuthor = onOpenAuthor,
            onSubscribe = viewModel::onSubscribe,
            onUnsubscribe = viewModel::onUnsubscribe,
            onToggleNotify = viewModel::onToggleNotify,
            onReact = viewModel::onReact,
            onDislike = viewModel::onDislike,
            onWatchLater = viewModel::onWatchLater,
            onAddToCollection = { sheets.collectionFor = it.id },
            onBookmark = viewModel::onBookmark,
            // The row's Share (2026-10-19: out of More, onto the row).
            onShare = onShare,
            onComment = { sheets.commentsFor = it },
            onMore = { sheets.moreFor = it },
            onOpenVideo = { viewModel.open(it.id) },
            onOpenEpisode = viewModel::open,
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
            series = series,
            countdown = countdown,
            ended = ended,
            overlays = overlays,
            library = library,
            subscriptionEdges = subscriptionEdges,
            subscribeBusy = subscribeBusy,
            offlineState = offlineState,
            viewModel = viewModel,
            actions = actions,
        )
        // A refused Like, Dislike, Watch later or Save first; else what the More or Share sheet left behind.
        UsMessageHost(
            message = actionMessage ?: creditMessage ?: moreMessage ?: shareMessage,
            onDismiss = {
                viewModel.dismissActionMessage()
                viewModel.dismissCreditMessage()
                more.dismissMessage()
                sharing.dismissMessage()
            },
        )
    }

    WatchSheetsHost(
        sheets = sheets,
        content = content,
        viewModel = viewModel,
        more = more,
        overlays = overlays,
        followEdges = followEdges,
        onShare = onShare,
        onBack = onBack,
    )
    sheets.shareFor?.let { item ->
        PostShareSheetHost(
            item = item,
            viewModel = sharing,
            onMore = onSystemShare,
            onDismiss = { sheets.shareFor = null },
        )
    }
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

    /** The post the "Add to collection" sheet is open for. */
    var collectionFor by mutableStateOf<String?>(null)

    /** The post the Share sheet is open for. */
    var shareFor by mutableStateOf<FeedItem?>(null)

    /** The owner's Edit (2026-10-21): the video being edited. */
    var editFor by mutableStateOf<FeedItem?>(null)

    /** The owner's Audio tracks (2026-10-21): the media whose dubs are managed. */
    var audioFor by mutableStateOf<String?>(null)
}

/** The four sheets, mounted over the screen when asked for. */
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
    onBack: () -> Unit,
) {
    val quality by viewModel.quality.collectAsStateWithLifecycle()
    val speed by viewModel.speed.collectAsStateWithLifecycle()
    val autoplayNext by viewModel.autoplayNext.collectAsStateWithLifecycle()
    val captions by viewModel.captions.collectAsStateWithLifecycle()
    val captionLanguage by viewModel.captionLanguage.collectAsStateWithLifecycle()
    val trackHeights = rememberVideoHeights(viewModel.player)
    if (sheets.settingsOpen) {
        val playback = (content as? WatchContent.Ready)?.playback
        WatchSettingsSheet(
            qualities = reelQualityOptions(heights = trackHeights, adaptive = playback?.kind == PlaybackKind.Hls),
            selectedQuality = quality,
            speed = speed,
            autoplayNext = autoplayNext,
            captions = captions,
            captionLanguage = captionLanguage,
            onSelectQuality = viewModel::selectQuality,
            onSelectSpeed = viewModel::selectSpeed,
            onAutoplayNextChange = viewModel::setAutoplayNext,
            onSelectCaption = viewModel::selectCaption,
            onDismiss = { sheets.settingsOpen = false },
        )
    }
    sheets.commentsFor?.let { postId ->
        CommentsSheet(
            postId = postId,
            onDismiss = { sheets.commentsFor = null },
            postAuthorId = (content as? WatchContent.Ready)?.item?.takeIf { it.id == postId }?.let { it.author.id }.orEmpty(),
            postAuthorName = (content as? WatchContent.Ready)?.item?.takeIf { it.id == postId }
                ?.creatorName.orEmpty(),
        )
    }
    sheets.moreFor?.let { item ->
        PostMoreSheetHost(
            item = item,
            overlay = overlays[item.id] ?: EngagementOverlay(),
            followEdge = followEdges[item.author.id],
            ownUserId = viewModel.ownUserId,
            onShare = onShare,
            onDismiss = {
                sheets.moreFor = null
                // The viewer's own video was deleted from the sheet: nothing is left to watch.
                if (more.delete.value == UsPostDeleteState.Deleted) onBack()
            },
            viewModel = more,
            suggested = false,
            surface = AnalyticsSurface.POSTTUBE,
            // The SAME rows as a reel's More (founder, 2026-10-02). The video
            // group feeds Description and Quality: the caption the details
            // show, and the ladder and the choice the gear's sheet has.
            reel = UsReelMoreState(
                description = item.text,
                fullMode = false,
                qualities = reelQualityOptions(
                    heights = trackHeights,
                    adaptive = (content as? WatchContent.Ready)?.playback?.kind == PlaybackKind.Hls,
                ),
                selected = quality,
                shareHidden = item.controls.hideShare,
                // The same Playback speed and Captions rows as a reel's
                // (T-32), from the state the gear's sheet already reads.
                speeds = PLAYBACK_SPEEDS,
                speed = speed,
                captions = captions.map { UsCaptionOption(language = it.language, label = it.label) },
                captionLanguage = captionLanguage,
            ).withAudioOf(viewModel, item),
            onSelectQuality = viewModel::selectQuality,
            onSelectSpeed = viewModel::selectSpeed,
            onSelectCaption = viewModel::selectCaption,
            onSelectAudioTrack = { trackId -> viewModel.selectAudioTrack(item, trackId) },
            // The owner's two context rows, the web's watch page's: Edit and Audio tracks.
            onEdit = { sheets.editFor = it },
            onManageAudio = { video -> sheets.audioFor = video.videoMediaId() },
            // What makes it the long video's: the words that say "video", and the channel a block names.
            longVideo = UsLongVideoMoreState(
                channelName = item.creatorName,
                shareHidden = item.controls.hideShare,
            ),
            // A blocked channel's video is left, as the web leaves it.
            onBlocked = onBack,
            offlineCopies = true,
        )
    }
    sheets.collectionFor?.let { postId ->
        CollectionPickerSheet(postId = postId, onDismiss = { sheets.collectionFor = null })
    }
    OwnerSheets(sheets)
}

/**
 * "Audio track" (2026-10-21): the original and each ready dub, offered once
 * the video has one — to the owner too, as on the web — with the one playing.
 */
@Composable
private fun UsReelMoreState.withAudioOf(viewModel: WatchViewModel, item: FeedItem): UsReelMoreState {
    val audioTracks by viewModel.audioTracks.collectAsStateWithLifecycle()
    val audioLanguage by viewModel.audioLanguage.collectAsStateWithLifecycle()
    return copy(
        audioTracks = remember(audioTracks, item.id) { viewModel.audioOptions(item) },
        audioTrackId = remember(audioTracks, audioLanguage, item.id) { viewModel.audioTrackId(item) },
    )
}

/** The owner's two sheets from More (2026-10-21): Edit, and the dub manager. */
@Composable
private fun OwnerSheets(sheets: WatchSheets) {
    sheets.editFor?.let { item ->
        PostEditSheet(postId = item.id, longVideo = true, onDismiss = { sheets.editFor = null })
    }
    sheets.audioFor?.let { mediaId ->
        AudioTracksSheet(mediaId = mediaId, onDismiss = { sheets.audioFor = null })
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
    series: SeriesInfo?,
    countdown: Countdown?,
    ended: Boolean,
    overlays: Map<String, EngagementOverlay>,
    library: VideoLibraryState,
    subscriptionEdges: Map<String, ChannelSubscription>,
    subscribeBusy: Boolean,
    offlineState: OfflineState,
    viewModel: WatchViewModel,
    actions: WatchDetailsActions,
) {
    if (fullscreen) {
        PlayerOrState(
            content = content,
            playhead = playhead,
            fullscreen = true,
            transport = transport,
            upNext = upNext,
            countdown = countdown,
            ended = ended,
            viewModel = viewModel,
            modifier = Modifier.fillMaxSize(),
        )
        return
    }
    Column(modifier = Modifier.fillMaxSize()) {
        PlayerOrState(
            content = content,
            playhead = playhead,
            fullscreen = false,
            transport = transport,
            upNext = upNext,
            countdown = countdown,
            ended = ended,
            viewModel = viewModel,
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(LANDSCAPE),
        )
        val item = (content as? WatchContent.Ready)?.item ?: return
        val channelId = subscribeRef(item)
        val edge = subscriptionEdges[channelId]
        val overlay = overlays[item.id] ?: EngagementOverlay()
        LazyColumn(
            modifier = Modifier
                .fillMaxSize()
                .testTag("watch_details"),
        ) {
            // The Action button (2026-10-23): under the player, never on a locked video.
            item(key = "watch_action") {
                ActionPill(
                    action = item.action,
                    isAuthor = (viewModel.ownUserId.isNotBlank() && item.authorId == viewModel.ownUserId) || item.action?.isAuthorCopy == true,
                    locked = (content as? WatchContent.Ready)?.locked == true,
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s),
                )
            }
            // An offline copy says so, and one being saved shows how far it is (2026-10-02).
            watchOfflineStatusItem(
                offlineCopy = (content as? WatchContent.Ready)?.offlineCopy == true,
                entry = offlineState.copies[item.id],
            )
            watchDetails(
                item = item,
                likes = overlay.likeCountOr(item.counts.likes, item.viewer.hasReacted),
                viewer = watchViewerState(item, overlay, library),
                subscription = WatchSubscription(
                    edge = edge,
                    offersSubscribe = offersSubscribe(viewModel.ownUserId, channelId, edge),
                    busy = subscribeBusy,
                ),
                upNext = upNext,
                series = series,
                thumbFor = viewModel::thumb,
                actions = actions,
            )
        }
    }
}

/**
 * The player, or what stands in for it: a loader while the post is fetched,
 * "still processing" for a video with nothing to play yet, the failure
 * with Back as the way out. Over the player, once the video has ended, the
 * countdown to the next episode or the end screen; the countdown wins when
 * both could show, because it is the one that is about to do something.
 *
 * ## THE POSTER UNDER THE END SCREEN (black after rotation, 2026-09-05)
 *
 * Between the surface and the overlay sits the video's cover while
 * [ended]. Turning the phone moves the player between the portrait column
 * and the fullscreen slot, two different places in the composition, so the
 * SurfaceView is torn down and a new one attached to the player. ExoPlayer
 * paints a freshly attached surface from the decoder's next output buffer;
 * at STATE_ENDED the decoder has drained and there is no buffer, so the
 * new surface stays black (a paused video survives the same turn because
 * its next frame is still queued). A seek would repaint it but re-enters
 * ENDED and restarts the countdown the viewer may have cancelled, so the
 * cover stands in instead: it is what the end screen dims, whatever the
 * surface has, and it is keyed on the view model's [ended] rather than
 * the polled playhead so the turn cannot blink it off.
 */
@Suppress("LongParameterList")
@Composable
private fun PlayerOrState(
    content: WatchContent,
    playhead: Playhead,
    fullscreen: Boolean,
    transport: WatchTransport,
    upNext: List<FeedItem>,
    countdown: Countdown?,
    ended: Boolean,
    viewModel: WatchViewModel,
    modifier: Modifier = Modifier,
) {
    when (content) {
        WatchContent.Loading -> UsLoadingState(label = "Loading video", modifier = modifier)
        is WatchContent.Failed -> UsErrorState(message = content.message, modifier = modifier, onRetry = null)
        // Members only (2026-10-23): the lock where the player goes; nothing plays.
        is WatchContent.Ready -> if (content.locked) {
            Box(modifier = modifier.background(UsTheme.extended.stage), contentAlignment = Alignment.Center) {
                viewModel.thumb(content.item).url?.let { url ->
                    AsyncImage(
                        model = url,
                        contentDescription = null,
                        contentScale = ContentScale.Crop,
                        modifier = Modifier.fillMaxSize().blur(LOCK_BLUR),
                    )
                }
                MembersOnlyLock(
                    creatorId = content.item.authorId,
                    creatorName = content.item.creatorName,
                    requiredLevelId = content.item.membersOnly.tierRequiredId,

                    requiredPricePaise = content.item.membersOnly.requiredPricePaise,
                    onUnlocked = viewModel::reloadAfterUnlock,
                    style = MembersOnlyLockStyle.OnMedia,
                    durationMs = content.item.membersOnly.redactedDurationMs,
                    modifier = Modifier.padding(UsTheme.spacing.l),
                )
            }
        } else if (content.playback == null) {
            UsEmptyState(
                title = "Still processing",
                detail = "This video isn't ready to play yet.",
                modifier = modifier,
            )
        } else {
            Box(modifier = modifier) {
                WatchPlayer(
                    player = viewModel.player,
                    playhead = playhead,
                    fullscreen = fullscreen,
                    transport = transport,
                    modifier = Modifier.fillMaxSize(),
                )
                // The stored caption track the viewer turned on; nothing is drawn while it is off.
                CaptionsOverlay(player = viewModel.player)
                if (ended) EndedPoster(thumb = viewModel.thumb(content.item))
                when {
                    countdown != null -> NextEpisodeCountdown(
                        countdown = countdown,
                        onCancel = viewModel::cancelCountdown,
                        onPlayNow = viewModel::playNextNow,
                    )
                    ended -> WatchEndScreen(
                        upNext = upNext,
                        thumbFor = viewModel::thumb,
                        onReplay = viewModel::togglePlay,
                        onOpen = { viewModel.open(it.id) },
                    )
                }
            }
        }
    }
}

private val LOCK_BLUR = 28.dp

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
