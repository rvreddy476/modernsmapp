package com.us.android.core.realtime

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.take
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.TimeUnit

/**
 * The reconnect policy against a real HTTP server.
 *
 * Every refusal body here is notification-service's own
 * (`{"error":{"code":…,"message":…},"meta":{…}}` from shared/api), and every
 * success body its own framing (handler_realtime.go).
 */
class SseClientTest {

    private lateinit var server: MockWebServer
    private val json = NetworkModule.provideJson()
    private val delays = CopyOnWriteArrayList<Long>()
    private val clock = RealtimeClock { delays += it }
    private val tokens = RecordingTokenSource()

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() = server.close()

    private fun client() = SseClient(
        client = OkHttpClient.Builder().readTimeout(10, TimeUnit.SECONDS).build(),
        baseUrl = server.url("/"),
        json = json,
        backoff = ReconnectBackoff(random = FixedRandom(0.5)),
        clock = clock,
        ioDispatcher = Dispatchers.IO,
    )

    private fun stream(body: String) = MockResponse.Builder()
        .code(200)
        .addHeader("Content-Type", "text/event-stream")
        .body(body)
        .build()

    private fun refusal(status: Int, code: String) = MockResponse.Builder()
        .code(status)
        .addHeader("Content-Type", "application/json")
        .body("""{"error":{"code":"$code","message":"refused"},"meta":{"request_id":"r-1"}}""")
        .build()

    private fun take(): RecordedRequest = checkNotNull(server.takeRequest(5, TimeUnit.SECONDS))

    private fun RecordedRequest.query(name: String) = "http://h$target".toHttpUrl().queryParameter(name)

    @Test
    fun `the stream decodes the connected frame and the shared event envelope`() {
        server.enqueue(
            stream(
                "event: connected\ndata: {\"subject\":\"u-1\",\"topics\":\"food.order.1,food.order.2\",\"since\":\"\"}\n\n" +
                    ": keepalive\n\n" +
                    "id: 1717000000000-0\nevent: food.order.1\n" +
                    "data: {\"topic\":\"food.order.1\",\"event_type\":\"food.order.confirmed\"," +
                    "\"data\":{\"status\":\"CONFIRMED\"},\"emitted_at\":\"2026-09-13T06:30:00Z\"}\n\n",
            ),
        )

        val events = runBlocking {
            withTimeout(TIMEOUT) { client().connect(listOf("food.order.1", "food.order.2"), tokens).take(2).toList() }
        }

        assertThat(events[0]).isEqualTo(
            RealtimeEvent.Connected("u-1", listOf("food.order.1", "food.order.2"), resumedFrom = null),
        )
        val message = events[1] as RealtimeEvent.Message
        assertThat(message.id).isEqualTo("1717000000000-0")
        assertThat(message.topic).isEqualTo("food.order.1")
        assertThat(message.eventType).isEqualTo("food.order.confirmed")
        assertThat(message.data.jsonObject["status"]?.jsonPrimitive?.content).isEqualTo("CONFIRMED")
        assertThat(message.emittedAt).isEqualTo("2026-09-13T06:30:00Z")

        val request = take()
        assertThat(request.url.encodedPath).isEqualTo("/v1/realtime/sse")
        assertThat(request.query("topics")).isEqualTo("food.order.1,food.order.2")
        assertThat(request.query("token")).isEqualTo("t1")
        assertThat(request.headers["Accept"]).isEqualTo("text/event-stream")
        assertThat(request.headers["Last-Event-ID"]).isNull()
    }

    @Test
    fun `a reconnect sends the last event id it saw`() {
        server.enqueue(stream("id: 1717000000000-3\nevent: food.order.1\ndata: {\"topic\":\"food.order.1\"}\n\n"))
        server.enqueue(stream("event: connected\ndata: {\"subject\":\"u-1\",\"topics\":\"food.order.1\",\"since\":\"1717000000000-3\"}\n\n"))

        val events = runBlocking {
            withTimeout(TIMEOUT) { client().connect(listOf("food.order.1"), tokens).take(2).toList() }
        }

        assertThat((events[1] as RealtimeEvent.Connected).resumedFrom).isEqualTo("1717000000000-3")
        assertThat(take().headers["Last-Event-ID"]).isNull()
        assertThat(take().headers["Last-Event-ID"]).isEqualTo("1717000000000-3")
        assertThat(delays).containsExactly(1_000L)
    }

    @Test
    fun `the backoff resets once a connection is established`() {
        server.enqueue(MockResponse.Builder().code(503).build())
        server.enqueue(MockResponse.Builder().code(503).build())
        server.enqueue(stream("event: connected\ndata: {\"subject\":\"u-1\",\"topics\":\"t\",\"since\":\"\"}\n\n"))
        server.enqueue(MockResponse.Builder().code(503).build())
        server.enqueue(MockResponse.Builder().code(503).build())

        runBlocking {
            val job = launch(Dispatchers.IO) { client().connect(listOf("t"), tokens).collect { } }
            withTimeout(TIMEOUT) { while (delays.size < 4) delay(10) }
            job.cancel()
        }

        // 503 → 1 s, 503 → 2 s, 200 then EOF → back to 1 s, 503 → 2 s.
        assertThat(delays.take(4)).containsExactly(1_000L, 2_000L, 1_000L, 2_000L).inOrder()
    }

    @Test
    fun `a 401 INVALID_TOKEN refreshes the token and reconnects immediately`() {
        server.enqueue(refusal(401, "INVALID_TOKEN"))
        server.enqueue(stream("event: connected\ndata: {\"subject\":\"u-1\",\"topics\":\"t\",\"since\":\"\"}\n\n"))

        val events = runBlocking { withTimeout(TIMEOUT) { client().connect(listOf("t"), tokens).take(1).toList() } }

        assertThat(events.single()).isInstanceOf(RealtimeEvent.Connected::class.java)
        assertThat(tokens.calls).containsExactly(false, true).inOrder()
        assertThat(take().query("token")).isEqualTo("t1")
        assertThat(take().query("token")).isEqualTo("t2")
        assertThat(delays).isEmpty()
    }

    @Test
    fun `a second consecutive 401 still refreshes but backs off`() {
        server.enqueue(refusal(401, "MISSING_TOKEN"))
        server.enqueue(refusal(401, "INVALID_TOKEN"))
        server.enqueue(stream("event: connected\ndata: {\"subject\":\"u-1\",\"topics\":\"t\",\"since\":\"\"}\n\n"))

        runBlocking { withTimeout(TIMEOUT) { client().connect(listOf("t"), tokens).take(1).toList() } }

        assertThat(tokens.calls).containsExactly(false, true, true).inOrder()
        assertThat(delays).containsExactly(1_000L)
    }

    @Test
    fun `a 403 TOPIC_FORBIDDEN refreshes once and is terminal the second time`() {
        server.enqueue(refusal(403, "TOPIC_FORBIDDEN"))
        server.enqueue(refusal(403, "TOPIC_FORBIDDEN"))
        // Only reachable if the second refusal were not terminal.
        server.enqueue(stream("event: connected\ndata: {\"subject\":\"u-1\",\"topics\":\"t\",\"since\":\"\"}\n\n"))

        val thrown = runCatching {
            runBlocking { withTimeout(TIMEOUT) { client().connect(listOf("t"), tokens).take(1).toList() } }
        }.exceptionOrNull()

        assertThat(thrown).isInstanceOf(RealtimeException.TopicForbidden::class.java)
        assertThat(tokens.calls).containsExactly(false, true).inOrder()
    }

    @Test
    fun `a single 403 TOPIC_FORBIDDEN followed by a fresh token connects`() {
        server.enqueue(refusal(403, "TOPIC_FORBIDDEN"))
        server.enqueue(stream("event: connected\ndata: {\"subject\":\"u-1\",\"topics\":\"t\",\"since\":\"\"}\n\n"))

        val events = runBlocking { withTimeout(TIMEOUT) { client().connect(listOf("t"), tokens).take(1).toList() } }

        assertThat(events.single()).isInstanceOf(RealtimeEvent.Connected::class.java)
        assertThat(tokens.calls).containsExactly(false, true).inOrder()
    }

    @Test
    fun `a 400 NO_TOPICS is terminal without retrying`() {
        server.enqueue(refusal(400, "NO_TOPICS"))

        val thrown = runCatching {
            runBlocking { withTimeout(TIMEOUT) { client().connect(emptyList(), tokens).toList() } }
        }.exceptionOrNull()

        assertThat(thrown).isInstanceOf(RealtimeException.Rejected::class.java)
        assertThat((thrown as RealtimeException.Rejected).code).isEqualTo("NO_TOPICS")
        assertThat(take().query("topics")).isNull()
    }

    @Test
    fun `a token source failure backs off and tries again`() {
        server.enqueue(stream("event: connected\ndata: {\"subject\":\"u-1\",\"topics\":\"t\",\"since\":\"\"}\n\n"))
        tokens.failNext = true

        runBlocking { withTimeout(TIMEOUT) { client().connect(listOf("t"), tokens).take(1).toList() } }

        assertThat(delays).containsExactly(1_000L)
        assertThat(server.requestCount).isEqualTo(1)
    }

    class RecordingTokenSource : RealtimeTokenSource {
        val calls = CopyOnWriteArrayList<Boolean>()
        @Volatile var failNext = false

        override suspend fun token(forceRefresh: Boolean): String {
            calls += forceRefresh
            if (failNext) {
                failNext = false
                error("issuer unavailable")
            }
            return "t${calls.size}"
        }
    }

    private companion object {
        const val TIMEOUT = 10_000L
    }
}
