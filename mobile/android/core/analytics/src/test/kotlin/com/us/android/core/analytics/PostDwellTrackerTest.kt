package com.us.android.core.analytics

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import org.junit.Test
import java.util.UUID

/**
 * Dwell on the posts that never play.
 *
 * These numbers become `MediaPrefs.ImageP95Dwell` and `TextP95Dwell` — a
 * PERCENTILE — so the failures worth pinning are the ones that would poison a
 * distribution rather than lose a single row: crediting time the app was
 * behind, emitting a glance as though it were a read, or counting the same
 * card twice on a nudge.
 */
class PostDwellTrackerTest {

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

        fun visibleMs() = events.map { it.payload["visible_ms"]!!.jsonPrimitive.long }
    }

    private val recorder = RecordingRecorder()

    // The scope is never used by anything these tests exercise —
    // onDwellChanged and endAll both write straight through the recorder.
    private val tracker = PostDwellTracker(recorder, CoroutineScope(Job()))

    private fun target(id: String = uuid(), isAutoplay: Boolean = false, position: Int? = null) =
        DwellTarget(
            contentId = id,
            creatorId = uuid(),
            surface = AnalyticsSurface.FEED,
            isAutoplay = isAutoplay,
            position = position,
        )

    private fun uuid() = UUID.randomUUID().toString()

    // ── the floor ───────────────────────────────────────────────────────

    /**
     * The case the whole floor exists for: a fling past twenty cards must not
     * put twenty rows in the queue, and must not put twenty 200ms readings
     * into a percentile that is supposed to describe how long things hold
     * people.
     */
    @Test
    fun `a fling past twenty cards reports nothing`() {
        var now = 0L
        repeat(20) {
            tracker.onDwellChanged(target(), running = true, nowMillis = now)
            now += 200
        }
        tracker.onDwellChanged(null, running = false, nowMillis = now)

        assertThat(recorder.events).isEmpty()
    }

    @Test
    fun `a dwell one millisecond under the floor is not reported`() {
        val post = target()
        tracker.onDwellChanged(post, running = true, nowMillis = 0)
        tracker.onDwellChanged(null, running = false, nowMillis = PostDwellTracker.MIN_REPORTABLE_MS - 1)

        assertThat(recorder.events).isEmpty()
    }

    @Test
    fun `a dwell exactly at the floor is reported`() {
        val post = target()
        tracker.onDwellChanged(post, running = true, nowMillis = 0)
        tracker.onDwellChanged(null, running = false, nowMillis = PostDwellTracker.MIN_REPORTABLE_MS)

        assertThat(recorder.visibleMs()).containsExactly(PostDwellTracker.MIN_REPORTABLE_MS)
    }

    /**
     * The server rejects the WHOLE batch on a `visible_ms` over ten minutes, so
     * a card left on screen while its reader made a cup of tea is clamped
     * rather than allowed to take a hundred real events down with it.
     */
    @Test
    fun `an absurd dwell is clamped to the server's ceiling, not dropped`() {
        val post = target()
        tracker.onDwellChanged(post, running = true, nowMillis = 0)
        tracker.onDwellChanged(null, running = false, nowMillis = 60L * 60 * 1000)

        assertThat(recorder.visibleMs()).containsExactly(PostDwellTracker.MAX_REPORTABLE_MS)
        assertThat(AnalyticsValidation.isValid(recorder.events.single())).isTrue()
    }

    // ── what counts as time ─────────────────────────────────────────────

    /**
     * Dwell is time the post was in front of someone, not time the app was
     * open. The caller stops the clock for a comments sheet, the media viewer
     * and the screen not being resumed; ten minutes behind one of those must
     * not read as ten minutes of reading.
     */
    @Test
    fun `time while the feed is not what the reader is looking at does not count`() {
        val post = target()
        tracker.onDwellChanged(post, running = true, nowMillis = 0)
        tracker.onDwellChanged(post, running = false, nowMillis = 2_000)
        // Ten minutes behind the comments sheet.
        tracker.onDwellChanged(post, running = true, nowMillis = 602_000)
        tracker.onDwellChanged(null, running = false, nowMillis = 605_000)

        assertThat(recorder.visibleMs()).containsExactly(5_000L)
    }

    /** A device whose clock moved backwards mid-scroll credits nothing, never a negative. */
    @Test
    fun `a clock that goes backwards credits nothing`() {
        val post = target()
        tracker.onDwellChanged(post, running = true, nowMillis = 10_000)
        tracker.onDwellChanged(null, running = false, nowMillis = 1_000)

        assertThat(recorder.events).isEmpty()
    }

    // ── one card at a time ──────────────────────────────────────────────

    @Test
    fun `moving to the next card closes the previous one`() {
        val first = target()
        val second = target()
        tracker.onDwellChanged(first, running = true, nowMillis = 0)
        tracker.onDwellChanged(second, running = true, nowMillis = 3_000)
        tracker.onDwellChanged(null, running = false, nowMillis = 5_000)

        assertThat(recorder.visibleMs()).containsExactly(3_000L, 2_000L).inOrder()
        assertThat(recorder.events.map { it.contentId })
            .containsExactly(first.contentId, second.contentId).inOrder()
    }

    /**
     * The list re-reports the same card on every nudge. Re-stating the target
     * must continue the measurement, not start a second one — otherwise a
     * steady read of one post arrives as a dozen sub-second glances, all of
     * them under the floor, and the post reads as having held nobody.
     */
    @Test
    fun `re-stating the same card continues one measurement`() {
        val post = target()
        repeat(10) { tick -> tracker.onDwellChanged(post, running = true, nowMillis = tick * 400L) }
        tracker.onDwellChanged(null, running = false, nowMillis = 4_000)

        assertThat(recorder.visibleMs()).containsExactly(4_000L)
    }

    // ── the wire shape ──────────────────────────────────────────────────

    /**
     * It has to be an `impression`. The server's type list is closed and
     * `IngestEvents` fails the whole batch on the first unknown type, so a
     * `dwell` event of our own would take every real view travelling with it
     * down too.
     */
    @Test
    fun `a dwell goes out as an impression the server would accept`() = runTest {
        val post = target(isAutoplay = true, position = 4)
        tracker.onDwellChanged(post, running = true, nowMillis = 0)
        tracker.endAll(nowMillis = 2_500)

        val event = recorder.events.single()
        assertThat(event.type).isEqualTo(AnalyticsEventType.IMPRESSION)
        assertThat(AnalyticsValidation.isValid(event)).isTrue()
        assertThat(event.payload["visible_ms"]!!.jsonPrimitive.long).isEqualTo(2_500)
        assertThat(event.payload["is_autoplay"]!!.jsonPrimitive.boolean).isTrue()
        assertThat(event.payload["position"]!!.jsonPrimitive.int).isEqualTo(4)
        // No playback session: an image never plays, and a non-uuid session_id
        // would fail the batch. `requiresSession` does not cover impressions.
        assertThat(event.sessionId).isEmpty()
        assertThat(event.payload["session_id"]).isNull()
    }

    /**
     * The background transition is where the process is most likely to be
     * reclaimed, and the card the reader was still on is the one that held
     * them longest — exactly the observation the percentile is for.
     */
    @Test
    fun `going to the background reports the card still open`() = runTest {
        tracker.onDwellChanged(target(), running = true, nowMillis = 0)
        tracker.endAll(nowMillis = 7_000)

        assertThat(recorder.visibleMs()).containsExactly(7_000L)
    }

    /** A second close reports nothing: the first took the measurement away. */
    @Test
    fun `closing twice reports once`() = runTest {
        tracker.onDwellChanged(target(), running = true, nowMillis = 0)
        tracker.endAll(nowMillis = 7_000)
        tracker.endAll(nowMillis = 9_000)

        assertThat(recorder.events).hasSize(1)
    }

    /** A post id that is not a uuid would fail the batch it travelled in. */
    @Test
    fun `a content id the server could not parse is never enqueued`() {
        val post = DwellTarget(
            contentId = "not-a-uuid",
            creatorId = uuid(),
            surface = AnalyticsSurface.FEED,
            isAutoplay = false,
        )
        tracker.onDwellChanged(post, running = true, nowMillis = 0)
        tracker.onDwellChanged(null, running = false, nowMillis = 5_000)

        assertThat(recorder.events).isEmpty()
    }
}
