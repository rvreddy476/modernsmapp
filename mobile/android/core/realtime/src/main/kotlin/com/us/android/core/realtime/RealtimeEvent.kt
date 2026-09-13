package com.us.android.core.realtime

import kotlinx.serialization.json.JsonElement

/**
 * What a realtime subscription delivers.
 *
 * The framing is notification-service's `GET /v1/realtime/sse`
 * (handler_realtime.go):
 *  - first, `event: connected` with
 *    `data: {"subject":"<user>","topics":"t1,t2","since":"<id or empty>"}`;
 *  - then per event `id: <redis stream id>`, `event: <topic>`,
 *    `data: {"topic","event_type","data","emitted_at"}` (shared/realtime Event);
 *  - `: keepalive` comments every 25 s with no events.
 */
sealed interface RealtimeEvent {

    /** The stream is open. [resumedFrom] is the Last-Event-ID the server resumed after. */
    data class Connected(
        val subject: String,
        val topics: List<String>,
        val resumedFrom: String?,
    ) : RealtimeEvent

    /** One domain event, e.g. [eventType] `food.order.placed` on `food.restaurant.<id>.orders`. */
    data class Message(
        val id: String?,
        val topic: String,
        val eventType: String,
        val data: JsonElement,
        /** RFC 3339, as Go marshals `time.Time`. */
        val emittedAt: String?,
    ) : RealtimeEvent

    /** A frame whose data was not the shared envelope. Surfaced, not dropped. */
    data class Unparsed(
        val id: String?,
        val event: String,
        val data: String,
    ) : RealtimeEvent
}

/**
 * The port a domain implements to hand the SSE client a topic token.
 *
 * Food issues them today through `POST /v1/food/realtime/token` (one token for
 * every topic the user owns); a scoped-token issuer is planned. The client
 * depends only on this, so that change stays on the issuing side.
 */
fun interface RealtimeTokenSource {
    /**
     * Returns a topic token. [forceRefresh] is true when the previous token was
     * refused (401 INVALID_TOKEN / MISSING_TOKEN, or 403 TOPIC_FORBIDDEN): a
     * cached token must not be returned then.
     *
     * Throwing is allowed; the client treats it as a failed attempt and backs off.
     */
    suspend fun token(forceRefresh: Boolean): String
}

/** A refusal that reconnecting cannot fix. The subscription flow completes with it. */
sealed class RealtimeException(message: String) : Exception(message) {

    /** A second consecutive 403 TOPIC_FORBIDDEN, after one token refresh. */
    class TopicForbidden(val serverMessage: String?) :
        RealtimeException("realtime topic forbidden after a token refresh: ${serverMessage.orEmpty()}")

    /** Any other terminal refusal — 400 NO_TOPICS, or a 403 with another code. */
    class Rejected(val status: Int, val code: String?, val serverMessage: String?) :
        RealtimeException("realtime subscription rejected: $status ${code.orEmpty()}")
}
