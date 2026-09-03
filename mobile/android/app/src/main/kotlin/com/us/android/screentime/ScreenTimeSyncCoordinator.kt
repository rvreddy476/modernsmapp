package com.us.android.screentime

import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.UsageAccumulator
import com.us.android.core.profile.data.WellbeingRepository
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Reports foreground usage to `POST /v1/users/me/screen-time`.
 *
 * Joins two modules `:app` alone is allowed to know about: `:core:datastore`
 * owns the local ledger ([UsageAccumulator]), `:core:profile` owns the
 * endpoint ([WellbeingRepository]). The app's `ProcessLifecycleOwner`
 * observer (`UsApplication.onCreate`) drives [onForeground] / [onBackground];
 * this class owns only what happens between those two calls.
 *
 * While foreground, a day's total is reported at most every five minutes —
 * frequent enough that "today so far" on the Screen time page is never far
 * stale, infrequent enough that scrolling a feed does not turn into a
 * background request storm. Backgrounding always flushes immediately: it is
 * the one moment a user is likely to check the Screen time page next.
 *
 * Either trigger is skipped per-date when there is nothing new to report:
 * [markFlushed][UsageAccumulator.markFlushed] never drops *today* from the
 * ledger (its total keeps growing), so it reappears in every
 * [snapshot][UsageAccumulator.snapshot] regardless of whether real time has
 * elapsed since the last confirmed report — e.g. a background flush and an
 * immediately-following foreground/background pair can both observe the same
 * whole-second total. [lastConfirmed] remembers what was last successfully
 * sent per date so an unchanged value is not re-posted; a value that has
 * never been confirmed (or changed since) is still retried every cycle.
 */
@Singleton
class ScreenTimeSyncCoordinator @Inject constructor(
    private val accumulator: UsageAccumulator,
    private val repository: WellbeingRepository,
    // The shared app-wide scope, never cancelled at the top level — only the
    // per-session flush loop is. Its SupervisorJob means one failed report
    // does not stop the next scheduled one.
    @ApplicationScope private val scope: CoroutineScope,
) {
    private var flushLoop: Job? = null

    /**
     * date -> (foregroundSecs, sessions) last confirmed by the server.
     * In-memory only: worst case, a cold start re-sends once.
     */
    private val lastConfirmed = mutableMapOf<String, Pair<Long, Int>>()

    fun onForeground() {
        flushLoop?.cancel()
        flushLoop = scope.launch {
            accumulator.onForeground()
            while (isActive) {
                delay(FLUSH_INTERVAL_MILLIS)
                flushNow()
            }
        }
    }

    fun onBackground() {
        flushLoop?.cancel()
        flushLoop = null
        scope.launch {
            accumulator.onBackground()
            flushNow()
        }
    }

    private suspend fun flushNow() {
        accumulator.snapshot().forEach { record ->
            val value = record.foregroundSecs to record.sessions
            // Nothing changed since the last confirmed report for this date:
            // skip the request rather than re-send an identical total.
            if (lastConfirmed[record.date] == value) return@forEach
            val result = repository.reportScreenTime(record.date, record.foregroundSecs, record.sessions)
            // A failed report leaves the day in the ledger for the next
            // attempt; only a confirmed write may drop it.
            if (result is AppResult.Success) {
                accumulator.markFlushed(record.date)
                lastConfirmed[record.date] = value
            }
        }
    }

    private companion object {
        const val FLUSH_INTERVAL_MILLIS = 5 * 60 * 1000L
    }
}
