package com.us.android.core.analytics

import com.us.android.core.common.di.ApplicationScope
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Ties the analytics pipeline to the app being in front or behind.
 *
 * ## THE BACKGROUND TRANSITION IS THE IMPORTANT ONE
 *
 * It is the moment the process is most likely to be reclaimed, and it is where
 * every open view would otherwise be lost — the person swiped away from a reel
 * and pressed home. So going behind does two things in order: PAUSES every
 * open view (which writes its running total to disk as a heartbeat; the view
 * itself is kept, because coming back is the same view, not a second one —
 * audit M-12), then flushes. If the process never comes back, the server
 * closes the session by inactivity from what was flushed.
 *
 * The foreground cadence exists for the opposite reason: on a device that stays
 * in the app for an hour, nothing would ever be sent without it, and a day of
 * viewing would sit on disk waiting for a background that might never be clean.
 * Five minutes matches [com.us.android.screentime.ScreenTimeSyncCoordinator],
 * which is the established rhythm for this kind of periodic sync here.
 */
@Singleton
class AnalyticsAppLifecycle @Inject constructor(
    private val tracker: VideoWatchTracker,
    private val dwell: PostDwellTracker,
    private val analytics: AnalyticsClient,
    @ApplicationScope private val scope: CoroutineScope,
) {

    private var foregroundLoop: Job? = null

    fun onForeground() {
        // Views paused by the last background pick up where they were: the
        // same session, no second play_start, no second view (M-12).
        tracker.resumeAll()
        foregroundLoop?.cancel()
        foregroundLoop = scope.launch {
            while (isActive) {
                delay(FOREGROUND_FLUSH_INTERVAL_MS)
                analytics.flush()
            }
        }
        // Anything left from a previous run — a process killed before its
        // queue drained — goes out now rather than waiting five minutes.
        analytics.flush()
    }

    fun onBackground() {
        foregroundLoop?.cancel()
        foregroundLoop = null
        scope.launch {
            // Ends every open view and WAITS for the writes, so the flush
            // below actually carries them. The dwell on the card the reader was
            // looking at is closed for the same reason: the feed's own effect
            // only PAUSES its clock when the screen stops being resumed, and a
            // paused measurement that is never closed is a measurement lost
            // when the process is reclaimed.
            tracker.pauseAll()
            dwell.endAll()
            analytics.flush()
        }
    }

    private companion object {
        const val FOREGROUND_FLUSH_INTERVAL_MS = 5L * 60 * 1000
    }
}
