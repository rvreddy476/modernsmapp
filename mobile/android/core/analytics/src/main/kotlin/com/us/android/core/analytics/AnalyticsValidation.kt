package com.us.android.core.analytics

import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.floatOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull

/**
 * The server's acceptance rules, restated on the client.
 *
 * ## WHY THIS IS WORTH DUPLICATING
 *
 * `IngestService.IngestEvents` validates in a loop and `return`s on the first
 * failure. There is no partial success: one malformed event rejects the entire
 * request, so a single bad row can take a hundred good views down with it — and
 * because the row stays queued, it does it again on every retry until the queue
 * is wedged for good.
 *
 * Two defences follow from that, and this is the first: nothing is enqueued
 * unless it would be accepted. The second is bisection in [AnalyticsUploader],
 * for the failures this cannot foresee (a content id the server has not
 * projected yet, a clock that moved).
 *
 * Every bound below is copied from `internal/service/ingest.go`; the
 * corresponding server check is named in a comment wherever it is not obvious.
 */
object AnalyticsValidation {

    private const val MAX_EVENT_AGE_MS = 24L * 60 * 60 * 1000
    private const val MAX_CLOCK_SKEW_MS = 5L * 60 * 1000
    private const val MAX_VIDEO_DURATION_MS = 12L * 60 * 60 * 1000
    private const val MAX_INCREMENT_MS = 10L * 60 * 1000
    private const val MIN_EVENT_ID_LENGTH = 16
    private const val MAX_EVENT_ID_LENGTH = 128
    private const val MAX_SEEK_COUNT = 1000
    private const val MAX_LOOP_COUNT = 20
    private const val MIN_PLAYBACK_SPEED = 0.25f
    private const val MAX_PLAYBACK_SPEED = 4.0f
    private const val WATCHED_TOTAL_DURATION_MULTIPLE = 10

    private val UUID_REGEX =
        Regex("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")

    private const val NIL_UUID = "00000000-0000-0000-0000-000000000000"

    /**
     * True when the server would accept this event.
     *
     * Deliberately total and side-effect free: it is called on the enqueue path,
     * which runs on whatever thread a player surface happens to be on.
     */
    fun isValid(event: AnalyticsEvent): Boolean {
        if (event.eventId.length !in MIN_EVENT_ID_LENGTH..MAX_EVENT_ID_LENGTH) return false
        if (event.type !in ALL_TYPES) return false

        // `uuid.Parse(payload.ContentID)` — a post id that is not a uuid takes
        // the whole batch down, and ids from a non-uuid source do exist on this
        // platform, so this check is not theoretical.
        if (!isUuid(event.contentId) || event.contentId == NIL_UUID) return false

        if (event.type in AnalyticsEventType.REQUIRES_SESSION) {
            if (!isUuid(event.sessionId) || event.sessionId == NIL_UUID) return false
        } else if (event.sessionId.isNotEmpty() && !isUuid(event.sessionId)) {
            return false
        }

        return isPayloadValid(event.type, event.payload)
    }

    /**
     * Whether the event is still inside the server's acceptance window.
     *
     * Checked at UPLOAD time, not enqueue time: an event queued on a plane is
     * perfectly valid when it is written and stale by the time the device
     * reconnects. `when.Before(now-24h) || when.After(now+5m)` rejects the whole
     * batch, so a stale row is dropped rather than sent.
     */
    fun isFresh(timestampMillis: Long, nowMillis: Long): Boolean =
        timestampMillis > nowMillis - MAX_EVENT_AGE_MS &&
            timestampMillis < nowMillis + MAX_CLOCK_SKEW_MS

    private fun isUuid(value: String) = UUID_REGEX.matches(value)

    private val ALL_TYPES = setOf(
        AnalyticsEventType.IMPRESSION,
        AnalyticsEventType.PLAY_START,
        AnalyticsEventType.WATCH_HEARTBEAT,
        AnalyticsEventType.MILESTONE,
        AnalyticsEventType.PLAY_END,
        AnalyticsEventType.LIKE,
        AnalyticsEventType.COMMENT_CREATE,
        AnalyticsEventType.SHARE,
        AnalyticsEventType.SAVE,
        AnalyticsEventType.FOLLOW_FROM_CONTENT,
        AnalyticsEventType.NOT_INTERESTED,
        AnalyticsEventType.REPORT,
        AnalyticsEventType.BLOCK_CREATOR,
    )

    private val VALID_MILESTONES: Set<String> =
        WatchMilestone.entries.map { it.wire }.toSet() +
            PercentMilestone.entries.map { it.wire }.toSet()

    private fun JsonObject.long(key: String): Long? = this[key]?.jsonPrimitive?.longOrNull

    private fun JsonObject.float(key: String): Float? = this[key]?.jsonPrimitive?.floatOrNull

    private fun JsonObject.bool(key: String): Boolean? = this[key]?.jsonPrimitive?.booleanOrNull

    // A validator is a list of guards; nesting them to satisfy a metric would
    // read worse than the contract it is restating.
    @Suppress("ReturnCount", "CyclomaticComplexMethod")
    private fun isPayloadValid(type: String, payload: JsonObject): Boolean = when (type) {
        AnalyticsEventType.IMPRESSION -> {
            val visible = payload.long("visible_ms")
            visible != null && visible in 0..MAX_INCREMENT_MS && payload.bool("is_autoplay") != null
        }

        AnalyticsEventType.PLAY_START -> {
            val duration = payload.long("content_duration_ms")
            val ttff = payload.long("time_to_first_frame_ms")
            val initialBuffer = payload.long("initial_buffer_ms")
            isValidDuration(duration) &&
                ttff != null && ttff in 0..MAX_INCREMENT_MS &&
                initialBuffer != null && initialBuffer in 0..MAX_INCREMENT_MS
        }

        AnalyticsEventType.WATCH_HEARTBEAT -> isHeartbeatValid(payload)

        AnalyticsEventType.MILESTONE -> {
            val watched = payload.long("watched_ms")
            val milestone = payload["milestone_type"]?.jsonPrimitive?.content
            milestone in VALID_MILESTONES && watched != null && watched in 0..MAX_VIDEO_DURATION_MS
        }

        AnalyticsEventType.PLAY_END -> isPlayEndValid(payload)

        // Engagement and negative signals carry nothing the server bounds
        // beyond `reason`, which is normalised rather than rejected.
        else -> true
    }

    @Suppress("ReturnCount")
    private fun isHeartbeatValid(payload: JsonObject): Boolean {
        val increment = payload.long("watched_ms_increment") ?: return false
        val total = payload.long("watched_ms_total") ?: return false
        val playhead = payload.long("playhead_position_ms") ?: return false
        val buffering = payload.long("buffering_ms_increment") ?: return false
        val seeks = payload.long("seek_count_increment") ?: return false
        val speed = payload.float("playback_speed") ?: return false
        if (increment !in 0..MAX_INCREMENT_MS) return false
        if (total !in 0..MAX_VIDEO_DURATION_MS) return false
        // `heartbeat increment exceeds running total` — the increment is part
        // of the total, so a client that reset one and not the other is caught.
        if (increment > total) return false
        if (playhead !in 0..MAX_VIDEO_DURATION_MS) return false
        if (buffering !in 0..MAX_INCREMENT_MS) return false
        if (seeks !in 0..MAX_SEEK_COUNT.toLong()) return false
        return speed in MIN_PLAYBACK_SPEED..MAX_PLAYBACK_SPEED
    }

    @Suppress("ReturnCount")
    private fun isPlayEndValid(payload: JsonObject): Boolean {
        val duration = payload.long("content_duration_ms")
        if (!isValidDuration(duration)) return false
        val watched = payload.long("watched_ms_total") ?: return false
        val loops = payload.long("loop_count") ?: return false
        val maxContinuous = payload.long("max_continuous_watch_ms") ?: return false
        // Ten times the content length is the server's ceiling — it allows for
        // looping, which is why loop_count exists, and rejects a claim of an
        // hour spent on a thirty-second reel.
        if (watched < 0 || watched > duration!! * WATCHED_TOTAL_DURATION_MULTIPLE) return false
        if (loops !in 0..MAX_LOOP_COUNT.toLong()) return false
        // A single unbroken stretch cannot exceed the total watched.
        return maxContinuous in 0..watched
    }

    private fun isValidDuration(durationMs: Long?): Boolean =
        durationMs != null && durationMs > 0 && durationMs <= MAX_VIDEO_DURATION_MS
}
