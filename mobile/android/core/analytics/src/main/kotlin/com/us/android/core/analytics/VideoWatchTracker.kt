package com.us.android.core.analytics

import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.joinAll
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.util.concurrent.ConcurrentHashMap
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.math.max
import kotlin.math.min

/**
 * One reading of a player's state.
 *
 * The tracker asks for this rather than holding an `ExoPlayer`, which is what
 * keeps `:core:analytics` free of a media3 dependency and, more usefully, makes
 * every rule below testable without a player at all.
 */
data class WatchProbe(
    val playheadMs: Long,
    val isPlaying: Boolean,
    val isBuffering: Boolean,
    val renderedFirstFrame: Boolean,
    val speed: Float = 1f,
    /** The player's own duration once known; 0 while it is not. */
    val durationMs: Long = 0L,
)

/**
 * Turns playback into the five video events.
 *
 * ## HEARTBEAT CADENCE: 5 SECONDS, ONE CONSTANT
 *
 * The service's own model comments suggest 2s for reels and 5s for long video.
 * This client sends one cadence, [HEARTBEAT_INTERVAL_MS] = 5s, for both, and
 * the reasoning is worth stating because heartbeats are by far the highest
 * volume event on the platform:
 *
 *  - Heartbeats exist to carry **totals** — watched ms, buffering ms, seeks.
 *    Those are additive, so a coarser cadence loses no information at all; it
 *    only delays it. Nothing about a 5s beat under-reports a watch.
 *  - The thing a 2s beat would genuinely buy — knowing *early* that a short
 *    watch happened — is already delivered by milestones, which fire at
 *    VIEW_1S and VIEW_3S. A short watch is therefore visible at one second
 *    regardless of the heartbeat rate.
 *  - At 2s a 90-second reel produces 45 heartbeats instead of 18. Across a
 *    scrolling session that is the difference between a few hundred rows and
 *    over a thousand, all of them radio wake-ups and rows in the ingest table,
 *    for resolution nothing consumes.
 *
 * Accuracy does not depend on it: totals are accumulated from real playhead
 * deltas on a separate [SAMPLE_INTERVAL_MS] = 1s tick, and a final sample is
 * taken when the view ends. The heartbeat only decides how often the running
 * total is *reported*.
 *
 * ## WHY THE TRACKER OWNS THE TICK
 *
 * Every surface already polls the player for its progress bar, at three
 * different rates. Reusing those would have put the cadence in three places and
 * left the feed — which has no progress bar and therefore no loop — with no
 * watch time at all. One ticker per active view, owned here, is one cadence,
 * and the feed gets counted like everything else.
 */
@Singleton
class VideoWatchTracker @Inject constructor(
    private val analytics: AnalyticsRecorder,
    @ApplicationScope private val scope: CoroutineScope,
    @Dispatcher(UsDispatcher.Main) private val main: CoroutineDispatcher,
) {

    private val active = ConcurrentHashMap<String, TrackedView>()

    private class TrackedView(
        val session: WatchSession,
        val probe: suspend () -> WatchProbe,
        val startedAtMillis: Long,
        val isAutoplay: Boolean,
        val isMuted: Boolean,
        val startMethod: PlayStartMethod,
    ) {
        var job: Job? = null

        /**
         * Seeded at zero rather than "unknown".
         *
         * With no baseline the FIRST sample of every view can only establish
         * one, which silently discards the first second of every watch — a
         * third of a three-second reel, and the three-second bar is exactly
         * where `IsDisplayView` decides whether the creator is paid. Playback
         * begins at zero on every surface here, so zero is the honest baseline.
         *
         * A resumed video is the exception: its first playhead is far from
         * zero, and the seek ceiling in [accumulateWatched] catches that and
         * credits nothing, which is the safe direction.
         */
        var lastPlayhead = 0L
        var watchedMs = 0L
        var bufferingMs = 0L
        var bufferingBeforeFirstFrameMs = 0L
        var continuousMs = 0L
        var maxContinuousMs = 0L
        var loopCount = 0
        var seekCount = 0
        var startEmitted = false
        var heartbeatSequence = 0
        var watchedAtLastHeartbeat = 0L
        var bufferingAtLastHeartbeat = 0L
        var seeksAtLastHeartbeat = 0
        var msSinceHeartbeat = 0L
        var ended = false
        val milestonesSent = mutableSetOf<String>()
    }

    /**
     * Begins tracking one view and emits its `play_start` once frames arrive.
     *
     * Returns the session so the surface can attribute engagement events to the
     * view that produced them, or null when the content cannot be reported —
     * an id that is not a uuid, or an unknown duration. A null is not an error;
     * it means this content will not be counted, and the caller carries on.
     */
    @Suppress("LongParameterList") // Each argument is a distinct wire field on play_start.
    fun startView(
        contentId: String,
        creatorId: String,
        surface: AnalyticsSurface,
        contentDurationMs: Long,
        startMethod: PlayStartMethod,
        isMuted: Boolean,
        isAutoplay: Boolean,
        position: Int? = null,
        probe: suspend () -> WatchProbe,
    ): WatchSession? {
        // Duration is 0 for images and for rows from a server that predates the
        // field. Reporting it as zero-length would divide by zero in the
        // percent-viewed calculation server-side, so the view is skipped.
        if (contentDurationMs <= 0L) return null

        val session = WatchSession.start(
            contentId = contentId,
            creatorId = creatorId,
            surface = surface,
            contentDurationMs = contentDurationMs,
            position = position,
        )
        // A content id that is not a uuid would fail the whole batch it
        // travelled in. Catch it here, once, rather than in the uploader.
        if (AnalyticsEvents.playEnd(session, PlayEndReason.ENDED, 0, 0, 0, System.currentTimeMillis()) == null) {
            return null
        }

        // The same content re-entering view without an explicit end (a fast
        // flick back) closes the previous view rather than leaking its ticker.
        endView(contentId, PlayEndReason.SWIPE_NEXT)

        val tracked = TrackedView(
            session = session,
            probe = probe,
            startedAtMillis = System.currentTimeMillis(),
            isAutoplay = isAutoplay,
            isMuted = isMuted,
            startMethod = startMethod,
        )
        active[contentId] = tracked
        tracked.job = scope.launch {
            while (isActive && !tracked.ended) {
                delay(SAMPLE_INTERVAL_MS)
                // ExoPlayer state is only safe to read on the application
                // thread. One read per second is a fraction of what the reels
                // progress bar already does at 250ms.
                val probed = runCatching { withContext(main) { tracked.probe() } }.getOrNull() ?: continue
                onSample(tracked, probed)
            }
        }
        return session
    }

    /**
     * Content became visible in the viewport without necessarily playing.
     *
     * Separate from [startView] because an impression is a viewport
     * measurement: the feed can show a card for a second without it ever
     * autoplaying, and that is still a delivery the ranking model wants.
     */
    fun recordImpression(session: WatchSession, visibleMs: Long, isAutoplay: Boolean) {
        analytics.record(
            AnalyticsEvents.impression(session, visibleMs, isAutoplay, System.currentTimeMillis()),
        )
    }

    /** A user-driven seek. Counted for the heartbeat, and it breaks the continuous run. */
    fun recordSeek(contentId: String) {
        active[contentId]?.let {
            it.seekCount++
            it.continuousMs = 0
            // Marks the baseline unknown so the jump itself is never credited;
            // the next sample re-establishes it.
            it.lastPlayhead = UNKNOWN_PLAYHEAD
        }
    }

    /**
     * Ends a view and emits its `play_end`.
     *
     * This is the event the whole pipeline is built on — analytics-service
     * counts views from `play_end` and `IsDisplayView` decides which of them a
     * creator is paid for — and it fires exactly when the app is most likely to
     * be killed. It is written to disk synchronously-enough (queued on the
     * application scope, not a screen's) that process death after this call
     * still delivers the view.
     *
     * Idempotent: the second call for a content id does nothing, so the
     * `ON_STOP` pause and the composable's `onDispose` racing each other cannot
     * produce two views.
     */
    fun endView(contentId: String, reason: PlayEndReason): Job? {
        val tracked = active.remove(contentId) ?: return null
        if (tracked.ended) return null
        tracked.ended = true
        tracked.job?.cancel()
        return scope.launch {
            // A last sample so the seconds between the final tick and the swipe
            // are not lost — at a 1s cadence that is up to a second per view,
            // which across a scrolling session is a real amount of watch time.
            runCatching { withContext(main) { tracked.probe() } }.getOrNull()
                ?.let { onSample(tracked, it, emitHeartbeat = false) }
            emitPlayEnd(tracked, reason)
        }
    }

    /**
     * Ends every open view — app going to background, or signing out.
     *
     * Suspends until each `play_end` is on disk. Sign-out drains immediately
     * afterwards and then wipes the queue, so a merely-scheduled write would be
     * a view deleted before it was ever sent.
     */
    suspend fun endAll(reason: PlayEndReason) {
        active.keys.toList().mapNotNull { endView(it, reason) }.joinAll()
    }

    private suspend fun emitPlayEnd(tracked: TrackedView, reason: PlayEndReason) {
        val watched = tracked.watchedMs
        analytics.recordNow(
            AnalyticsEvents.playEnd(
                session = tracked.session,
                endReason = reason,
                watchedMsTotal = watched,
                // The server rejects a continuous stretch longer than the
                // total; clamping here rather than trusting the arithmetic
                // means a rounding edge cannot fail the batch it rides in.
                maxContinuousWatchMs = min(max(tracked.maxContinuousMs, tracked.continuousMs), watched),
                loopCount = min(tracked.loopCount, MAX_LOOP_COUNT),
                timestampMillis = System.currentTimeMillis(),
            ),
        )
    }

    @Suppress("CyclomaticComplexMethod") // One pass over one tick; splitting it would hide the ordering.
    private fun onSample(tracked: TrackedView, probe: WatchProbe, emitHeartbeat: Boolean = true) {
        val now = System.currentTimeMillis()

        if (probe.isBuffering) {
            tracked.bufferingMs += SAMPLE_INTERVAL_MS
            if (!tracked.startEmitted) tracked.bufferingBeforeFirstFrameMs += SAMPLE_INTERVAL_MS
            tracked.continuousMs = 0
        }

        if (!tracked.startEmitted && probe.renderedFirstFrame) {
            tracked.startEmitted = true
            analytics.record(
                AnalyticsEvents.playStart(
                    session = tracked.session,
                    startMethod = tracked.startMethod,
                    isMuted = tracked.isMuted,
                    isAutoplay = tracked.isAutoplay,
                    // Measured from the surface asking for playback to the
                    // first frame actually drawn. `VideoLoading.kt` tracks the
                    // same transition for the spinner but keeps no timings, so
                    // there was nothing there to reuse.
                    timeToFirstFrameMs = (now - tracked.startedAtMillis).coerceIn(0, MAX_INCREMENT_MS),
                    initialBufferMs = tracked.bufferingBeforeFirstFrameMs.coerceIn(0, MAX_INCREMENT_MS),
                    timestampMillis = now,
                ),
            )
        }

        accumulateWatched(tracked, probe)

        if (tracked.startEmitted) emitMilestones(tracked, now)

        tracked.msSinceHeartbeat += SAMPLE_INTERVAL_MS
        if (emitHeartbeat && tracked.msSinceHeartbeat >= HEARTBEAT_INTERVAL_MS && tracked.watchedMs > 0) {
            emitHeartbeat(tracked, probe, now)
        }
    }

    /**
     * Adds the watch time between two samples.
     *
     * Uses the PLAYHEAD delta rather than wall-clock, so a stall, a pause or a
     * background does not silently accrue watch time the person never saw.
     */
    private fun accumulateWatched(tracked: TrackedView, probe: WatchProbe) {
        val previous = tracked.lastPlayhead
        tracked.lastPlayhead = probe.playheadMs

        if (!probe.isPlaying || probe.isBuffering) {
            tracked.continuousMs = 0
            return
        }
        if (previous == UNKNOWN_PLAYHEAD) return

        val duration = tracked.session.contentDurationMs
        var delta = probe.playheadMs - previous
        if (delta < 0) {
            // Either the reel looped or the viewer scrubbed backwards. A loop
            // restarts near zero from near the end; anything else is a seek and
            // contributes no watch time.
            val looped = previous >= duration - LOOP_TOLERANCE_MS && probe.playheadMs <= LOOP_TOLERANCE_MS
            if (looped) {
                tracked.loopCount++
                delta = (duration - previous) + probe.playheadMs
            } else {
                tracked.seekCount++
                tracked.continuousMs = 0
                return
            }
        }

        // A forward jump larger than the tick could allow is a seek, not watch
        // time. Bound by the sample interval scaled by playback speed, with one
        // extra interval of slack for a late tick.
        val ceiling = ((SAMPLE_INTERVAL_MS * 2) * max(probe.speed, 1f)).toLong()
        if (delta > ceiling) {
            tracked.seekCount++
            tracked.continuousMs = 0
            return
        }

        tracked.watchedMs += delta
        tracked.continuousMs += delta
        tracked.maxContinuousMs = max(tracked.maxContinuousMs, tracked.continuousMs)
    }

    /**
     * Fires the time and percent thresholds this view has newly crossed.
     *
     * The time ladder depends on content type — a reel never reaches VIEW_30S,
     * so sending it that ladder would be noise — and each threshold fires once
     * per session, matching the server's own per-milestone dedupe key.
     */
    private fun emitMilestones(tracked: TrackedView, now: Long) {
        val session = tracked.session
        val watched = tracked.watchedMs
        val ladder = when (session.contentType) {
            AnalyticsContentType.REEL -> WatchMilestone.REEL_LADDER
            AnalyticsContentType.LONG_VIDEO -> WatchMilestone.LONG_VIDEO_LADDER
        }
        ladder.filter { watched >= it.thresholdMs }.forEach { send(tracked, it.wire, watched, now) }

        val percent = (watched * PERCENT).toDouble() / session.contentDurationMs
        PercentMilestone.entries
            .filter { percent >= it.percent }
            .forEach { send(tracked, it.wire, watched, now) }
    }

    private fun send(tracked: TrackedView, milestone: String, watchedMs: Long, now: Long) {
        if (!tracked.milestonesSent.add(milestone)) return
        analytics.record(AnalyticsEvents.milestone(tracked.session, milestone, watchedMs, now))
    }

    private fun emitHeartbeat(tracked: TrackedView, probe: WatchProbe, now: Long) {
        val watchedIncrement = tracked.watchedMs - tracked.watchedAtLastHeartbeat
        val bufferingIncrement = tracked.bufferingMs - tracked.bufferingAtLastHeartbeat
        val seekIncrement = tracked.seekCount - tracked.seeksAtLastHeartbeat
        tracked.watchedAtLastHeartbeat = tracked.watchedMs
        tracked.bufferingAtLastHeartbeat = tracked.bufferingMs
        tracked.seeksAtLastHeartbeat = tracked.seekCount
        tracked.msSinceHeartbeat = 0
        tracked.heartbeatSequence++

        analytics.record(
            AnalyticsEvents.heartbeat(
                session = tracked.session,
                sequence = tracked.heartbeatSequence,
                watchedMsIncrement = watchedIncrement.coerceIn(0, tracked.watchedMs),
                watchedMsTotal = tracked.watchedMs,
                playheadPositionMs = probe.playheadMs.coerceAtLeast(0),
                bufferingMsIncrement = bufferingIncrement.coerceIn(0, MAX_INCREMENT_MS),
                seekCountIncrement = seekIncrement.coerceIn(0, MAX_SEEKS),
                playbackSpeed = probe.speed.coerceIn(MIN_SPEED, MAX_SPEED),
                timestampMillis = now,
            ),
        )
    }

    companion object {
        /**
         * A tracker that measures nothing, for tests and previews.
         *
         * The scope is already cancelled, so [startView] starts no ticker and
         * leaks nothing — a test about a ViewModel's paging or engagement does
         * not want a one-second coroutine running underneath it.
         */
        fun disabled(): VideoWatchTracker = VideoWatchTracker(
            analytics = NoOpAnalyticsRecorder,
            scope = CoroutineScope(Job().apply { cancel() }),
            main = Dispatchers.Unconfined,
        )

        /**
         * How often a running watch is REPORTED. See the class comment.
         */
        const val HEARTBEAT_INTERVAL_MS = 5_000L

        /**
         * How often the playhead is READ.
         *
         * One second matches the finest thing anything downstream cares about
         * — `VIEW_1S`, and the 3-second display-view bar — while costing a
         * quarter of what the reels progress bar already spends at 250ms.
         */
        const val SAMPLE_INTERVAL_MS = 1_000L

        /** Baseline lost to a seek: the next sample re-establishes it, crediting nothing. */
        private const val UNKNOWN_PLAYHEAD = -1L

        /** A playhead this close to either end counts a wrap as a loop, not a seek. */
        private const val LOOP_TOLERANCE_MS = 1_500L

        private const val MAX_INCREMENT_MS = 10L * 60 * 1000
        private const val MAX_LOOP_COUNT = 20
        private const val MAX_SEEKS = 1000
        private const val MIN_SPEED = 0.25f
        private const val MAX_SPEED = 4.0f
        private const val PERCENT = 100
    }
}
