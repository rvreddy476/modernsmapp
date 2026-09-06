package com.us.android.core.analytics.data

import com.us.android.core.analytics.AnalyticsEvent
import com.us.android.core.analytics.AnalyticsValidation
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.database.AnalyticsDao
import com.us.android.core.database.AnalyticsPendingEventEntity
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import java.time.Instant
import java.time.format.DateTimeFormatter
import javax.inject.Inject
import javax.inject.Provider
import javax.inject.Singleton

/**
 * The analytics outbox: enqueue on disk, drain in batches, delete on ack.
 *
 * Every policy the pipeline has lives here, so [AnalyticsUploadWorker] can stay
 * as dumb as [com.us.android.core.chat.data.ChatSendWorker] is — the worker
 * asks it to drain and reports retry-or-not.
 *
 * ## THE FOUR RULES
 *
 * 1. **Nothing blocks the caller.** [enqueue] hands the row to an IO dispatcher
 *    and returns. A player surface calling this on the main thread does no
 *    disk work on the main thread, and an enqueue that fails fails silently:
 *    telemetry must never become an error the person using the app can see.
 * 2. **A retry replays the same `event_id`.** The id is generated once, at
 *    enqueue, and stored. The server counts a repeated id as `duplicate`, so a
 *    flaky network costs a creator nothing; a client that minted fresh ids per
 *    attempt would invent views.
 * 3. **One bad event cannot wedge the queue.** The server fails a whole batch
 *    on its first invalid event. So a permanently-rejected batch is BISECTED
 *    until the offender is alone, and only the offender is dropped.
 * 4. **It gives up.** Rows exhaust their attempts, stale rows are dropped
 *    before they are sent, and the table is capped. An analytics queue that
 *    grows without bound on a device that has been offline for a week is a
 *    bug, not durability.
 */
@Singleton
class AnalyticsStore @Inject constructor(
    private val dao: AnalyticsDao,
    private val api: AnalyticsApi,
    private val errorMapper: ErrorMapper,
    /**
     * A `Provider`, not the instance, and this is load-bearing.
     *
     * `SessionStateProvider` is `AuthRepository`, which injects the
     * `Set<SessionTeardownTask>` that [AnalyticsTeardown] belongs to, which
     * needs this store — a genuine construction cycle Dagger rejects at
     * compile time. Asking for the session lazily breaks it, and it is what
     * the code wants anyway: the store needs the session state at DRAIN time,
     * never at construction.
     */
    private val sessionState: Provider<SessionStateProvider>,
    private val json: Json,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) {

    /**
     * Serialises drains against each other.
     *
     * Two drains running at once would read the same rows and send them twice.
     * That is harmless on the server — `event_id` collapses them — but it
     * doubles the request count for nothing, and the periodic flush racing the
     * flush-on-background is the normal case, not an edge one.
     */
    private val drainMutex = Mutex()

    private val timestampFormat: DateTimeFormatter = DateTimeFormatter.ISO_INSTANT

    suspend fun enqueue(event: AnalyticsEvent) {
        if (!sessionState.get().sessionState.value.isAuthenticated) return
        withContext(io) {
            runCatching {
                dao.enqueue(
                    AnalyticsPendingEventEntity(
                        eventId = event.eventId,
                        type = event.type,
                        timestamp = timestampFormat.format(Instant.ofEpochMilli(event.timestampMillis)),
                        payloadJson = json.encodeToString(JsonObject.serializer(), event.payload),
                        sessionId = event.sessionId,
                        contentId = event.contentId,
                        dedupeKey = event.dedupeKey,
                        createdAtMillis = event.timestampMillis,
                    ),
                )
                dao.trimTo(QUEUE_CAP)
            }
        }
    }

    suspend fun queueSize(): Int = withContext(io) { runCatching { dao.count() }.getOrDefault(0) }

    /**
     * Sends everything queued.
     *
     * @return true when the queue is drained or there is nothing more worth
     * trying, false when a transient failure means the worker should retry with
     * backoff. Never throws.
     */
    suspend fun drain(nowMillis: Long = System.currentTimeMillis()): Boolean = drainMutex.withLock {
        withContext(io) {
            runCatching { drainLocked(nowMillis) }.getOrDefault(true)
        }
    }

    private suspend fun drainLocked(nowMillis: Long): Boolean {
        // Signed out: the gateway would send the request with no actor and the
        // service would refuse it. Nothing to do, and nothing to retry.
        if (!sessionState.get().sessionState.value.isAuthenticated) return true

        dao.dropExhausted(MAX_ATTEMPTS)

        while (true) {
            val rows = dao.oldest(MAX_BATCH)
            if (rows.isEmpty()) return true

            // Drop what the server would reject for being too old before it
            // can take a live batch down with it.
            val (fresh, stale) = rows.partition { AnalyticsValidation.isFresh(it.createdAtMillis, nowMillis) }
            if (stale.isNotEmpty()) {
                dao.delete(stale.map { it.eventId })
            }
            if (fresh.isEmpty()) continue

            dao.recordAttempt(fresh.map { it.eventId })
            when (send(fresh)) {
                Outcome.Delivered -> Unit // loop for the next batch
                Outcome.Retry -> return false
                Outcome.Stop -> return true
            }
        }
    }

    private enum class Outcome { Delivered, Retry, Stop }

    /**
     * Sends one batch, isolating a poisoned event by bisection.
     *
     * A permanent rejection is a statement about ONE event in the batch, but
     * the server does not say which. Halving until the offender is alone costs
     * at most log2(batch) extra requests and happens rarely; the alternatives
     * are dropping the whole batch (losing up to a hundred real views to one
     * bad row) or retrying it forever (a queue that never drains again).
     */
    private suspend fun send(rows: List<AnalyticsPendingEventEntity>): Outcome =
        when (val result = post(rows)) {
            is AppResult.Success -> {
                dao.delete(rows.map { it.eventId })
                Outcome.Delivered
            }

            is AppResult.Failure -> when (classify(result.error)) {
                Failure.Transient -> Outcome.Retry

                // No session, or it expired mid-drain. Rows stay; sign-out
                // clears them, and a re-auth drains them.
                Failure.Unauthenticated -> Outcome.Stop

                Failure.Permanent -> isolate(rows)
            }
        }

    /**
     * Narrows a permanently-rejected batch to the event that caused it.
     *
     * Halving costs at most log2(batch) extra requests and happens rarely. The
     * alternatives are dropping the whole batch — up to a hundred real views
     * lost to one bad row — or retrying it forever, which leaves the queue
     * unable to deliver anything ever again.
     */
    private suspend fun isolate(rows: List<AnalyticsPendingEventEntity>): Outcome {
        if (rows.size == 1) {
            // Found it. It will never be accepted, so it stops existing —
            // silently, as telemetry must.
            dao.delete(rows.map { it.eventId })
            return Outcome.Delivered
        }
        val half = rows.size / 2
        return when (send(rows.take(half))) {
            Outcome.Delivered -> send(rows.drop(half))
            else -> Outcome.Retry
        }
    }

    private suspend fun post(rows: List<AnalyticsPendingEventEntity>): AppResult<IngestResponse> =
        apiCall(errorMapper) {
            api.ingest(
                IngestRequest(
                    events = rows.map { row ->
                        EventDto(
                            eventId = row.eventId,
                            type = row.type,
                            timestamp = row.timestamp,
                            payload = json.decodeFromString(JsonObject.serializer(), row.payloadJson),
                        )
                    },
                ),
            )
        }

    private enum class Failure { Transient, Permanent, Unauthenticated }

    /**
     * Which failures are worth trying again.
     *
     * `CONTENT_NOT_READY` (422) is the interesting one: it means the
     * PostCreated ownership projection has not caught up with a video that was
     * just published. That is transient by nature — and dropping it would lose
     * a view on exactly the freshest content, which is when views matter most
     * to a creator. So it retries, isolated by the bisection above so it holds
     * nobody else up, and is eventually dropped by the attempt cap.
     */
    private fun classify(error: AppError): Failure = when (error) {
        is AppError.NoNetwork, is AppError.Timeout, is AppError.RateLimited -> Failure.Transient
        is AppError.Server -> Failure.Transient
        is AppError.AuthFailed -> Failure.Unauthenticated
        is AppError.Unknown ->
            if (error.code == CODE_CONTENT_NOT_READY || error.statusCode == null) {
                Failure.Transient
            } else {
                Failure.Permanent
            }
        // INVALID_ANALYTICS_EVENT and anything else the server refuses to
        // parse: replaying it would produce the same answer forever.
        else -> Failure.Permanent
    }

    /** Sign-out / account switch: the queue belongs to the session that made it. */
    suspend fun clear() {
        withContext(io) { runCatching { dao.clear() } }
    }

    companion object {
        const val UPLOAD_WORK_NAME = "analytics_upload"

        /**
         * Events per request.
         *
         * The server's hard cap is 200. A hundred keeps request bodies small
         * enough to succeed on a bad mobile connection, halves the worst-case
         * bisection depth, and still means a heavy viewing session costs a
         * handful of requests rather than hundreds.
         */
        const val MAX_BATCH = 100

        /**
         * How many rows the queue may hold.
         *
         * At the cadences in `VideoWatchTracker` a very heavy hour of viewing
         * produces on the order of a thousand events, so this holds roughly a
         * day of offline use. Past it the oldest go first — they are closest to
         * the server's 24-hour window anyway.
         */
        const val QUEUE_CAP = 5_000

        /**
         * Upload attempts before a row is abandoned.
         *
         * Five attempts across WorkManager's exponential backoff spans hours,
         * which is long enough to outlast a tunnel, a flight or a service
         * restart, and short enough that a genuinely unacceptable event does
         * not ride along forever.
         */
        const val MAX_ATTEMPTS = 5

        private const val CODE_CONTENT_NOT_READY = "CONTENT_NOT_READY"
    }
}
