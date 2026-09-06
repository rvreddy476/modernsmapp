package com.us.android.core.analytics

import com.google.common.truth.Truth.assertThat
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Test
import java.util.UUID

/**
 * The wire contract, restated as tests.
 *
 * These are the rules a wrong answer to is expensive and invisible: a
 * misclassified reel is paid on the wrong view rule, and an event the server
 * refuses takes its whole batch down with it.
 */
class AnalyticsContractTest {

    // ── the 90-second boundary ──────────────────────────────────────────

    /**
     * `model.ClassifyContentType` is `durationMS <= 90000`, so ninety seconds
     * EXACTLY is still a reel. Off by one millisecond in either direction and
     * the video is judged by the other content type's display-view rule — 3s
     * and 25% for a reel, 30s and 50% for long video — which is the difference
     * between a view a creator is paid for and one they are not.
     */
    @Test
    fun `exactly ninety seconds is a reel`() {
        assertThat(AnalyticsContentType.classify(90_000)).isEqualTo(AnalyticsContentType.REEL)
    }

    @Test
    fun `one millisecond over ninety seconds is a long video`() {
        assertThat(AnalyticsContentType.classify(90_001)).isEqualTo(AnalyticsContentType.LONG_VIDEO)
    }

    @Test
    fun `well under and well over the boundary classify as expected`() {
        assertThat(AnalyticsContentType.classify(1)).isEqualTo(AnalyticsContentType.REEL)
        assertThat(AnalyticsContentType.classify(89_999)).isEqualTo(AnalyticsContentType.REEL)
        assertThat(AnalyticsContentType.classify(600_000)).isEqualTo(AnalyticsContentType.LONG_VIDEO)
    }

    // ── milestone ladders ───────────────────────────────────────────────

    /**
     * A reel is capped at 90s, so its ladder stops at VIEW_10S. Sending it
     * VIEW_30S would be accepted by the server — `validMilestone` takes the
     * union of both ladders — and would then sit in the aggregates as a
     * threshold no reel can ever cross.
     */
    @Test
    fun `reel ladder stops at ten seconds`() {
        assertThat(WatchMilestone.REEL_LADDER.map { it.wire })
            .containsExactly("VIEW_1S", "VIEW_3S", "VIEW_10S").inOrder()
    }

    @Test
    fun `long video ladder starts at ten seconds and runs to two minutes`() {
        assertThat(WatchMilestone.LONG_VIDEO_LADDER.map { it.wire })
            .containsExactly("VIEW_10S", "VIEW_30S", "VIEW_60S", "VIEW_120S").inOrder()
    }

    @Test
    fun `milestone thresholds are the durations their names claim`() {
        assertThat(WatchMilestone.VIEW_1S.thresholdMs).isEqualTo(1_000)
        assertThat(WatchMilestone.VIEW_3S.thresholdMs).isEqualTo(3_000)
        assertThat(WatchMilestone.VIEW_10S.thresholdMs).isEqualTo(10_000)
        assertThat(WatchMilestone.VIEW_30S.thresholdMs).isEqualTo(30_000)
        assertThat(WatchMilestone.VIEW_60S.thresholdMs).isEqualTo(60_000)
        assertThat(WatchMilestone.VIEW_120S.thresholdMs).isEqualTo(120_000)
    }

    @Test
    fun `percent ladder matches the server's four steps`() {
        assertThat(PercentMilestone.entries.map { it.wire })
            .containsExactly("PCT_25", "PCT_50", "PCT_75", "PCT_95").inOrder()
    }

    // ── validation: what must never reach a batch ───────────────────────

    @Test
    fun `a content id that is not a uuid is refused`() {
        // Refused HERE because the server parses it with uuid.Parse and fails
        // the ENTIRE batch it travelled in, not just this event.
        assertThat(AnalyticsEvents.playEnd(session(contentId = "post_12345"), PlayEndReason.ENDED, 0, 0, 0, NOW))
            .isNull()
    }

    @Test
    fun `a play_end whose continuous watch exceeds the total is refused`() {
        val event = AnalyticsEvents.playEnd(
            session = session(),
            endReason = PlayEndReason.ENDED,
            watchedMsTotal = 5_000,
            maxContinuousWatchMs = 6_000,
            loopCount = 0,
            timestampMillis = NOW,
        )
        assertThat(event).isNull()
    }

    @Test
    fun `a play_end claiming more than ten times the duration is refused`() {
        assertThat(
            AnalyticsEvents.playEnd(session(durationMs = 10_000), PlayEndReason.ENDED, 100_001, 0, 0, NOW),
        ).isNull()
        // Exactly ten times is the server's ceiling and is allowed — that
        // headroom is what lets a looping reel report honestly.
        assertThat(
            AnalyticsEvents.playEnd(session(durationMs = 10_000), PlayEndReason.ENDED, 100_000, 0, 20, NOW),
        ).isNotNull()
    }

    @Test
    fun `a loop count over twenty is refused`() {
        assertThat(AnalyticsEvents.playEnd(session(), PlayEndReason.ENDED, 1_000, 1_000, 21, NOW)).isNull()
    }

    @Test
    fun `a heartbeat whose increment exceeds its running total is refused`() {
        val event = AnalyticsEvents.heartbeat(
            session = session(),
            sequence = 1,
            watchedMsIncrement = 6_000,
            watchedMsTotal = 5_000,
            playheadPositionMs = 5_000,
            bufferingMsIncrement = 0,
            seekCountIncrement = 0,
            playbackSpeed = 1f,
            timestampMillis = NOW,
        )
        assertThat(event).isNull()
    }

    @Test
    fun `an unknown milestone name is refused`() {
        assertThat(AnalyticsEvents.milestone(session(), "PCT_33", 1_000, NOW)).isNull()
        assertThat(AnalyticsEvents.milestone(session(), "PCT_50", 1_000, NOW)).isNotNull()
    }

    // ── freshness, checked at upload rather than enqueue ────────────────

    @Test
    fun `an event older than a day is stale and one just inside the window is not`() {
        val day = 24L * 60 * 60 * 1000
        assertThat(AnalyticsValidation.isFresh(NOW - day - 1, NOW)).isFalse()
        assertThat(AnalyticsValidation.isFresh(NOW - day + 1_000, NOW)).isTrue()
    }

    @Test
    fun `an event far in the future is refused for clock skew`() {
        assertThat(AnalyticsValidation.isFresh(NOW + 6 * 60 * 1000, NOW)).isFalse()
        assertThat(AnalyticsValidation.isFresh(NOW + 60 * 1000, NOW)).isTrue()
    }

    // ── session handling for engagement ─────────────────────────────────

    /**
     * An engagement event outside a playback session must OMIT `session_id`
     * rather than send it blank: the server parses any non-empty value as a
     * uuid and rejects the whole batch when it is not one.
     */
    @Test
    fun `engagement without a playback session omits session_id`() {
        val event = AnalyticsEvents.engagement(
            AnalyticsEventType.LIKE,
            WatchSession.forEngagement(CONTENT_ID, AnalyticsSurface.FEED),
            NOW,
        )
        assertThat(event).isNotNull()
        assertThat(event!!.payload).doesNotContainKey("session_id")
        assertThat(event.sessionId).isEmpty()
    }

    @Test
    fun `a playback event carries its session id`() {
        val session = session()
        val event = AnalyticsEvents.playEnd(session, PlayEndReason.ENDED, 1_000, 1_000, 0, NOW)
        assertThat(event!!.sessionId).isEqualTo(session.sessionId)
        assertThat(event.payload["session_id"]!!.jsonPrimitive.content).isEqualTo(session.sessionId)
    }

    @Test
    fun `a surface always reaches the wire as one of the five the server keeps`() {
        val accepted = setOf("feed", "posttube", "profile", "search", "channel")
        assertThat(AnalyticsSurface.entries.map { it.wire }).containsExactlyElementsIn(accepted)
    }

    private companion object {
        const val NOW = 1_757_000_000_000L
        val CONTENT_ID: String = UUID.randomUUID().toString()

        fun session(
            contentId: String = CONTENT_ID,
            durationMs: Long = 30_000,
        ) = WatchSession.start(
            contentId = contentId,
            creatorId = UUID.randomUUID().toString(),
            surface = AnalyticsSurface.FEED,
            contentDurationMs = durationMs,
        )
    }
}
