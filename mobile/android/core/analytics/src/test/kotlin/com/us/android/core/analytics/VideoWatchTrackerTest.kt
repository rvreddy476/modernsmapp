package com.us.android.core.analytics

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
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

    // ── loops, late ticks, background (audit M-12, M-27, M-29) ─────────

    @Test
    fun `a loop wrap is credited in full and counted once, never as a seek`() = runTest {
        // A 10 s reel: 8.6 s in, the next sample lands at 1.4 s. The tail
        // (1.4 s) plus the head (1.4 s) is 2.8 s of frames that were shown —
        // more than the two-tick ceiling, which used to count the loop AND
        // then discard its watch time as a seek.
        val fixture = fixture((1..8).map { playing(it * 1_000L) } + listOf(playing(8_600), playing(1_400), playing(2_400)))

        val session = fixture.start(durationMs = 10_000)!!
        testScheduler.advanceTimeBy(12_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.SWIPE_NEXT)
        testScheduler.advanceTimeBy(1_000)

        assertThat(fixture.watchedTotal()).isEqualTo(8_000 + 600 + 2_800 + 1_000)
        assertThat(fixture.loopCount()).isEqualTo(1)
        assertThat(fixture.seeksReported()).isEqualTo(0)
    }

    @Test
    fun `a late tick with a matching playhead advance is watch time, not a seek`() = runTest {
        // The third read stalls the main thread for two seconds (a decoder
        // hand-off, a Compose frame) and comes back with the playhead three
        // seconds on: exactly what three seconds of playback looks like. The
        // nominal ceiling (2 x 1 s) called that a seek and threw it away.
        val frames = listOf(playing(1_000), playing(2_000), playing(5_000), playing(6_000))
        var index = 0
        val probe: suspend () -> WatchProbe = {
            if (index == 2) delay(2_000)
            frames[index.coerceAtMost(frames.lastIndex)].also { index++ }
        }
        val fixture = fixture(probe)

        val session = fixture.start(durationMs = 30_000)!!
        testScheduler.advanceTimeBy(7_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.PAUSED)
        testScheduler.advanceTimeBy(1_000)

        assertThat(fixture.watchedTotal()).isEqualTo(6_000)
        assertThat(fixture.seeksReported()).isEqualTo(0)
    }

    @Test
    fun `a real skip is still a seek when the tick was on time`() = runTest {
        // Same playhead jump, no stall: the ceiling stays two ticks.
        val fixture = fixture(listOf(playing(1_000), playing(2_000), playing(5_000), playing(6_000)))

        val session = fixture.start(durationMs = 30_000)!!
        testScheduler.advanceTimeBy(6_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.PAUSED)
        testScheduler.advanceTimeBy(1_000)

        assertThat(fixture.watchedTotal()).isEqualTo(3_000)
        assertThat(fixture.seeksReported()).isEqualTo(1)
    }

    @Test
    fun `every heartbeat carries the loop count and the duration`() = runTest {
        // A 5 s flick looped once: the beat at 5 s says zero loops, the beat
        // at 10 s says one. A session closed by inactivity server-side is
        // clamped to duration x (loop_count + 1) from these.
        val fixture = fixture((1..5).map { playing(it * 1_000L) } + (0..5).map { playing(200 + it * 1_000L) })

        val session = fixture.start(durationMs = 5_000)!!
        testScheduler.advanceTimeBy(11_000)
        fixture.tracker.endView(session.contentId, PlayEndReason.SWIPE_NEXT)
        testScheduler.advanceTimeBy(1_000)

        val beats = fixture.recorder.ofType(AnalyticsEventType.WATCH_HEARTBEAT)
        assertThat(beats).hasSize(2)
        assertThat(beats.map { it.payload["loop_count"]!!.jsonPrimitive.long }).containsExactly(0L, 1L).inOrder()
        assertThat(beats.map { it.payload["content_duration_ms"]!!.jsonPrimitive.long }).containsExactly(5_000L, 5_000L)
    }

    @Test
    fun `going to the background pauses the view and coming back continues the same one`() = runTest {
        // Five seconds watched, the app goes behind for twenty seconds, then
        // three more seconds are watched. One session, one play_start, one
        // play_end carrying both halves — and nothing for the time away.
        val fixture = fixture(
            (1..5).map { playing(it * 1_000L) } +
                listOf(playing(5_000)) + // the sample taken as the app goes behind
                listOf(playing(5_000)) + // the first sample back: baseline only
                (6..8).map { playing(it * 1_000L) },
        )

        val session = fixture.start(durationMs = 30_000)!!
        testScheduler.advanceTimeBy(5_500)
        fixture.tracker.pauseAll()
        testScheduler.advanceTimeBy(20_000)
        val beatsWhileAway = fixture.recorder.ofType(AnalyticsEventType.WATCH_HEARTBEAT).size
        assertThat(fixture.recorder.ofType(AnalyticsEventType.PLAY_END)).isEmpty()

        // The surface re-announces the reel it is showing, as reels does on
        // resume: same view, not a second one.
        val resumed = fixture.start(session.contentId, durationMs = 30_000)
        assertThat(resumed).isEqualTo(session)
        testScheduler.advanceTimeBy(4_500)
        fixture.tracker.endView(session.contentId, PlayEndReason.ENDED)
        testScheduler.advanceTimeBy(1_000)

        assertThat(fixture.recorder.ofType(AnalyticsEventType.PLAY_START)).hasSize(1)
        assertThat(fixture.recorder.ofType(AnalyticsEventType.PLAY_END)).hasSize(1)
        assertThat(fixture.watchedTotal()).isEqualTo(8_000)
        // The running total went to disk as the app went behind, and nothing
        // was reported during the twenty seconds away.
        val pauseBeat = fixture.recorder.ofType(AnalyticsEventType.WATCH_HEARTBEAT)[beatsWhileAway - 1]
        assertThat(pauseBeat.payload["watched_ms_total"]!!.jsonPrimitive.long).isEqualTo(5_000)
        assertThat(fixture.recorder.ofType(AnalyticsEventType.WATCH_HEARTBEAT).size).isAtLeast(beatsWhileAway)
    }

    @Test
    fun `resumeAll picks a paused view up without the surface asking`() = runTest {
        val fixture = fixture((1..3).map { playing(it * 1_000L) } + List(2) { playing(3_000) } + (4..5).map { playing(it * 1_000L) })

        val session = fixture.start(durationMs = 30_000)!!
        testScheduler.advanceTimeBy(3_500)
        fixture.tracker.pauseAll()
        testScheduler.advanceTimeBy(60_000)
        fixture.tracker.resumeAll()
        testScheduler.advanceTimeBy(3_500)
        fixture.tracker.endView(session.contentId, PlayEndReason.ENDED)
        testScheduler.advanceTimeBy(1_000)

        assertThat(fixture.recorder.ofType(AnalyticsEventType.PLAY_START)).hasSize(1)
        assertThat(fixture.watchedTotal()).isEqualTo(5_000)
    }

    // ── fixture ─────────────────────────────────────────────────────────

    private class Fixture(
        val tracker: VideoWatchTracker,
        val recorder: RecordingRecorder,
        val probe: suspend () -> WatchProbe,
    ) {
        fun start(durationMs: Long): WatchSession? = start(UUID.randomUUID().toString(), durationMs)

        fun start(contentId: String, durationMs: Long): WatchSession? = tracker.startView(
            contentId = contentId,
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

        fun loopCount(): Long = recorder.ofType(AnalyticsEventType.PLAY_END)
            .single().payload["loop_count"]!!.jsonPrimitive.long

        /** Seeks reach the wire only as heartbeat increments, so this is their sum. */
        fun seeksReported(): Long = recorder.ofType(AnalyticsEventType.WATCH_HEARTBEAT)
            .sumOf { it.payload["seek_count_increment"]!!.jsonPrimitive.long }
    }

    /**
     * A scripted player: one reading per tick, holding the last frame after.
     *
     * This is the point of [WatchProbe] being a lambda — every rule above is
     * exercised against a list of readings, with no ExoPlayer and no Android.
     */
    private fun TestScope.fixture(frames: List<WatchProbe>): Fixture {
        var index = 0
        return fixture { frames[index.coerceAtMost(frames.lastIndex)].also { index++ } }
    }

    /**
     * The tracker's clock is the scheduler's, so a probe that suspends is a
     * tick that really was late, and `advanceTimeBy` is time that really
     * passed — which is what the measured-tick and background rules read.
     */
    private fun TestScope.fixture(probe: suspend () -> WatchProbe): Fixture {
        val recorder = RecordingRecorder()
        val dispatcher = StandardTestDispatcher(testScheduler)
        val tracker = VideoWatchTracker(recorder, this, dispatcher) { EPOCH_MILLIS + testScheduler.currentTime }
        return Fixture(tracker, recorder, probe)
    }

    private companion object {
        /** Any fixed instant; the tests only ever read differences. */
        const val EPOCH_MILLIS = 1_760_000_000_000L

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
