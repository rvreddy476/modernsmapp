package com.us.android.core.network

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.interceptor.TracingInterceptor
import com.us.android.core.telemetry.Operation
import com.us.android.core.telemetry.StatusClass
import com.us.android.core.telemetry.Telemetry
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.SocketEffect
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.After
import org.junit.Before
import org.junit.Test

/** Records what was emitted so the cardinality rule can be asserted. */
private class RecordingTelemetry(
    private val header: String? = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
) : Telemetry {
    data class Sample(val operation: Operation, val status: StatusClass, val durationMillis: Long)

    val samples = mutableListOf<Sample>()
    val errors = mutableListOf<Pair<String, Map<String, String>>>()

    override fun recordOperation(operation: Operation, statusClass: StatusClass, durationMillis: Long) {
        samples += Sample(operation, statusClass, durationMillis)
    }

    override fun recordError(event: String, cause: Throwable?, attributes: Map<String, String>) {
        errors += event to attributes
    }

    override fun traceParentHeader(): String? = header

    override fun <T> span(operation: Operation, block: () -> T): T = block()
}

/**
 * Acceptance tests for audit B6.
 *
 * The client half of "one trace spanning app → gateway → auth-service": the
 * request must carry a well-formed W3C `traceparent`, and the RED sample must
 * carry ONLY low-cardinality dimensions.
 */
class TracingInterceptorTest {

    private lateinit var server: MockWebServer

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() = server.close()

    private fun clientWith(telemetry: Telemetry) = OkHttpClient.Builder()
        .addInterceptor(TracingInterceptor(telemetry))
        .retryOnConnectionFailure(false)
        .build()

    private fun get(client: OkHttpClient, path: String = "/v1/feed/home") =
        client.newCall(Request.Builder().url(server.url(path)).build()).execute().close()

    @Test
    fun `traceparent is injected in W3C format`() {
        server.enqueue(MockResponse.Builder().code(200).body("{}").build())
        get(clientWith(RecordingTelemetry()))

        val header = server.takeRequest().headers["traceparent"]

        assertThat(header).isNotNull()
        // version(2)-traceId(32)-spanId(16)-flags(2), hyphen separated.
        assertThat(header).matches("^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$")
    }

    @Test
    fun `no traceparent when tracing is disabled`() {
        server.enqueue(MockResponse.Builder().code(200).body("{}").build())
        get(clientWith(RecordingTelemetry(header = null)))

        assertThat(server.takeRequest().headers["traceparent"]).isNull()
    }

    @Test
    fun `status codes bucket into classes, never raw codes`() {
        val cases = mapOf(
            200 to StatusClass.Success,
            302 to StatusClass.Success,
            404 to StatusClass.ClientError,
            422 to StatusClass.ClientError,
            500 to StatusClass.ServerError,
            503 to StatusClass.ServerError,
        )
        cases.forEach { (code, expected) ->
            val telemetry = RecordingTelemetry()
            server.enqueue(MockResponse.Builder().code(code).body("{}").build())
            get(clientWith(telemetry))
            assertThat(telemetry.samples.single().status).isEqualTo(expected)
        }
    }

    @Test
    fun `a transport failure records NetworkError and an error span`() {
        val telemetry = RecordingTelemetry()
        server.enqueue(
            MockResponse.Builder().onRequestStart(SocketEffect.CloseSocket()).build(),
        )

        runCatching { get(clientWith(telemetry)) }

        assertThat(telemetry.samples.single().status).isEqualTo(StatusClass.NetworkError)
        assertThat(telemetry.errors.single().first).isEqualTo("http.request.failed")
    }

    /**
     * The cardinality rule, asserted rather than trusted.
     *
     * A path containing an id — `/v1/profiles/{uuid}` — as a metric dimension
     * would mint a new time series per profile viewed. It belongs on the span.
     */
    @Test
    fun `url and ids never reach a metric dimension`() {
        val telemetry = RecordingTelemetry()
        server.enqueue(MockResponse.Builder().code(200).body("{}").build())

        get(clientWith(telemetry), "/v1/profiles/3697ae63-0000-4000-8000-000000000000")

        val sample = telemetry.samples.single()
        // The operation is a fixed enum value, not the request path.
        assertThat(sample.operation).isEqualTo(Operation.HttpRequest)
        assertThat(sample.operation.metricName).doesNotContain("3697ae63")
        assertThat(sample.operation.metricName).isEqualTo("http.request")
    }

    @Test
    fun `error attributes may carry the path because they land on a span`() {
        val telemetry = RecordingTelemetry()
        server.enqueue(
            MockResponse.Builder().onRequestStart(SocketEffect.CloseSocket()).build(),
        )

        runCatching { get(clientWith(telemetry), "/v1/auth/login") }

        val (_, attributes) = telemetry.errors.single()
        assertThat(attributes["url.path"]).isEqualTo("/v1/auth/login")
        assertThat(attributes["http.method"]).isEqualTo("GET")
    }
}
