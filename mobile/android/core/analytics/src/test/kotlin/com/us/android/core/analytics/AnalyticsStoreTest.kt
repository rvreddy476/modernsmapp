package com.us.android.core.analytics

import com.google.common.truth.Truth.assertThat
import com.us.android.core.analytics.data.AnalyticsApi
import com.us.android.core.analytics.data.AnalyticsStore
import com.us.android.core.analytics.data.IngestRequest
import com.us.android.core.analytics.data.IngestResponse
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.database.AnalyticsDao
import com.us.android.core.database.AnalyticsPendingEventEntity
import com.us.android.core.model.SessionState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import java.io.IOException
import java.util.UUID

/**
 * The outbox rules the creator payout model depends on.
 *
 * Three of these describe failures that are silent in production and expensive
 * in aggregate: a retry that invents a new `event_id` inflates view counts, a
 * batch that is dropped wholesale loses real views to one bad row, and a batch
 * that is retried forever wedges the queue so nothing is ever delivered again.
 */
class AnalyticsStoreTest {

    // ── fakes ───────────────────────────────────────────────────────────

    /** In-memory [AnalyticsDao]; ordering and the attempt counter are real. */
    private class FakeDao : AnalyticsDao {
        val rows = linkedMapOf<String, AnalyticsPendingEventEntity>()

        override suspend fun enqueue(row: AnalyticsPendingEventEntity): Long {
            // IGNORE-on-conflict, both on the primary key and on the unique
            // (session, content, type, dedupeKey) index — same as Room.
            val clash = rows.values.any {
                it.sessionId == row.sessionId && it.contentId == row.contentId &&
                    it.type == row.type && it.dedupeKey == row.dedupeKey
            }
            if (rows.containsKey(row.eventId) || clash) return -1
            rows[row.eventId] = row
            return 1
        }

        override suspend fun oldest(limit: Int) =
            rows.values.sortedBy { it.createdAtMillis }.take(limit)

        override suspend fun count() = rows.size

        override suspend fun recordAttempt(eventIds: List<String>) {
            eventIds.forEach { id -> rows[id]?.let { rows[id] = it.copy(attempts = it.attempts + 1) } }
        }

        override suspend fun delete(eventIds: List<String>) {
            eventIds.forEach(rows::remove)
        }

        override suspend fun dropExhausted(maxAttempts: Int) {
            rows.values.filter { it.attempts >= maxAttempts }.forEach { rows.remove(it.eventId) }
        }

        override suspend fun trimTo(keep: Int) {
            rows.values.sortedByDescending { it.createdAtMillis }.drop(keep)
                .forEach { rows.remove(it.eventId) }
        }

        override suspend fun clear() = rows.clear()
    }

    private class FakeApi : AnalyticsApi {
        val requests = mutableListOf<IngestRequest>()
        var respond: (IngestRequest) -> ApiEnvelope<IngestResponse> = { accept(it) }

        override suspend fun ingest(body: IngestRequest): ApiEnvelope<IngestResponse> {
            requests += body
            return respond(body)
        }
    }

    private class FakeSession(state: SessionState) : SessionStateProvider {
        val flow = MutableStateFlow(state)
        override val sessionState: StateFlow<SessionState> get() = flow
    }

    // ── the tests ───────────────────────────────────────────────────────

    /**
     * The whole reason this module exists rather than one POST per event: a
     * scrolling session produces heartbeats and milestones by the hundred, and
     * one request each would be brutal on battery and radio.
     */
    @Test
    fun `events go out in batches, not one request each`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val store = store(dao, api)
        repeat(250) { dao.enqueue(row(createdAt = NOW + it)) }

        assertThat(store.drain(NOW)).isTrue()

        assertThat(api.requests).hasSize(3)
        assertThat(api.requests[0].events).hasSize(AnalyticsStore.MAX_BATCH)
        assertThat(api.requests[1].events).hasSize(AnalyticsStore.MAX_BATCH)
        assertThat(api.requests[2].events).hasSize(50)
        assertThat(dao.rows).isEmpty()
    }

    /** Never more than the server's hard cap of 200, whatever the queue holds. */
    @Test
    fun `no request ever exceeds the server's batch limit`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        repeat(500) { dao.enqueue(row(createdAt = NOW + it)) }

        store(dao, api).drain(NOW)

        assertThat(api.requests.map { it.events.size }.max()).isAtMost(200)
    }

    /**
     * THE dedup rule. A retry must replay the id the first attempt used: the
     * server counts a repeated `event_id` as `duplicate` rather than a second
     * view, so a client that minted a fresh uuid per attempt would turn one
     * flaky connection into several paid views.
     */
    @Test
    fun `a retry replays the same event_id`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val store = store(dao, api)
        dao.enqueue(row(eventId = "event-0000000000000001"))

        api.respond = { throw IOException("radio off") }
        assertThat(store.drain(NOW)).isFalse()
        // Still queued: a transient failure must not discard the view.
        assertThat(dao.rows).hasSize(1)

        api.respond = { accept(it) }
        assertThat(store.drain(NOW)).isTrue()

        assertThat(api.requests).hasSize(2)
        val ids = api.requests.map { it.events.single().eventId }
        assertThat(ids).containsExactly("event-0000000000000001", "event-0000000000000001")
    }

    /**
     * A server that answers `duplicate` has already got the event. Treating
     * that as failure would retry it forever; treating it as success is what
     * makes the whole retry scheme safe.
     */
    @Test
    fun `a duplicate response clears the row like an accept does`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        dao.enqueue(row())
        api.respond = { ApiEnvelope(data = IngestResponse(accepted = 0, duplicate = 1)) }

        assertThat(store(dao, api).drain(NOW)).isTrue()

        assertThat(dao.rows).isEmpty()
    }

    /**
     * The failure mode this client is shaped around.
     *
     * `IngestEvents` returns on its FIRST invalid event, rejecting the whole
     * request. Without isolation, one permanently-bad row either takes every
     * good view in its batch down with it or wedges the queue for good. The
     * store bisects until the offender is alone, drops only it, and delivers
     * the rest.
     */
    @Test
    fun `one poisoned event is isolated and dropped, and the rest are delivered`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        repeat(8) { dao.enqueue(row(eventId = "event-000000000000000$it", createdAt = NOW + it)) }
        val poison = "event-0000000000000005"
        api.respond = { request ->
            if (request.events.any { it.eventId == poison }) {
                badRequest("INVALID_ANALYTICS_EVENT")
            } else {
                accept(request)
            }
        }

        assertThat(store(dao, api).drain(NOW)).isTrue()

        assertThat(dao.rows).isEmpty()
        // Every non-poisoned event reached the server on some request.
        val delivered = api.requests
            .filterNot { req -> req.events.any { it.eventId == poison } }
            .flatMap { it.events }
            .map { it.eventId }
            .toSet()
        assertThat(delivered).hasSize(7)
        assertThat(delivered).doesNotContain(poison)
    }

    /**
     * `CONTENT_NOT_READY` means the ownership projection has not caught up with
     * a just-published video. Dropping it would lose views on exactly the
     * freshest content, so it retries rather than being discarded.
     */
    @Test
    fun `content not ready is retried rather than dropped`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        dao.enqueue(row())
        api.respond = { unprocessable("CONTENT_NOT_READY") }

        assertThat(store(dao, api).drain(NOW)).isFalse()

        assertThat(dao.rows).hasSize(1)
    }

    /** Telemetry is not worth unbounded retries. */
    @Test
    fun `a row that exhausts its attempts is dropped silently`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        dao.enqueue(row(attempts = AnalyticsStore.MAX_ATTEMPTS))

        assertThat(store(dao, api).drain(NOW)).isTrue()

        assertThat(dao.rows).isEmpty()
        assertThat(api.requests).isEmpty()
    }

    /**
     * The server refuses anything older than 24 hours and fails the whole batch
     * for it, so a stale row is dropped before it can take live views with it.
     */
    @Test
    fun `a stale event is dropped before it can poison a live batch`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val day = 24L * 60 * 60 * 1000
        dao.enqueue(row(eventId = "event-0000000000000old", createdAt = NOW - day - 1))
        dao.enqueue(row(eventId = "event-0000000000000new", createdAt = NOW - 1_000))

        assertThat(store(dao, api).drain(NOW)).isTrue()

        assertThat(api.requests.single().events.single().eventId).isEqualTo("event-0000000000000new")
        assertThat(dao.rows).isEmpty()
    }

    /**
     * Signed out, the gateway forwards no actor and analytics-service refuses
     * the batch. Nothing is attempted, and nothing is reported as an error —
     * telemetry must never surface anything to the person.
     */
    @Test
    fun `nothing is sent while signed out`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val session = FakeSession(SessionState.Unauthenticated)
        dao.enqueue(row())

        assertThat(store(dao, api, session).drain(NOW)).isTrue()

        assertThat(api.requests).isEmpty()
    }

    @Test
    fun `enqueue is refused while signed out`() = runTest {
        val dao = FakeDao()
        val session = FakeSession(SessionState.Unauthenticated)
        val store = store(dao, FakeApi(), session)

        store.enqueue(
            AnalyticsEvents.playEnd(
                WatchSession.start(
                    contentId = UUID.randomUUID().toString(),
                    creatorId = UUID.randomUUID().toString(),
                    surface = AnalyticsSurface.FEED,
                    contentDurationMs = 30_000,
                ),
                PlayEndReason.ENDED,
                10_000,
                10_000,
                0,
                NOW,
            )!!,
        )

        assertThat(dao.rows).isEmpty()
    }

    /** Local mirror of the server's own collapse rule, so a double tap costs nothing. */
    @Test
    fun `the same signal twice in one session occupies one queue slot`() = runTest {
        val dao = FakeDao()
        val store = store(dao, FakeApi())
        val session = WatchSession.forEngagement(UUID.randomUUID().toString(), AnalyticsSurface.FEED)

        repeat(2) {
            store.enqueue(AnalyticsEvents.engagement(AnalyticsEventType.LIKE, session, NOW)!!)
        }

        assertThat(dao.rows).hasSize(1)
    }

    // ── helpers ─────────────────────────────────────────────────────────

    /**
     * The store's IO dispatcher shares the test's scheduler, so `runTest`
     * drives the drain in virtual time rather than racing it on a real thread.
     */
    private fun TestScope.store(
        dao: AnalyticsDao,
        api: AnalyticsApi,
        session: SessionStateProvider = FakeSession(SessionState.Authenticated("u1", "s1")),
    ) = AnalyticsStore(
        dao = dao,
        api = api,
        errorMapper = ErrorMapper(Json { ignoreUnknownKeys = true }),
        sessionState = { session },
        json = Json { ignoreUnknownKeys = true },
        io = StandardTestDispatcher(testScheduler),
    )

    private companion object {
        const val NOW = 1_757_000_000_000L

        fun row(
            eventId: String = UUID.randomUUID().toString(),
            createdAt: Long = NOW,
            attempts: Int = 0,
        ) = AnalyticsPendingEventEntity(
            eventId = eventId,
            type = AnalyticsEventType.PLAY_END,
            timestamp = "2026-09-07T10:00:00Z",
            payloadJson = """{"content_id":"${UUID.randomUUID()}"}""",
            sessionId = UUID.randomUUID().toString(),
            contentId = UUID.randomUUID().toString(),
            dedupeKey = "end",
            createdAtMillis = createdAt,
            attempts = attempts,
        )

        fun accept(request: IngestRequest) =
            ApiEnvelope(data = IngestResponse(accepted = request.events.size, duplicate = 0))

        fun badRequest(code: String): Nothing = throw HttpException(
            Response.error<Unit>(
                400,
                """{"error":{"code":"$code","message":"bad"}}"""
                    .toResponseBody("application/json".toMediaType()),
            ),
        )

        fun unprocessable(code: String): Nothing = throw HttpException(
            Response.error<Unit>(
                422,
                """{"error":{"code":"$code","message":"not ready"}}"""
                    .toResponseBody("application/json".toMediaType()),
            ),
        )
    }
}
