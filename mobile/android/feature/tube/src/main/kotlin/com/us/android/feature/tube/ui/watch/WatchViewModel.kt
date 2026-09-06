package com.us.android.feature.tube.ui.watch

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import androidx.navigation.toRoute
import com.us.android.core.analytics.AnalyticsEventType
import com.us.android.core.analytics.AnalyticsRecorder
import com.us.android.core.analytics.AnalyticsSurface
import com.us.android.core.analytics.PlayEndReason
import com.us.android.core.analytics.PlayStartMethod
import com.us.android.core.analytics.VideoWatchTracker
import com.us.android.core.analytics.WatchProbe
import com.us.android.core.analytics.WatchSession
import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.feed.data.FeedRepository
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.playbackFor
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaSources
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerFactory
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.UsReelQuality
import com.us.android.feature.tube.data.TubeQueue
import com.us.android.feature.tube.data.WatchProgressRepository
import com.us.android.feature.tube.navigation.WatchRoute
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject

/** What the watch screen has to show: the video being fetched, playing, or gone. */
sealed interface WatchContent {
    data object Loading : WatchContent

    /** [playback] is null for a video still transcoding with nothing to play yet. */
    data class Ready(val item: FeedItem, val playback: Playback?) : WatchContent
    data class Failed(val message: String) : WatchContent
}

/**
 * One video, playing (Tube, 2026-09-05).
 *
 * ## THE PLAYER LIVES HERE
 *
 * A dedicated ExoPlayer from the same factory the pool uses — the same
 * buffering, the same authenticated, segment-caching data source — held by
 * the ViewModel rather than the composable, because fullscreen is an
 * orientation change and the screen must not lose its playhead (or its
 * decoder) to one. Sound is ON: a long video is watched, not previewed.
 * Not looped: when it ends, the next in the list plays ([advance]).
 *
 * ## RESUME AND PROGRESS
 *
 * On open the saved playhead is fetched beside the prepare and applied if
 * the viewer had not all but finished ([resumePositionMs]); progress is
 * reported every ten seconds while playing, whenever playback pauses (a
 * tap, the end, the app going to the background) and when the screen is
 * left, `completed` at 95% ([isCompleted]). A report needs a duration —
 * the server binds it required — so a video whose length is not yet known
 * is not reported until it is.
 *
 * ## WHAT IT PLAYS THROUGH
 *
 * The list Tube home was showing ([TubeQueue]): "Up next" is the rows after
 * this one and the end of the video advances to the first of them. A video
 * opened outside the list — a deep link, or a list refreshed underneath —
 * is fetched by id and plays with the rest of the list after it.
 */
@HiltViewModel
// Constructor injection of the surface's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class WatchViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: FeedRepository,
    private val urlResolver: MediaUrlResolver,
    playerFactory: PlayerFactory,
    private val sources: MediaSources,
    private val progress: WatchProgressRepository,
    private val queue: TubeQueue,
    private val engagement: EngagementStore,
    private val shares: EngagementRepository,
    private val follows: FollowGraph,
    private val watchTracker: VideoWatchTracker,
    private val analytics: AnalyticsRecorder,
    /** Progress reports outlive the screen: the last one is sent as the ViewModel clears. */
    @ApplicationScope private val appScope: CoroutineScope,
) : ViewModel() {

    /**
     * The analytics view currently open, if any.
     *
     * Held so engagement — a like, a save, a follow — can be attributed to the
     * playback session that produced it. `follow_from_content` in particular
     * only means anything with the content in scope, and on this screen it
     * always is.
     */
    private var watchSession: WatchSession? = null

    private val _currentId = MutableStateFlow(savedStateHandle.toRoute<WatchRoute>().postId)

    /** The post playing now; changes in place when the viewer picks from "Up next" or the video ends. */
    val currentId: StateFlow<String> = _currentId.asStateFlow()

    private val _content = MutableStateFlow<WatchContent>(WatchContent.Loading)
    val content: StateFlow<WatchContent> = _content.asStateFlow()

    /** The rows after the current one in the list the viewer came from. */
    val upNext: StateFlow<List<FeedItem>> = combine(queue.items, _currentId) { items, id -> upNext(items, id) }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), emptyList())

    /** The one player. Released with the ViewModel — see the class note. */
    val player: ExoPlayer = playerFactory.create().apply {
        repeatMode = Player.REPEAT_MODE_OFF
        volume = 1f
    }

    private val _quality = MutableStateFlow<UsReelQuality>(UsReelQuality.Auto)

    /** The rendition the viewer asked for from the gear; Auto until they do. */
    val quality: StateFlow<UsReelQuality> = _quality.asStateFlow()

    private val _speed = MutableStateFlow(1f)
    val speed: StateFlow<Float> = _speed.asStateFlow()

    // ── Engagement, the shared lanes ─────────────────────────────────────

    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges
    val ownUserId: String get() = follows.ownId

    private val listener = object : Player.Listener {
        // Only the END is business here. STATE_BUFFERING is deliberately not
        // handled in this class: buffering is something the SURFACE draws,
        // and it is drawn by the one shared observer every video surface in
        // the app uses (`core.media.ui.rememberVideoLoadState`, consumed by
        // WatchPlayer). A second reading of the same player here would be a
        // copy that can disagree with the first.
        override fun onPlaybackStateChanged(playbackState: Int) {
            if (playbackState == Player.STATE_ENDED) {
                // Closed as `ended` BEFORE advancing, so the completed view is
                // attributed to this video rather than being swept up by the
                // swipe_next that moves to the following one.
                endWatchAnalytics(PlayEndReason.ENDED)
                advance()
            }
        }

        override fun onIsPlayingChanged(isPlaying: Boolean) {
            // A real stop — the viewer paused, or the video ended — not a
            // rebuffer: `isPlaying` also drops on every stall, and a stuttering
            // connection would report every few seconds.
            if (!isPlaying && (!player.playWhenReady || player.playbackState == Player.STATE_ENDED)) report()
        }
    }

    init {
        player.addListener(listener)
        // collectLatest: a pick from "Up next" while the last video's progress
        // read is still in flight abandons that read rather than queueing.
        viewModelScope.launch { _currentId.collectLatest { load(it) } }
        viewModelScope.launch {
            while (isActive) {
                delay(REPORT_INTERVAL_MILLIS)
                if (player.isPlaying) report()
            }
        }
    }

    /** Swaps the video in place — "Up next" was tapped. The screen stays; the player re-prepares. */
    fun open(postId: String) {
        if (postId == _currentId.value) return
        report()
        endWatchAnalytics(PlayEndReason.SWIPE_NEXT)
        _currentId.value = postId
    }

    private suspend fun load(postId: String) {
        _content.value = WatchContent.Loading
        val item = queue.items.value.firstOrNull { it.id == postId } ?: fetch(postId)
        if (item == null) {
            player.stop()
            _content.value = WatchContent.Failed("We couldn't load this video.")
            return
        }
        val playback = urlResolver.playbackFor(item)
        _content.value = WatchContent.Ready(item, playback)
        launchKnowAuthor(item)
        if (playback == null) {
            player.stop()
            player.clearMediaItems()
            return
        }
        player.setMediaSource(sources.create(playback))
        player.prepare()
        player.playWhenReady = true
        startWatchAnalytics(item)
        resume(postId, item)
    }

    /**
     * Opens the analytics view for this video.
     *
     * Started beside `prepare()` rather than on the first frame, because
     * `time_to_first_frame_ms` is measured from the moment playback was ASKED
     * for — starting the clock when the frame arrives would report zero for
     * every video.
     *
     * The duration comes from the row, not the player: at this point the player
     * has none, and analytics-service requires `content_duration_ms > 0` and
     * divides by it for `percent_viewed`. A row with no duration is not
     * tracked rather than reported as zero-length.
     */
    private fun startWatchAnalytics(item: FeedItem) {
        endWatchAnalytics(PlayEndReason.SWIPE_NEXT)
        watchSession = watchTracker.startView(
            contentId = item.id,
            creatorId = item.author.id,
            surface = AnalyticsSurface.POSTTUBE,
            contentDurationMs = item.durationMs(),
            // Tube plays because the viewer opened this video, not because it
            // scrolled past. `resume` seeks afterwards; the method describes
            // how playback BEGAN.
            startMethod = PlayStartMethod.TAP,
            isMuted = player.volume == 0f,
            isAutoplay = false,
        ) {
            WatchProbe(
                playheadMs = player.currentPosition.coerceAtLeast(0L),
                isPlaying = player.isPlaying,
                isBuffering = player.playbackState == Player.STATE_BUFFERING,
                // The first frame has been drawn once the player is out of
                // BUFFERING and into READY with content behind it.
                renderedFirstFrame = player.playbackState == Player.STATE_READY,
                speed = player.playbackParameters.speed,
                durationMs = player.duration.takeIf { it > 0L } ?: 0L,
            )
        }
    }

    private fun endWatchAnalytics(reason: PlayEndReason) {
        watchSession?.let { watchTracker.endView(it.contentId, reason) }
        watchSession = null
    }

    private suspend fun fetch(postId: String): FeedItem? =
        (repository.post(postId) as? AppResult.Success)?.data

    private fun launchKnowAuthor(item: FeedItem) {
        viewModelScope.launch { follows.ensureKnown(listOf(item.author.id)) }
    }

    /**
     * The saved playhead, applied once known — beside the prepare rather
     * than before it, so the first frame never waits on a round trip. If the
     * answer lands after the viewer has already watched a stretch, it is
     * left alone: a jump mid-watch is worse than a missed resume.
     */
    private suspend fun resume(postId: String, item: FeedItem) {
        val saved = progress.progress(postId) ?: return
        if (_currentId.value != postId) return
        val duration = player.duration.takeIf { it > 0L } ?: saved.durationMs.takeIf { it > 0L } ?: item.durationMs()
        val at = resumePositionMs(saved.positionMs, duration)
        if (at > 0L && player.currentPosition < RESUME_GRACE_MILLIS) player.seekTo(at)
    }

    /** The end of the list is the end: the player simply stops on the last frame. */
    private fun advance() {
        val next = nextAfter(queue.items.value, _currentId.value) ?: return
        open(next.id)
    }

    /**
     * Where the playhead is, to the server. Needs a duration — the player's
     * once it has one, the row's otherwise — because the server binds
     * `duration_ms` required; a video whose length nobody knows yet is not
     * reported until they do.
     */
    private fun report() {
        val item = (_content.value as? WatchContent.Ready)?.item ?: return
        val duration = player.duration.takeIf { it > 0L } ?: item.durationMs()
        if (duration <= 0L) return
        val position = player.currentPosition.coerceIn(0L, duration)
        // The app scope, not the ViewModel's: the last report is made as the
        // ViewModel clears, when its own scope is already cancelled.
        appScope.launch {
            progress.save(item.id, position, duration, isCompleted(position, duration))
        }
    }

    // ── Transport ────────────────────────────────────────────────────────

    fun togglePlay() {
        if (player.playbackState == Player.STATE_ENDED) player.seekTo(0L)
        player.playWhenReady = !player.playWhenReady
    }

    /** ±10 s from a double-tap; clamped so a skip past the end lands on the end. */
    fun seekBy(deltaMillis: Long) {
        val duration = player.duration.takeIf { it > 0L } ?: Long.MAX_VALUE
        noteSeek()
        player.seekTo((player.currentPosition + deltaMillis).coerceIn(0L, duration))
    }

    fun seekTo(positionMillis: Long) {
        noteSeek()
        player.seekTo(positionMillis.coerceAtLeast(0L))
    }

    /**
     * Tells the tracker a jump was deliberate.
     *
     * Without it the next playhead sample looks like a huge forward or backward
     * step, and the tracker would either credit the skipped stretch as watched
     * or mistake a scrub back for a loop.
     */
    private fun noteSeek() {
        watchSession?.let { watchTracker.recordSeek(it.contentId) }
    }

    fun selectQuality(quality: UsReelQuality) {
        _quality.value = quality
        player.applyQuality(quality)
    }

    fun selectSpeed(speed: Float) {
        _speed.value = speed
        player.setPlaybackSpeed(speed)
    }

    /** The app went behind something: hold the frame, and say where it was. */
    fun onBackground() {
        player.playWhenReady = false
        report()
        // The view is closed rather than paused: the process may not survive
        // the background, and an unclosed view is a view the creator is never
        // paid for. Coming back to the foreground opens a fresh one.
        endWatchAnalytics(PlayEndReason.BACKGROUNDED)
    }

    /** What the card draws for an "Up next" row. */
    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    // ── Engagement ───────────────────────────────────────────────────────

    fun onReact(postId: String, serverReacted: Boolean) = viewModelScope.launch {
        // Only the POSITIVE direction is an analytics signal. `serverReacted`
        // is the state before the tap, so an un-like is `true` here — and
        // there is no "unlike" event in the model, because the engagement rate
        // that feeds the content quality score counts likes given, not the net.
        if (!serverReacted) recordEngagement(AnalyticsEventType.LIKE)
        engagement.toggleReaction(postId, serverReacted)
    }

    fun onBookmark(postId: String, serverBookmarked: Boolean) = viewModelScope.launch {
        if (!serverBookmarked) recordEngagement(AnalyticsEventType.SAVE)
        engagement.toggleBookmark(postId, serverBookmarked)
    }

    /** Recorded AFTER the chooser was launched; a failed count is not the viewer's problem. */
    fun onExternalShared(postId: String) = viewModelScope.launch {
        recordEngagement(AnalyticsEventType.SHARE)
        shares.recordExternalShare(postId)
    }

    fun onFollow(authorId: String) = viewModelScope.launch {
        // follow_from_content, not a bare follow: this button sits on the video
        // being watched, so the content that earned the follow is known. The
        // channel page's follow button deliberately does NOT emit this — see
        // ChannelViewModel.
        recordEngagement(AnalyticsEventType.FOLLOW_FROM_CONTENT)
        follows.follow(authorId)
    }

    private fun recordEngagement(type: String) {
        watchSession?.let { analytics.recordEngagement(type, it) }
    }

    override fun onCleared() {
        report()
        // Leaving the screen. Not `ended` — the video did not finish — and not
        // `backgrounded`, which is a different signal about the app.
        endWatchAnalytics(PlayEndReason.PAUSED)
        player.removeListener(listener)
        player.release()
    }

    private fun FeedItem.durationMs(): Long = media.firstOrNull { it.kind == VIDEO_KIND }?.durationMs ?: 0L

    private companion object {
        const val VIDEO_KIND = "video"
        const val STOP_TIMEOUT_MILLIS = 5_000L

        /** Every ten seconds while playing — the contract's cadence. */
        const val REPORT_INTERVAL_MILLIS = 10_000L

        /** A resume that arrives after this much has played is dropped. */
        const val RESUME_GRACE_MILLIS = 5_000L
    }
}
