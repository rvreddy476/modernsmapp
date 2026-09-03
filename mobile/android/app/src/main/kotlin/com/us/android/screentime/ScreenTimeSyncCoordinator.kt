package com.us.android.screentime

import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.UsageAccumulator
import com.us.android.core.profile.data.WellbeingRepository
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
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
 */
@Singleton
class ScreenTimeSyncCoordinator @Inject constructor(
    private val accumulator: UsageAccumulator,
    private val repository: WellbeingRepository,
) {
    // Application-scoped and deliberately never cancelled at the top level —
    // only the per-session flush loop is. SupervisorJob so one failed report
    // does not stop the next scheduled one.
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var flushLoop: Job? = null

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
            val result = repository.reportScreenTime(record.date, record.foregroundSecs, record.sessions)
            // A failed report leaves the day in the ledger for the next
            // attempt; only a confirmed write may drop it.
            if (result is AppResult.Success) accumulator.markFlushed(record.date)
        }
    }

    private companion object {
        const val FLUSH_INTERVAL_MILLIS = 5 * 60 * 1000L
    }
}
