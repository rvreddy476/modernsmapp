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
 * and pressed home, and the `play_end` that the creator's view count depends on
 * was never written. So going behind does two things in order: closes every
 * open view (which writes its `play_end` to disk), then flushes.
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
    private val analytics: AnalyticsClient,
    @ApplicationScope private val scope: CoroutineScope,
) {

    private var foregroundLoop: Job? = null

    fun onForeground() {
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
            // below actually carries them.
            tracker.endAll(PlayEndReason.BACKGROUNDED)
            analytics.flush()
        }
    }

    private companion object {
        const val FOREGROUND_FLUSH_INTERVAL_MS = 5L * 60 * 1000
    }
}
