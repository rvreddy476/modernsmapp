package com.us.android.core.analytics

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import java.util.UUID

/**
 * One event, already shaped for the wire.
 *
 * Construction goes through the [AnalyticsEvents] factories rather than this
 * constructor so that no event can exist without having passed
 * [AnalyticsValidation] — see the class comment on [AnalyticsValidation] for
 * why a single malformed event is far more expensive than it looks.
 */
data class AnalyticsEvent(
    val eventId: String,
    val type: String,
    val timestampMillis: Long,
    val sessionId: String,
    val contentId: String,
    val dedupeKey: String,
    val payload: JsonObject,
)

/**
 * The identity of one continuous viewing session of one piece of content.
 *
 * ## WHY A SESSION IS PER-VIEW AND NOT PER-APP-LAUNCH
 *
 * The server de-duplicates on `(actor_id, session_id, content_id, event_type,
 * dedupe_key)`. If `session_id` were the app session, then re-watching the same
 * reel after scrolling back would collapse into the first view and the creator
 * would be paid once for two views. If it is minted per view, each genuine view
 * is counted and a *retry* still collapses correctly, because a retry replays
 * the row it already wrote — same `event_id` and same `session_id`, both
 * generated once at enqueue and stored on disk.
 *
 * That is the whole reason the ids are minted here, at the start of a view, and
 * never at upload time.
 */
data class WatchSession(
    val sessionId: String,
    val contentId: String,
    val creatorId: String,
    val surface: AnalyticsSurface,
    val contentDurationMs: Long,
    val contentType: AnalyticsContentType,
    /** Feed rank, 1-based. Null off a ranked surface; the server bounds it to 10 000. */
    val position: Int?,
) {
    companion object {
        fun start(
            contentId: String,
            creatorId: String,
            surface: AnalyticsSurface,
            contentDurationMs: Long,
            position: Int? = null,
            sessionId: String = UUID.randomUUID().toString(),
        ): WatchSession = WatchSession(
            sessionId = sessionId,
            contentId = contentId,
            creatorId = creatorId,
            surface = surface,
            contentDurationMs = contentDurationMs,
            contentType = AnalyticsContentType.classify(contentDurationMs),
            position = position,
        )

        /**
         * A session for content the viewer acted on without a playback session.
         *
         * A like on a text post, a save from a card three rows above the one
         * playing. The server's `requiresSession` covers only the four playback
         * events, so these carry the nil session and are still counted.
         *
         * [creatorId] defaults to empty because the server never keeps it:
         * `ingest.go` decodes it into `ClaimedCreator` and drops it, rebuilding
         * attribution from the PostCreated ownership projection. Sending a
         * blank is exactly as accurate as sending a correct one, and unlike a
         * guessed one it cannot be wrong.
         */
        fun forEngagement(
            contentId: String,
            surface: AnalyticsSurface,
            creatorId: String = "",
        ): WatchSession = WatchSession(
            sessionId = "",
            contentId = contentId,
            creatorId = creatorId,
            surface = surface,
            contentDurationMs = 0L,
            contentType = AnalyticsContentType.REEL,
            position = null,
        )
    }
}

/**
 * Builds the thirteen event payloads.
 *
 * Each factory returns null when the event would be rejected by the server. A
 * null is dropped at the call site rather than enqueued — see
 * [AnalyticsValidation].
 */
object AnalyticsEvents {

    private fun JsonObject.withCommon(session: WatchSession): JsonObject = buildJsonObject {
        this@withCommon.forEach { (k, v) -> put(k, v) }
        put("content_id", session.contentId)
        // Omitted rather than sent blank when there is no playback session:
        // the server parses a non-empty session_id as a uuid and rejects the
        // WHOLE batch if it is not one.
        if (session.sessionId.isNotEmpty()) put("session_id", session.sessionId)
        put("surface", session.surface.wire)
        // creator_id is sent for parity with the documented model but is
        // NEVER trusted: ingest.go decodes it into `ClaimedCreator` and drops
        // it, rebuilding attribution from the PostCreated ownership
        // projection. Sending it wrong cannot mis-credit anyone.
        put("creator_id", session.creatorId)
        session.position?.takeIf { it in 1..MAX_POSITION }?.let { put("position", it) }
    }

    private const val MAX_POSITION = 10_000

    private fun event(
        type: String,
        session: WatchSession,
        timestampMillis: Long,
        dedupeKey: String,
        payload: JsonObject,
    ): AnalyticsEvent? {
        val full = payload.withCommon(session)
        val candidate = AnalyticsEvent(
            eventId = UUID.randomUUID().toString(),
            type = type,
            timestampMillis = timestampMillis,
            sessionId = if (type in AnalyticsEventType.REQUIRES_SESSION) session.sessionId else "",
            contentId = session.contentId,
            dedupeKey = dedupeKey,
            payload = full,
        )
        return candidate.takeIf { AnalyticsValidation.isValid(it) }
    }

    fun impression(
        session: WatchSession,
        visibleMs: Long,
        isAutoplay: Boolean,
        timestampMillis: Long,
    ): AnalyticsEvent? = event(
        type = AnalyticsEventType.IMPRESSION,
        session = session,
        timestampMillis = timestampMillis,
        // The server does not collapse impressions, but a viewport can report
        // the same card visible twice in one session and there is nothing to
        // learn from the second. Collapsing locally saves the round trip.
        dedupeKey = "impression",
        payload = buildJsonObject {
            put("visible_ms", visibleMs)
            put("is_autoplay", isAutoplay)
        },
    )

    fun playStart(
        session: WatchSession,
        startMethod: PlayStartMethod,
        isMuted: Boolean,
        isAutoplay: Boolean,
        timeToFirstFrameMs: Long,
        initialBufferMs: Long,
        timestampMillis: Long,
    ): AnalyticsEvent? = event(
        type = AnalyticsEventType.PLAY_START,
        session = session,
        timestampMillis = timestampMillis,
        // One start per session: a session IS one playback. A resume after a
        // pause is the same view continuing, not a new one.
        dedupeKey = "start",
        payload = buildJsonObject {
            put("content_duration_ms", session.contentDurationMs)
            put("content_type", session.contentType.wire)
            put("start_method", startMethod.wire)
            put("is_muted", isMuted)
            put("is_autoplay", isAutoplay)
            put("time_to_first_frame_ms", timeToFirstFrameMs)
            put("initial_buffer_ms", initialBufferMs)
        },
    )

    @Suppress("LongParameterList") // One per wire field; grouping them would only hide the contract.
    fun heartbeat(
        session: WatchSession,
        sequence: Int,
        watchedMsIncrement: Long,
        watchedMsTotal: Long,
        playheadPositionMs: Long,
        bufferingMsIncrement: Long,
        seekCountIncrement: Int,
        playbackSpeed: Float,
        timestampMillis: Long,
    ): AnalyticsEvent? = event(
        type = AnalyticsEventType.WATCH_HEARTBEAT,
        session = session,
        timestampMillis = timestampMillis,
        // Heartbeats are the one high-volume event that must NOT collapse, so
        // the sequence number is what keeps successive beats distinct in the
        // local unique index. (The server does not collapse them either — they
        // carry no dedupe_key server-side.)
        dedupeKey = "hb-$sequence",
        payload = buildJsonObject {
            put("watched_ms_increment", watchedMsIncrement)
            put("watched_ms_total", watchedMsTotal)
            put("playhead_position_ms", playheadPositionMs)
            put("buffering_ms_increment", bufferingMsIncrement)
            put("seek_count_increment", seekCountIncrement)
            put("playback_speed", playbackSpeed)
        },
    )

    fun milestone(
        session: WatchSession,
        milestone: String,
        watchedMs: Long,
        timestampMillis: Long,
    ): AnalyticsEvent? = event(
        type = AnalyticsEventType.MILESTONE,
        session = session,
        timestampMillis = timestampMillis,
        // Matches the server: one crossing of a given threshold per session.
        // Re-crossing PCT_50 on a loop is a loop, not a second milestone.
        dedupeKey = milestone,
        payload = buildJsonObject {
            put("milestone_type", milestone)
            put("watched_ms", watchedMs)
        },
    )

    fun playEnd(
        session: WatchSession,
        endReason: PlayEndReason,
        watchedMsTotal: Long,
        maxContinuousWatchMs: Long,
        loopCount: Int,
        timestampMillis: Long,
    ): AnalyticsEvent? = event(
        type = AnalyticsEventType.PLAY_END,
        session = session,
        timestampMillis = timestampMillis,
        dedupeKey = "end",
        payload = buildJsonObject {
            put("watched_ms_total", watchedMsTotal)
            put("max_continuous_watch_ms", maxContinuousWatchMs)
            put("content_duration_ms", session.contentDurationMs)
            put("content_type", session.contentType.wire)
            put("loop_count", loopCount)
            put("end_reason", endReason.wire)
        },
    )

    /**
     * like / comment_create / share / save / follow_from_content.
     *
     * These carry nothing beyond the common fields — the server counts them and
     * never quotes them back. They do not require a session, so they can be
     * emitted from a card the viewer never played; when a playback session IS
     * in scope it is passed, because that is what lets the server attribute an
     * engagement to the view that produced it.
     */
    fun engagement(
        type: String,
        session: WatchSession,
        timestampMillis: Long,
    ): AnalyticsEvent? = event(
        type = type,
        session = session,
        timestampMillis = timestampMillis,
        dedupeKey = "session",
        payload = JsonObject(emptyMap()),
    )

    /** not_interested / report / block_creator. */
    fun negativeSignal(
        type: String,
        session: WatchSession,
        reason: NegativeSignalReason,
        timestampMillis: Long,
    ): AnalyticsEvent? = event(
        type = type,
        session = session,
        timestampMillis = timestampMillis,
        dedupeKey = "session",
        payload = buildJsonObject { put("reason", JsonPrimitive(reason.wire)) },
    )
}
