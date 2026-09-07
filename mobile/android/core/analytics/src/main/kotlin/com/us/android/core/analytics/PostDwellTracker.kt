package com.us.android.core.analytics

import com.us.android.core.common.di.ApplicationScope
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * How long a post held the reader — for the posts that never play.
 *
 * ## THE GAP THIS CLOSES
 *
 * The feed ranker keeps `MediaPrefs.VideoP95Dwell`, `ImageP95Dwell` and
 * `TextP95Dwell` — the viewer's 95th-percentile dwell per media type — and
 * uses them to learn whether someone would rather be shown video, photos or
 * writing. Only the first can ever be filled: it is computed from
 * `play_end.watched_ms_total`, and of the thirteen accepted event types NONE
 * measures time spent on a post that does not play. So a reader who studies
 * every caption and skips every video looks, to the ranker, like a person with
 * no preferences at all.
 *
 * ## WHY THIS RIDES ON `impression` AND NOT A NEW EVENT
 *
 * The server's type list is closed — `model.VideoEventNames` — and
 * `IngestEvents` rejects the WHOLE batch on the first unrecognised type, so an
 * invented `dwell` event would not merely be ignored, it would take every real
 * view travelling with it down too. `impression` already carries `visible_ms`,
 * bounded to ten minutes, and `normalizeEvent` stamps the row with the
 * SERVER's own `content_type` from the PostCreated ownership projection. That
 * is the whole shape a dwell measurement needs: how long, on what kind of
 * thing, for which viewer. Nothing had to be added to the wire.
 *
 * (What the server still cannot do with it is split image from text: its
 * ownership `content_type` is `post` for both. See the note in
 * `analytics-service/internal/personalization/warmer.go`. That is a server-side
 * distinction to make, not a field this client can add — the ingest validator
 * copies a fixed set of keys into the stored payload and silently drops
 * everything else, so a `media_type` sent from here would vanish.)
 *
 * ## WHAT COUNTS AS DWELL
 *
 * Time the post was the one substantially on screen AND the app was actually
 * in front of the reader. Not "time the app was open": the caller passes
 * `running = false` whenever the feed is not what is being looked at — the
 * screen not resumed, a comments sheet up, the media viewer over the list —
 * which is exactly the condition the feed's autoplay already pauses on. The
 * visibility judgement itself is not made here; it is the feed's existing
 * most-visible-row rule, so there is one answer to "which card is the reader
 * on" and not two.
 *
 * ## THE FLOOR
 *
 * [MIN_REPORTABLE_MS]. A fling past twenty cards must not produce twenty
 * events, and — more importantly — must not produce twenty NUMBERS, because
 * these are fed into a percentile: a queue full of 200ms glances would drag
 * every viewer's dwell distribution down and the ranker would conclude nobody
 * likes anything. One second is the same bar `VIEW_1S` sets for video, so
 * "counted" means one thing across media types.
 */
@Singleton
class PostDwellTracker @Inject constructor(
    private val analytics: AnalyticsRecorder,
    @ApplicationScope private val scope: CoroutineScope,
) {

    /**
     * The post the reader is on, what it has accrued, and when its clock last
     * started. `null` when nothing is being dwelt on.
     */
    private var open: OpenDwell? = null

    private class OpenDwell(val target: DwellTarget) {
        var accruedMs: Long = 0

        /** When the clock last started, or [NOT_RUNNING] while it is paused. */
        var runningSinceMillis: Long = NOT_RUNNING
    }

    /**
     * The reader is now on [target], and the clock is [running].
     *
     * One entry point rather than start/stop/pause/resume, because the caller
     * is a Compose effect keyed on exactly those two values: it cannot tell
     * which of them changed, and it should not have to.
     *
     * Moving to a different post closes the previous one, which is what emits
     * its impression. A [target] of null closes without opening — leaving the
     * feed, or nothing clearing the visibility bar.
     */
    @Synchronized
    fun onDwellChanged(
        target: DwellTarget?,
        running: Boolean,
        nowMillis: Long = System.currentTimeMillis(),
    ) {
        val current = open
        if (current != null && current.target.contentId == target?.contentId) {
            if (running) resume(current, nowMillis) else pause(current, nowMillis)
            return
        }
        if (current != null) {
            pause(current, nowMillis)
            emit(current, nowMillis)
        }
        open = target?.let { OpenDwell(it).also { fresh -> if (running) resume(fresh, nowMillis) } }
    }

    /**
     * Closes the open dwell and WAITS for its write.
     *
     * Called on the way to the background and on sign-out, both of which flush
     * the queue immediately afterwards — a merely-scheduled write at that
     * moment is a measurement deleted before it was ever sent. Same reasoning,
     * and the same ordering, as [VideoWatchTracker.endAll].
     */
    suspend fun endAll(nowMillis: Long = System.currentTimeMillis()) {
        val current = synchronized(this) { open.also { open = null } } ?: return
        pause(current, nowMillis)
        val visibleMs = reportableMs(current.accruedMs) ?: return
        analytics.recordNow(impressionFor(current.target, visibleMs, nowMillis))
    }

    private fun resume(dwell: OpenDwell, nowMillis: Long) {
        if (dwell.runningSinceMillis == NOT_RUNNING) dwell.runningSinceMillis = nowMillis
    }

    private fun pause(dwell: OpenDwell, nowMillis: Long) {
        val since = dwell.runningSinceMillis
        if (since == NOT_RUNNING) return
        dwell.runningSinceMillis = NOT_RUNNING
        // A clock that has gone backwards (the device's time was changed
        // mid-scroll) credits nothing rather than a negative.
        dwell.accruedMs += (nowMillis - since).coerceAtLeast(0)
    }

    private fun emit(dwell: OpenDwell, nowMillis: Long): Job? {
        val visibleMs = reportableMs(dwell.accruedMs) ?: return null
        return analytics.record(impressionFor(dwell.target, visibleMs, nowMillis))
    }

    private fun impressionFor(target: DwellTarget, visibleMs: Long, nowMillis: Long): AnalyticsEvent? =
        AnalyticsEvents.impression(
            session = WatchSession.forEngagement(
                contentId = target.contentId,
                surface = target.surface,
                creatorId = target.creatorId,
                position = target.position,
            ),
            visibleMs = visibleMs,
            isAutoplay = target.isAutoplay,
            timestampMillis = nowMillis,
        )

    /**
     * A convenience for callers that are not in a coroutine — the app going
     * behind, where the caller already owns a scope.
     */
    fun flush() {
        scope.launch { endAll() }
    }

    companion object {
        /**
         * A tracker that measures nothing, for tests and previews — the same
         * escape hatch [VideoWatchTracker.disabled] provides.
         */
        fun disabled(): PostDwellTracker = PostDwellTracker(
            analytics = NoOpAnalyticsRecorder,
            scope = CoroutineScope(Job().apply { cancel() }),
        )

        /**
         * Below this a dwell is not worth a row, a request or a data point.
         *
         * One second, matching `VIEW_1S`. A fling at two cards a second leaves
         * no card past the feed's 60 % visibility bar for anything like this
         * long, so twenty cards flung past produce nothing; a genuine glance at
         * a photo — one to two seconds — is still recorded, and that glance is
         * precisely the signal that separates "prefers photos" from "no
         * preferences".
         */
        const val MIN_REPORTABLE_MS = 1_000L

        /**
         * The server's own ceiling on `visible_ms` (`maxImpressionVisibleMS`).
         * A card left on screen while the reader does something else is capped
         * rather than dropped: the batch it rides in must not be refused.
         */
        const val MAX_REPORTABLE_MS = 10L * 60 * 1000

        /**
         * The `visible_ms` to send for [accruedMs], or null when it is below
         * the floor. Pure, so the floor and the ceiling are table-testable.
         */
        fun reportableMs(accruedMs: Long): Long? =
            if (accruedMs < MIN_REPORTABLE_MS) null else accruedMs.coerceAtMost(MAX_REPORTABLE_MS)

        private const val NOT_RUNNING = -1L
    }
}

/**
 * The post a dwell is being measured on.
 *
 * [isAutoplay] is the impression's own wire field and means "this card was
 * playing by itself while it was on screen" — true for the one autoplaying
 * video row, false for a photo, a poll or a text post.
 */
data class DwellTarget(
    val contentId: String,
    val creatorId: String,
    val surface: AnalyticsSurface,
    val isAutoplay: Boolean,
    /** Feed rank, 1-based; null off a ranked surface. */
    val position: Int? = null,
)
