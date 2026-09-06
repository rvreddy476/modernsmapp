package com.us.android.core.analytics.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject
import retrofit2.http.Body
import retrofit2.http.POST

/**
 * `POST /v1/analytics/events` — the ingest endpoint, and the only one this
 * module calls.
 *
 * The gateway routes `/v1/analytics` to analytics-service and stamps
 * `X-User-Id` from the verified bearer token; the client sends no identity of
 * its own. A request with no token reaches the service with no actor and is
 * refused, which is exactly why the uploader will not run while signed out.
 *
 * NOT annotated `@Retryable`. The shared [com.us.android.core.network.retry.RetryInterceptor]
 * replays a request transparently, and a transparent replay is the one thing
 * this endpoint must not get from the transport layer: retries here are the
 * outbox's job, because only the outbox knows to isolate the offending event
 * rather than re-sending the same poisoned batch. (The replay would be
 * *correct* — `event_id` de-duplicates — it would just be useless.)
 */
interface AnalyticsApi {

    @POST("v1/analytics/events")
    suspend fun ingest(@Body body: IngestRequest): ApiEnvelope<IngestResponse>
}

@Serializable
data class IngestRequest(
    /** 1..200 events. The server rejects a larger batch outright. */
    val events: List<EventDto>,
)

@Serializable
data class EventDto(
    @SerialName("event_id") val eventId: String,
    val type: String,
    /** RFC-3339 UTC, e.g. `2026-09-07T10:00:00Z`. */
    val timestamp: String,
    val payload: JsonObject,
)

@Serializable
data class IngestResponse(
    /** Rows genuinely written. */
    val accepted: Int = 0,
    /**
     * Rows the server had already seen.
     *
     * A duplicate is a SUCCESS for the client: it means the previous attempt
     * landed and this retry correctly replayed the same `event_id` instead of
     * inventing a new one. The row is deleted either way.
     */
    val duplicate: Int = 0,
)
