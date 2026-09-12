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
import com.us.android.core.datastore.SettingsDataStore
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.feed.data.FeedRepository
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.SubscriptionGraph
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.feed.data.playbackFor
import com.us.android.core.feed.data.videoThumb
import com.us.android.core.media.MediaSources
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerFactory
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.NotifyOn
import com.us.android.core.ui.UsReelQuality
import com.us.android.feature.tube.data.SeriesEpisode
import com.us.android.feature.tube.data.SeriesInfo
import com.us.android.feature.tube.data.TubeQueue
import com.us.android.feature.tube.data.VideoSeriesRepository
import com.us.android.feature.tube.data.WatchProgressRepository
import com.us.android.feature.tube.navigation.WatchRoute
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
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

/** The next episode on its way: [secondsLeft] ticks down once a second until it plays. */
data class Countdown(val next: SeriesEpisode, val secondsLeft: Int)

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
 * Not looped: when it ends, the player stops on the last frame and what
 * happens next is [endOfVideo]'s rule.
 *
 * ## THE END OF A VIDEO (founder, 2026-09-12)
 *
 * A video in a series counts down ten seconds to the next episode
 * ([countdown]), with Cancel and Play now on screen; the viewer can switch
 * the countdown off for good ([autoplayNext]). The last episode, a video in
 * no series, and a cancelled countdown all end on the end screen: Replay and
 * the "Up next" recommendations, none of which plays on its own. Nothing
 * auto-advances along the browse list.
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
 * ## WHAT IT OFFERS
 *
 * The list Tube home was showing ([TubeQueue]): "Up next" is the rows after
 * this one, offered under the player and on the end screen. A video opened
 * outside the list (a deep link, or a list refreshed underneath) is fetched
 * by id and offers the rest of the list. The video's series, when it has
 * one ([series]), is fetched beside the resume and never waited on.
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
    private val seriesRepository: VideoSeriesRepository,
    private val settings: SettingsDataStore,
    private val queue: TubeQueue,
    private val engagement: EngagementStore,
    private val shares: EngagementRepository,
    private val follows: FollowGraph,
    private val subscriptions: SubscriptionGraph,
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

    /**
     * How the NEXT view began, consumed by [startWatchAnalytics]. TAP unless
     * the countdown ran out, because the viewer opens everything else on this
     * screen by hand, and `autoplay` on the wire means the app chose.
     */
    private var pendingStartMethod: PlayStartMethod = PlayStartMethod.TAP

    private val _currentId = MutableStateFlow(savedStateHandle.toRoute<WatchRoute>().postId)

    /** The post playing now; changes in place when the viewer picks from "Up next" or the next episode plays. */
    val currentId: StateFlow<String> = _currentId.asStateFlow()

    private val _content = MutableStateFlow<WatchContent>(WatchContent.Loading)
    val content: StateFlow<WatchContent> = _content.asStateFlow()

    /** The rows after the current one in the list the viewer came from. */
    val upNext: StateFlow<List<FeedItem>> = combine(queue.items, _currentId) { items, id -> upNext(items, id) }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), emptyList())

    private val _series = MutableStateFlow<SeriesInfo?>(null)

    /** The series this video is an episode of, or null: not in one, or not known yet. */
    val series: StateFlow<SeriesInfo?> = _series.asStateFlow()

    /**
     * "Autoplay next episode". Eager, not while-subscribed: the value is read
     * the instant a video ends, and a flow nobody has collected yet would
     * answer with the default rather than the viewer's choice.
     */
    val autoplayNext: StateFlow<Boolean> = settings.autoplayNextEpisode
        .stateIn(viewModelScope, SharingStarted.Eagerly, true)

    private val _countdown = MutableStateFlow<Countdown?>(null)

    /** The countdown to the next episode while one is running; null otherwise. */
    val countdown: StateFlow<Countdown?> = _countdown.asStateFlow()
    private var countdownJob: Job? = null

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

    /**
     * Channel id → the viewer's subscription toward it. The author row's
     * control is Subscribe, not Follow (founder, 2026-09-12): the server
     * makes the follow edge alongside, and the follow graph learns it on
     * its next read; the "more" sheet still reads [followEdges].
     */
    val subscriptionEdges: StateFlow<Map<String, ChannelSubscription>> = subscriptions.edges

    private val _subscribeBusy = MutableStateFlow(false)

    /** A subscribe, unsubscribe or bell change in flight, so the control does not take a second tap. */
    val subscribeBusy: StateFlow<Boolean> = _subscribeBusy.asStateFlow()

    private val _ended = MutableStateFlow(false)

    /**
     * The player is on its last frame. Held HERE rather than read off the
     * player by the screen, because the end screen and the poster under it
     * have to survive a turn of the phone: the surface is re-created on the
     * way and the composable that polled the player goes with it, while
     * this value does not.
     */
    val ended: StateFlow<Boolean> = _ended.asStateFlow()

    private val listener = object : Player.Listener {
        // Only the END is business here. STATE_BUFFERING is deliberately not
        // handled in this class: buffering is something the SURFACE draws,
        // and it is drawn by the one shared observer every video surface in
        // the app uses (`core.media.ui.rememberVideoLoadState`, consumed by
        // WatchPlayer). A second reading of the same player here would be a
        // copy that can disagree with the first.
        override fun onPlaybackStateChanged(playbackState: Int) {
            // Every transition, not only the one INTO ended: Replay seeks to
            // the top and the player goes through BUFFERING on its way back,
            // which is when the poster has to come off the surface.
            _ended.value = playbackState == Player.STATE_ENDED
            if (playbackState == Player.STATE_ENDED) {
                // Closed as `ended` BEFORE the countdown starts, so the
                // completed view is attributed to this video rather than being
                // swept up by the next episode's open.
                endWatchAnalytics(PlayEndReason.ENDED)
                startCountdown()
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

    /**
     * Swaps the video in place: "Up next" or an episode row was tapped, or
     * the countdown ran out. The screen stays; the player re-prepares.
     */
    fun open(postId: String) {
        if (postId == _currentId.value) return
        cancelCountdown()
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
        launchLoadSeries(postId)
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
        // Consumed once: the method describes how THIS playback began, and
        // the next one is a tap again unless another countdown says otherwise.
        val startMethod = pendingStartMethod
        pendingStartMethod = PlayStartMethod.TAP
        watchSession = watchTracker.startView(
            contentId = item.id,
            creatorId = item.author.id,
            surface = AnalyticsSurface.POSTTUBE,
            contentDurationMs = item.durationMs(),
            // Tube plays because the viewer opened this video, not because it
            // scrolled past, except when the countdown to the next episode
            // ran out. `resume` seeks afterwards; the method describes how
            // playback BEGAN.
            startMethod = startMethod,
            isMuted = player.volume == 0f,
            isAutoplay = startMethod == PlayStartMethod.AUTOPLAY,
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

    /**
     * Both graphs: the subscription for the author row's Subscribe, the
     * follow for the "more" sheet. Two reads rather than one because the
     * edges are not the same edge (a viewer can follow without subscribing).
     */
    private fun launchKnowAuthor(item: FeedItem) {
        viewModelScope.launch { follows.ensureKnown(listOf(item.author.id)) }
        viewModelScope.launch { subscriptions.ensureKnown(listOf(subscribeRef(item))) }
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

    /**
     * The series, fetched beside the prepare and never awaited: the first
     * frame must not wait on a round trip for a list that only matters at
     * the end. Launched on the ViewModel's scope rather than inside the
     * collectLatest that called [load], so a pick from "Up next" mid-fetch
     * does not silently abandon it; the id check keeps a late answer for
     * the last video from landing on this one. The list is kept while the
     * new video is in it (the next episode, most often) so "In this series"
     * does not blink out and back between episodes.
     */
    private fun launchLoadSeries(postId: String) {
        if (_series.value?.episodes?.none { it.postId == postId } != false) _series.value = null
        viewModelScope.launch {
            val fetched = seriesRepository.forPost(postId)
            if (_currentId.value == postId) _series.value = fetched
        }
    }

    // ── The end of a video ───────────────────────────────────────────────

    /**
     * Ten seconds to the next episode, one tick a second so the number on
     * screen moves; then it plays. Nothing starts when [endOfVideo] says the
     * end screen: the player stays on its last frame and the screen draws
     * Replay and the recommendations.
     */
    private fun startCountdown() {
        val end = endOfVideo(_series.value, _currentId.value, autoplayNext.value)
        val next = (end as? EndOfVideo.Countdown)?.next ?: return
        countdownJob?.cancel()
        countdownJob = viewModelScope.launch {
            var left = end.seconds
            _countdown.value = Countdown(next, left)
            while (left > 0) {
                delay(COUNTDOWN_TICK_MILLIS)
                left--
                _countdown.value = Countdown(next, left)
            }
            playNext(next, PlayStartMethod.AUTOPLAY)
        }
    }

    /** The viewer said no, or did something else with the player: the end screen instead. */
    fun cancelCountdown() {
        countdownJob?.cancel()
        countdownJob = null
        _countdown.value = null
    }

    /** "Play now": the next episode without the wait. A tap, so the view is a tap's. */
    fun playNextNow() {
        val next = _countdown.value?.next ?: return
        playNext(next, PlayStartMethod.TAP)
    }

    private fun playNext(next: SeriesEpisode, startMethod: PlayStartMethod) {
        cancelCountdown()
        pendingStartMethod = startMethod
        open(next.postId)
    }

    fun setAutoplayNext(enabled: Boolean) {
        viewModelScope.launch { settings.setAutoplayNextEpisode(enabled) }
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

    /**
     * Play or pause; from the end, Replay. Replay sets `playWhenReady` rather
     * than toggling it: at STATE_ENDED the player still holds the `true` it
     * finished with, and a toggle would seek to the top and sit there paused.
     */
    fun togglePlay() {
        cancelCountdown()
        if (player.playbackState == Player.STATE_ENDED) {
            player.seekTo(0L)
            player.playWhenReady = true
            return
        }
        player.playWhenReady = !player.playWhenReady
    }

    /** ±10 s from a double-tap; clamped so a skip past the end lands on the end. */
    fun seekBy(deltaMillis: Long) {
        cancelCountdown()
        val duration = player.duration.takeIf { it > 0L } ?: Long.MAX_VALUE
        noteSeek()
        player.seekTo((player.currentPosition + deltaMillis).coerceIn(0L, duration))
    }

    fun seekTo(positionMillis: Long) {
        cancelCountdown()
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

    /**
     * The app went behind something: hold the frame, and say where it was.
     * A running countdown is dropped rather than paused: the next episode
     * must not start playing to a screen nobody is looking at.
     */
    fun onBackground() {
        cancelCountdown()
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

    /**
     * Subscribe to the video's channel (follow plus notify, made by the
     * server in one call). Still `follow_from_content`, not a bare follow:
     * the button sits on the video being watched, so the content that
     * earned the edge is known, and a subscribe IS a follow to the ranking
     * model. The channel page deliberately does NOT emit this; see
     * ChannelViewModel.
     */
    fun onSubscribe(channelId: String) = relationshipChange {
        recordEngagement(AnalyticsEventType.FOLLOW_FROM_CONTENT)
        subscriptions.subscribe(channelId)
    }

    fun onUnsubscribe(channelId: String) = relationshipChange { subscriptions.unsubscribe(channelId) }

    /** The bell: all to none, none to all. Only meaningful while subscribed. */
    fun onToggleNotify(channelId: String) {
        val current = subscriptions.edges.value[channelId] ?: return
        if (!current.subscribed) return
        val next = if (current.notifyOn == NotifyOn.ALL) NotifyOn.NONE else NotifyOn.ALL
        relationshipChange { subscriptions.setNotifyOn(channelId, next) }
    }

    /** One change at a time: the graph flips optimistically, and a second tap mid-flight would race the first. */
    private fun relationshipChange(change: suspend () -> AppResult<Unit>) {
        if (_subscribeBusy.value) return
        viewModelScope.launch {
            _subscribeBusy.value = true
            change()
            _subscribeBusy.value = false
        }
    }

    private fun recordEngagement(type: String) {
        watchSession?.let { analytics.recordEngagement(type, it) }
    }

    override fun onCleared() {
        cancelCountdown()
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

        /** The countdown moves once a second, as the number on screen does. */
        const val COUNTDOWN_TICK_MILLIS = 1_000L
    }
}
