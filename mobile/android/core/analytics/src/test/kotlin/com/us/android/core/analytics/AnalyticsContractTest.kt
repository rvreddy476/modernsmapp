package com.us.android.core.analytics

import com.google.common.truth.Truth.assertThat
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import org.junit.Test
import java.util.UUID

/**
 * The wire contract, restated as tests.
 *
 * These are the rules a wrong answer to is expensive and invisible: a session
 * put on the wrong milestone ladder draws a retention curve from rungs it can
 * never reach, and an event the server refuses takes its whole batch down
 * with it.
 */
class AnalyticsContractTest {

    // ── the 90-second view bar ──────────────────────────────────────────

    /**
     * `model.ShortFormViewRuleMaxDurationMS` is 90 000 and the comparison is
     * `<=`, so ninety seconds EXACTLY is still under the short-form bar.
     *
     * This is the VIEW-COUNTING bar, not the definition of a flick — a flick
     * may run to five minutes (`postclassify.FlickMaxDurationSeconds`) and one
     * that does is scored by the long-form rule. Nothing on this side decides
     * content type: `ingest.go` takes it from the ownership projection.
     */
    @Test
    fun `exactly ninety seconds is under the short form bar`() {
        assertThat(AnalyticsContentType.classify(90_000)).isEqualTo(AnalyticsContentType.FLICK)
    }

    @Test
    fun `one millisecond over ninety seconds is a long video`() {
        assertThat(AnalyticsContentType.classify(90_001)).isEqualTo(AnalyticsContentType.LONG_VIDEO)
    }

    @Test
    fun `well under and well over the boundary classify as expected`() {
        assertThat(AnalyticsContentType.classify(1)).isEqualTo(AnalyticsContentType.FLICK)
        assertThat(AnalyticsContentType.classify(89_999)).isEqualTo(AnalyticsContentType.FLICK)
        assertThat(AnalyticsContentType.classify(600_000)).isEqualTo(AnalyticsContentType.LONG_VIDEO)
    }

    /**
     * The wire word is the server's, and the server's word is `flick`.
     * `postclassify.IsShortForm` still accepts `reel` as a legacy synonym, so
     * sending the old string would not have failed — it would quietly have
     * been the only place in the platform still saying it.
     */
    @Test
    fun `short form goes on the wire as flick`() {
        assertThat(AnalyticsContentType.FLICK.wire).isEqualTo("flick")
        assertThat(AnalyticsContentType.LONG_VIDEO.wire).isEqualTo("long_video")
    }

    /**
     * The view bar and the flick cap are different numbers with different
     * jobs, and were both called `REEL_MAX_DURATION_MS` until 2026-09-07.
     */
    @Test
    fun `the view bar is not the flick cap`() {
        assertThat(AnalyticsContentType.SHORT_FORM_VIEW_BAR_MS).isEqualTo(90_000L)
    }

    // ── milestone ladders ───────────────────────────────────────────────

    /**
     * A session under the short-form view bar has under ninety seconds to
     * give, so its ladder stops at VIEW_10S. Sending it VIEW_30S would be
     * accepted by the server — `validMilestone` takes the union of both
     * ladders — and would then sit in the aggregates as a threshold that
     * session could never have crossed.
     */
    @Test
    fun `short form ladder stops at ten seconds`() {
        assertThat(WatchMilestone.SHORT_FORM_LADDER.map { it.wire })
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
    fun `a play_end far past ten times the duration is kept, only the twelve-hour ceiling refuses`() {
        // A 5 s flick left looping for 130 s (twenty-five passes). The server
        // clamps the total to duration x (loop_count + 1) and keeps the
        // reported figure for audit; it no longer refuses it, and neither does
        // this client. The old "ten playthroughs" drop threw away the
        // most-watched reels' most engaged sessions (audit M-09).
        assertThat(
            AnalyticsEvents.playEnd(session(durationMs = 5_000), PlayEndReason.SWIPE_NEXT, 130_000, 130_000, 20, NOW),
        ).isNotNull()
        // Twelve hours is still the ceiling; a millisecond over is malformed.
        assertThat(
            AnalyticsEvents.playEnd(session(durationMs = 5_000), PlayEndReason.ENDED, TWELVE_HOURS_MS + 1, 0, 20, NOW),
        ).isNull()
    }

    @Test
    fun `a loop count over twenty is refused`() {
        assertThat(AnalyticsEvents.playEnd(session(), PlayEndReason.ENDED, 1_000, 1_000, 21, NOW)).isNull()
    }

    @Test
    fun `a heartbeat whose increment exceeds its running total is refused`() {
        val event = heartbeat(watchedMsIncrement = 6_000, watchedMsTotal = 5_000)
        assertThat(event).isNull()
    }

    @Test
    fun `a heartbeat carries its loop count and duration, and refuses a bad one`() {
        // Every beat says how many loops came before it and how long the
        // content is (M-29): a session the server closes by inactivity is
        // clamped to duration x (loop_count + 1) from these alone.
        val event = heartbeat(loopCount = 3, contentDurationMs = 5_000)!!
        assertThat(event.payload["loop_count"]!!.jsonPrimitive.long).isEqualTo(3)
        assertThat(event.payload["content_duration_ms"]!!.jsonPrimitive.long).isEqualTo(5_000)
        assertThat(heartbeat(loopCount = 21)).isNull()
        assertThat(heartbeat(contentDurationMs = 0)).isNull()
    }

    private fun heartbeat(
        watchedMsIncrement: Long = 1_000,
        watchedMsTotal: Long = 5_000,
        loopCount: Int = 0,
        contentDurationMs: Long = 30_000,
    ) = AnalyticsEvents.heartbeat(
        session = session(),
        sequence = 1,
        watchedMsIncrement = watchedMsIncrement,
        watchedMsTotal = watchedMsTotal,
        playheadPositionMs = 5_000,
        bufferingMsIncrement = 0,
        seekCountIncrement = 0,
        playbackSpeed = 1f,
        loopCount = loopCount,
        contentDurationMs = contentDurationMs,
        timestampMillis = NOW,
    )

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
    fun `a surface always reaches the wire as one of the six the server keeps`() {
        val accepted = setOf("feed", "reels", "posttube", "profile", "search", "channel")
        assertThat(AnalyticsSurface.entries.map { it.wire }).containsExactlyElementsIn(accepted)
    }

    private companion object {
        const val NOW = 1_757_000_000_000L
        const val TWELVE_HOURS_MS = 12L * 60 * 60 * 1000
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
