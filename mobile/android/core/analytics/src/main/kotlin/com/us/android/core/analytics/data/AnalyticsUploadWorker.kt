package com.us.android.core.analytics.data

import android.content.Context
import androidx.hilt.work.HiltWorker
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import dagger.assisted.Assisted
import dagger.assisted.AssistedInject

/**
 * Drains the analytics outbox.
 *
 * Deliberately dumb, the way [com.us.android.core.chat.data.ChatSendWorker] is:
 * [AnalyticsStore.drain] owns batching, the attempt cap, staleness, bisection
 * and the signed-out check. A retryable outcome retries THIS worker with
 * backoff; everything else is success.
 *
 * Going through WorkManager at all — rather than just launching a coroutine on
 * the application scope — is what makes the queue survive the case it exists
 * for. The user swipes away from a reel, backgrounds the app, and the process
 * is reclaimed a moment later; a coroutine dies with it, an enqueued job does
 * not.
 */
@HiltWorker
class AnalyticsUploadWorker @AssistedInject constructor(
    @Assisted appContext: Context,
    @Assisted params: WorkerParameters,
    private val store: AnalyticsStore,
) : CoroutineWorker(appContext, params) {

    override suspend fun doWork(): Result {
        // Telemetry never reports failure: a thrown exception here would show
        // up as a failed job in the user's system work queue for no benefit.
        val drained = runCatching { store.drain() }.getOrDefault(true)
        return if (drained) Result.success() else Result.retry()
    }
}

/**
 * Schedules the drain.
 *
 * An interface so the store and client can be unit-tested without WorkManager,
 * which needs an initialised Android context — the same seam
 * `ChatModule.provideOutboxScheduler` uses.
 */
interface AnalyticsUploadScheduler {
    fun scheduleUpload()

    fun cancelUpload()
}
