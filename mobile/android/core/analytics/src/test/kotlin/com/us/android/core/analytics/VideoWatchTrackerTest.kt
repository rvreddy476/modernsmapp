package com.us.android.core.analytics

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import org.junit.Test
import java.util.UUID

/**
 * Watch accounting.
 *
 * Every number here ends up in a creator's view count and, eventually, their
 * payout — so the cases worth testing are the ones where a plausible
 * implementation is generous by accident: crediting a skipped stretch as
 * watched, crediting wall-clock while the video was stalled, or firing the same
 * milestone twice.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class VideoWatchTrackerTest {

    /** Captures what would have been queued, with no database and no network. */
    private class RecordingRecorder : AnalyticsRecorder {
        val events = mutableListOf<AnalyticsEvent>()

        override fun record(event: AnalyticsEvent?): Job? {
            if (event != null) events += event
            return null
        }

        override suspend fun recordNow(event: AnalyticsEvent?) {
            record(event)
        }

        override fun recordEngagement(type: String, session: WatchSession) = Unit
        override fun recordNegativeSignal(
            type: String,
            session: WatchSession,
            reason: NegativeSignalReason,
        ) = Unit

        override fun flush() = Unit

        fun ofType(type: String) = events.filter { it.type == type }

        fun milestones() = ofType(AnalyticsEventType.MILESTONE)
            .map { it.payload["milestone_type"]!!.jsonPrimitive.content }
    }

    // ── watch time ──────────────────────────────────────────────────────

    @Test
    fun `watch time comes from the playhead, not the wall clock`() = runTest {
        // Five ticks that advance, then five where the video is stalled.
        // Counting elapsed time would report ten seconds for five seconds seen.
        val fixture = fixture((1..5).map { playing(it * 1_000L) } + List(5) { stalled(5_000) })

        val session = fixture.start(durationMs = 30_000)!!
        testScheduler.advanceTimeBy(11_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.PAUSED)
        testScheduler.advanceTimeBy(1_000)

        assertThat(fixture.watchedTotal()).isEqualTo(5_000)
    }

    @Test
    fun `a forward skip is not credited as watch time`() = runTest {
        // 1s, 2s, then a scrub to 20s, then 21s. The eighteen-second jump is
        // navigation, not viewing.
        val fixture = fixture(listOf(playing(1_000), playing(2_000), playing(20_000), playing(21_000)))

        val session = fixture.start(durationMs = 60_000)!!
        testScheduler.advanceTimeBy(5_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.PAUSED)
        testScheduler.advanceTimeBy(1_000)

        assertThat(fixture.watchedTotal()).isAtMost(3_000)
    }

    @Test
    fun `max continuous watch never exceeds the total, which the server requires`() = runTest {
        val fixture = fixture((1..8).map { playing(it * 1_000L) })

        val session = fixture.start(durationMs = 30_000)!!
        testScheduler.advanceTimeBy(9_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.ENDED)
        testScheduler.advanceTimeBy(1_000)

        val end = fixture.recorder.ofType(AnalyticsEventType.PLAY_END).single()
        val total = end.payload["watched_ms_total"]!!.jsonPrimitive.long
        val continuous = end.payload["max_continuous_watch_ms"]!!.jsonPrimitive.long
        assertThat(continuous).isAtMost(total)
    }

    // ── milestones ──────────────────────────────────────────────────────

    @Test
    fun `a reel gets the reel ladder and never a long-video threshold`() = runTest {
        val fixture = fixture((1..15).map { playing(it * 1_000L) })

        val session = fixture.start(durationMs = 60_000)!!
        testScheduler.advanceTimeBy(16_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.ENDED)
        testScheduler.advanceTimeBy(1_000)

        assertThat(fixture.recorder.milestones()).containsAtLeast("VIEW_1S", "VIEW_3S", "VIEW_10S")
        // A 60-second reel cannot cross these; sending them would put
        // thresholds in the aggregates that no reel can ever reach.
        assertThat(fixture.recorder.milestones()).containsNoneOf("VIEW_30S", "VIEW_60S", "VIEW_120S")
    }

    @Test
    fun `a long video gets the long-video ladder and not the one-second step`() = runTest {
        val fixture = fixture((1..35).map { playing(it * 1_000L) })

        val session = fixture.start(durationMs = 600_000)!!
        testScheduler.advanceTimeBy(36_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.ENDED)
        testScheduler.advanceTimeBy(1_000)

        assertThat(fixture.recorder.milestones()).containsAtLeast("VIEW_10S", "VIEW_30S")
        assertThat(fixture.recorder.milestones()).containsNoneOf("VIEW_1S", "VIEW_3S")
    }

    /** The server collapses a repeated milestone per session; so does the client. */
    @Test
    fun `each milestone fires exactly once`() = runTest {
        val fixture = fixture((1..20).map { playing(it * 1_000L) })

        val session = fixture.start(durationMs = 30_000)!!
        testScheduler.advanceTimeBy(21_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.ENDED)
        testScheduler.advanceTimeBy(1_000)

        assertThat(fixture.recorder.milestones()).containsNoDuplicates()
    }

    @Test
    fun `percent milestones are measured against the content duration`() = runTest {
        // Eleven seconds of a twenty-second reel: past 25% and 50%, short of 75%.
        val fixture = fixture((1..11).map { playing(it * 1_000L) })

        val session = fixture.start(durationMs = 20_000)!!
        testScheduler.advanceTimeBy(12_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.ENDED)
        testScheduler.advanceTimeBy(1_000)

        assertThat(fixture.recorder.milestones()).containsAtLeast("PCT_25", "PCT_50")
        assertThat(fixture.recorder.milestones()).containsNoneOf("PCT_75", "PCT_95")
    }

    // ── cadence ─────────────────────────────────────────────────────────

    @Test
    fun `heartbeats follow the five-second cadence, not the one-second sample`() = runTest {
        val fixture = fixture((1..20).map { playing(it * 1_000L) })

        val session = fixture.start(durationMs = 60_000)!!
        testScheduler.advanceTimeBy(21_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.PAUSED)
        testScheduler.advanceTimeBy(1_000)

        val heartbeats = fixture.recorder.ofType(AnalyticsEventType.WATCH_HEARTBEAT)
        // Twenty seconds of playback is four beats, not twenty.
        assertThat(heartbeats).hasSize(4)
        assertThat(heartbeats.map { it.payload["watched_ms_total"]!!.jsonPrimitive.long }).isInOrder()
    }

    /**
     * The server refuses a heartbeat whose increment exceeds its running total,
     * and that refusal fails the whole batch it travels in.
     */
    @Test
    fun `every heartbeat increment is within its running total`() = runTest {
        val fixture = fixture((1..20).map { playing(it * 1_000L) })

        val session = fixture.start(durationMs = 60_000)!!
        testScheduler.advanceTimeBy(21_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.PAUSED)
        testScheduler.advanceTimeBy(1_000)

        fixture.recorder.ofType(AnalyticsEventType.WATCH_HEARTBEAT).forEach { beat ->
            val increment = beat.payload["watched_ms_increment"]!!.jsonPrimitive.long
            val total = beat.payload["watched_ms_total"]!!.jsonPrimitive.long
            assertThat(increment).isAtMost(total)
        }
    }

    // ── lifecycle ───────────────────────────────────────────────────────

    /**
     * `ON_STOP` pausing playback and a composable's `onDispose` can both close
     * the same view. Two `play_end`s would be two views for one watch.
     */
    @Test
    fun `ending a view twice produces one play_end`() = runTest {
        val fixture = fixture((1..5).map { playing(it * 1_000L) })

        val session = fixture.start(durationMs = 30_000)!!
        testScheduler.advanceTimeBy(3_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.SWIPE_NEXT)
        fixture.tracker.endView(session.contentId, PlayEndReason.BACKGROUNDED)
        testScheduler.advanceTimeBy(1_000)

        assertThat(fixture.recorder.ofType(AnalyticsEventType.PLAY_END)).hasSize(1)
    }

    @Test
    fun `the end reason reaches the payload`() = runTest {
        val fixture = fixture((1..5).map { playing(it * 1_000L) })

        val session = fixture.start(durationMs = 30_000)!!
        testScheduler.advanceTimeBy(3_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.SWIPE_NEXT)
        testScheduler.advanceTimeBy(1_000)

        val end = fixture.recorder.ofType(AnalyticsEventType.PLAY_END).single()
        assertThat(end.payload["end_reason"]!!.jsonPrimitive.content).isEqualTo("swipe_next")
    }

    @Test
    fun `a play_start is emitted once the first frame is drawn`() = runTest {
        val fixture = fixture(listOf(buffering(), buffering()) + (1..5).map { playing(it * 1_000L) })

        val session = fixture.start(durationMs = 30_000)!!
        testScheduler.advanceTimeBy(8_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.ENDED)
        testScheduler.advanceTimeBy(1_000)

        val start = fixture.recorder.ofType(AnalyticsEventType.PLAY_START).single()
        // Two seconds of stall before the first frame.
        assertThat(start.payload["initial_buffer_ms"]!!.jsonPrimitive.long).isAtLeast(2_000)
        assertThat(start.payload["time_to_first_frame_ms"]!!.jsonPrimitive.long).isAtLeast(0)
    }

    // ── content that cannot be reported ─────────────────────────────────

    @Test
    fun `content with no known duration is not tracked`() = runTest {
        // Zero means unknown — images, and rows from a server predating the
        // field. Sending it would divide by zero in percent_viewed.
        val fixture = fixture(listOf(playing(0)))

        assertThat(fixture.start(durationMs = 0)).isNull()
        assertThat(fixture.recorder.events).isEmpty()
    }

    @Test
    fun `a content id that is not a uuid is not tracked`() = runTest {
        val fixture = fixture(listOf(playing(0)))

        val session = fixture.tracker.startView(
            contentId = "post_12345",
            creatorId = UUID.randomUUID().toString(),
            surface = AnalyticsSurface.FEED,
            contentDurationMs = 30_000,
            startMethod = PlayStartMethod.AUTOPLAY,
            isMuted = false,
            isAutoplay = true,
            probe = fixture.probe,
        )

        assertThat(session).isNull()
        assertThat(fixture.recorder.events).isEmpty()
    }

    // ── fixture ─────────────────────────────────────────────────────────

    private class Fixture(
        val tracker: VideoWatchTracker,
        val recorder: RecordingRecorder,
        val probe: suspend () -> WatchProbe,
    ) {
        fun start(durationMs: Long): WatchSession? = tracker.startView(
            contentId = UUID.randomUUID().toString(),
            creatorId = UUID.randomUUID().toString(),
            surface = AnalyticsSurface.FEED,
            contentDurationMs = durationMs,
            startMethod = PlayStartMethod.AUTOPLAY,
            isMuted = false,
            isAutoplay = true,
            probe = probe,
        )

        fun watchedTotal(): Long = recorder.ofType(AnalyticsEventType.PLAY_END)
            .single().payload["watched_ms_total"]!!.jsonPrimitive.long
    }

    /**
     * A scripted player: one reading per tick, holding the last frame after.
     *
     * This is the point of [WatchProbe] being a lambda — every rule above is
     * exercised against a list of readings, with no ExoPlayer and no Android.
     */
    private fun TestScope.fixture(frames: List<WatchProbe>): Fixture {
        var index = 0
        val probe: suspend () -> WatchProbe = { frames[index.coerceAtMost(frames.lastIndex)].also { index++ } }
        val recorder = RecordingRecorder()
        val dispatcher = StandardTestDispatcher(testScheduler)
        return Fixture(VideoWatchTracker(recorder, this, dispatcher), recorder, probe)
    }

    private companion object {
        fun playing(playheadMs: Long) = WatchProbe(
            playheadMs = playheadMs,
            isPlaying = true,
            isBuffering = false,
            renderedFirstFrame = true,
        )

        fun stalled(playheadMs: Long) = WatchProbe(
            playheadMs = playheadMs,
            isPlaying = false,
            isBuffering = true,
            renderedFirstFrame = true,
        )

        fun buffering() = WatchProbe(
            playheadMs = 0,
            isPlaying = false,
            isBuffering = true,
            renderedFirstFrame = false,
        )
    }
}
