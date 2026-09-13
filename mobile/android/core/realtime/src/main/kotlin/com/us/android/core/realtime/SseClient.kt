package com.us.android.core.realtime

import com.us.android.core.realtime.sse.SseFrame
import com.us.android.core.realtime.sse.SseParser
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.channelFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import okhttp3.Call
import okhttp3.HttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.IOException
import java.io.InputStreamReader

/**
 * The realtime subscription client for notification-service
 * `GET /v1/realtime/sse?topics=t1,t2&token=<topic token>`.
 *
 * Connection policy:
 *  - reconnects on every drop, sending `Last-Event-ID` so the server resumes
 *    from its Redis stream rather than live-tailing past what was missed;
 *  - backs off 1 s, 2 s, 4 s … 30 s with jitter ([ReconnectBackoff]), and
 *    RESETS the backoff once a connection is established (a 200);
 *  - 401 (INVALID_TOKEN, MISSING_TOKEN — an expired topic token — or the
 *    gateway's UNAUTHORIZED) asks the [RealtimeTokenSource] for a fresh token
 *    and reconnects immediately; a second consecutive 401 still refreshes but
 *    backs off, so a broken issuer cannot spin a hot loop;
 *  - 403 TOPIC_FORBIDDEN refreshes once; a second consecutive one completes
 *    the flow with [RealtimeException.TopicForbidden];
 *  - 400 (NO_TOPICS) or a 403 with another code is terminal
 *    ([RealtimeException.Rejected]); 429 RATE_LIMIT_* and 5xx back off.
 *
 * The flow is cold: collecting opens the stream, cancelling the collector
 * cancels the in-flight call.
 */
class SseClient(
    private val client: OkHttpClient,
    private val baseUrl: HttpUrl,
    private val json: Json,
    private val backoff: ReconnectBackoff,
    private val clock: RealtimeClock,
    private val ioDispatcher: CoroutineDispatcher,
) {

    fun connect(topics: List<String>, tokenSource: RealtimeTokenSource): Flow<RealtimeEvent> = channelFlow {
        val session = Session()
        while (true) {
            val token = fetchToken(tokenSource, session.refreshNext)
            session.refreshNext = false
            if (token == null) {
                clock.delay(session.nextDelay())
                continue
            }
            val outcome = open(buildRequest(topics, token, session.lastEventId), session) { frame ->
                send(decode(frame))
            }
            when (outcome) {
                is Outcome.Dropped -> clock.delay(session.nextDelay())
                is Outcome.Refused -> handleRefusal(outcome, session)
            }
        }
    }

    private suspend fun handleRefusal(refusal: Outcome.Refused, session: Session) {
        when {
            refusal.status == HTTP_UNAUTHORIZED -> {
                session.refreshNext = true
                if (session.immediateAuthRetryUsed) {
                    clock.delay(session.nextDelay())
                } else {
                    session.immediateAuthRetryUsed = true
                }
            }

            refusal.status == HTTP_FORBIDDEN && refusal.code == CODE_TOPIC_FORBIDDEN -> {
                session.forbiddenStrikes++
                if (session.forbiddenStrikes >= MAX_FORBIDDEN_STRIKES) {
                    throw RealtimeException.TopicForbidden(refusal.message)
                }
                session.refreshNext = true
            }

            refusal.status == HTTP_BAD_REQUEST || refusal.status == HTTP_FORBIDDEN ->
                throw RealtimeException.Rejected(refusal.status, refusal.code, refusal.message)

            else -> clock.delay(session.nextDelay())
        }
    }

    @Suppress("TooGenericExceptionCaught")
    private suspend fun fetchToken(source: RealtimeTokenSource, forceRefresh: Boolean): String? = try {
        source.token(forceRefresh).takeIf { it.isNotBlank() }
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        null
    }

    private fun buildRequest(topics: List<String>, token: String, lastEventId: String?): Request {
        val url = requireNotNull(baseUrl.resolve(SSE_PATH)) { "cannot resolve $SSE_PATH against $baseUrl" }
            .newBuilder()
            .apply { if (topics.isNotEmpty()) addQueryParameter("topics", topics.joinToString(",")) }
            .addQueryParameter("token", token)
            .build()
        return Request.Builder()
            .url(url)
            .header("Accept", "text/event-stream")
            .header("Cache-Control", "no-cache")
            .apply { if (lastEventId != null) header(HEADER_LAST_EVENT_ID, lastEventId) }
            .get()
            .build()
    }

    /**
     * Runs one connection to completion. The blocking read happens on
     * [ioDispatcher]; cancelling the caller cancels the OkHttp call, which is
     * what unblocks a socket read that thread interruption would not.
     */
    private suspend fun open(
        request: Request,
        session: Session,
        onFrame: suspend (SseFrame) -> Unit,
    ): Outcome = coroutineScope {
        val call = client.newCall(request)
        val canceller = launch(start = CoroutineStart.UNDISPATCHED) {
            try {
                awaitCancellation()
            } finally {
                call.cancel()
            }
        }
        try {
            withContext(ioDispatcher) { stream(call, session, onFrame) }
        } finally {
            canceller.cancel()
        }
    }

    private suspend fun stream(call: Call, session: Session, onFrame: suspend (SseFrame) -> Unit): Outcome {
        val response = try {
            call.execute()
        } catch (e: IOException) {
            return Outcome.Dropped
        }
        response.use { r ->
            if (r.code != HTTP_OK) {
                val error = runCatching { json.decodeFromString(WireErrorEnvelope.serializer(), r.body.string()) }
                    .getOrNull()?.error
                return Outcome.Refused(r.code, error?.code, error?.message)
            }
            session.established()
            val parser = SseParser()
            val reader = InputStreamReader(r.body.byteStream(), Charsets.UTF_8)
            val buffer = CharArray(READ_BUFFER_CHARS)
            try {
                while (true) {
                    val read = reader.read(buffer)
                    if (read < 0) break
                    val frames = parser.feed(String(buffer, 0, read))
                    parser.lastEventId?.let { session.lastEventId = it }
                    parser.retryMillis?.let { session.serverRetryMillis = it }
                    for (frame in frames) onFrame(frame)
                }
            } catch (e: IOException) {
                return Outcome.Dropped
            }
            return Outcome.Dropped
        }
    }

    private fun decode(frame: SseFrame): RealtimeEvent = runCatching {
        if (frame.event == EVENT_CONNECTED) {
            val c = json.decodeFromString(WireConnected.serializer(), frame.data)
            RealtimeEvent.Connected(
                subject = c.subject,
                topics = c.topics.split(',').map { it.trim() }.filter { it.isNotEmpty() },
                resumedFrom = c.since.takeIf { it.isNotEmpty() },
            )
        } else {
            val e = json.decodeFromString(WireEvent.serializer(), frame.data)
            RealtimeEvent.Message(
                id = frame.id,
                topic = e.topic.ifEmpty { frame.event },
                eventType = e.eventType,
                data = e.data,
                emittedAt = e.emittedAt,
            )
        }
    }.getOrElse { RealtimeEvent.Unparsed(frame.id, frame.event, frame.data) }

    /** Mutable per-subscription state, shared across its reconnects. */
    private inner class Session {
        var lastEventId: String? = null
        var serverRetryMillis: Long? = null
        var failures = 0
        var refreshNext = false
        var immediateAuthRetryUsed = false
        var forbiddenStrikes = 0

        fun nextDelay(): Long = backoff.delayMillis(failures++, serverRetryMillis)

        /** A 200: the connection is up, so every failure streak starts over. */
        fun established() {
            failures = 0
            immediateAuthRetryUsed = false
            forbiddenStrikes = 0
        }
    }

    private sealed interface Outcome {
        /** Never connected (transport failure) or the stream ended after a 200. */
        data object Dropped : Outcome

        data class Refused(val status: Int, val code: String?, val message: String?) : Outcome
    }

    @Serializable
    private data class WireConnected(
        val subject: String = "",
        val topics: String = "",
        val since: String = "",
    )

    @Serializable
    private data class WireEvent(
        val topic: String = "",
        @SerialName("event_type") val eventType: String = "",
        val data: JsonElement = JsonNull,
        @SerialName("emitted_at") val emittedAt: String? = null,
    )

    @Serializable
    private data class WireErrorEnvelope(val error: WireError? = null)

    @Serializable
    private data class WireError(val code: String? = null, val message: String? = null)

    companion object {
        const val SSE_PATH = "v1/realtime/sse"
        const val HEADER_LAST_EVENT_ID = "Last-Event-ID"
        const val CODE_INVALID_TOKEN = "INVALID_TOKEN"
        const val CODE_MISSING_TOKEN = "MISSING_TOKEN"
        const val CODE_TOPIC_FORBIDDEN = "TOPIC_FORBIDDEN"

        private const val EVENT_CONNECTED = "connected"
        private const val HTTP_OK = 200
        private const val HTTP_BAD_REQUEST = 400
        private const val HTTP_UNAUTHORIZED = 401
        private const val HTTP_FORBIDDEN = 403
        private const val MAX_FORBIDDEN_STRIKES = 2
        private const val READ_BUFFER_CHARS = 4_096
    }
}
