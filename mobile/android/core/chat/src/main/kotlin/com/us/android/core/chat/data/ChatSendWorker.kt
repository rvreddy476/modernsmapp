package com.us.android.core.chat.data

import android.content.Context
import androidx.hilt.work.HiltWorker
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import dagger.assisted.Assisted
import dagger.assisted.AssistedInject

/**
 * Drains the durable chat send outbox (directive §5.4, CH-LB-4.4).
 *
 * The worker is deliberately dumb: [ChatStore.drainOutbox] owns every rule
 * (ordering, idempotency-key replay, attempt caps, parking rows as failed).
 * A retryable outcome retries THIS worker with backoff; everything else is
 * success — the parked-failed rows wait for an explicit user retry, and
 * rescheduling for them would loop forever.
 *
 * Unique-KEEP scheduling means process death between enqueue and drain
 * resumes on the next [ChatStore.scheduleDrain] (app start) with the SAME
 * idempotency keys — the server replays, never duplicates.
 */
@HiltWorker
class ChatSendWorker @AssistedInject constructor(
    @Assisted appContext: Context,
    @Assisted params: WorkerParameters,
    private val store: ChatStore,
) : CoroutineWorker(appContext, params) {

    override suspend fun doWork(): Result {
        val delivered = runCatching { store.drainOutbox() }.getOrDefault(false)
        return if (delivered) Result.success() else Result.retry()
    }
}
