package com.us.android.core.media.ui

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.test.currentTime
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * The two halves of the buffering indicator, checked without a device:
 * the rule that decides whether a surface is waiting, and the debounce that
 * decides whether the viewer is told about it.
 *
 * These are the claims the founder's complaint turns on. "It shows stuck"
 * is [waiting is not drawn immediately] failing in the other direction —
 * nothing was ever drawn — and the reason nothing was drawn naively is that
 * a spinner on every micro-stall is worse. Both are asserted here.
 */
class VideoLoadingTest {

    // ── The rule ─────────────────────────────────────────────────────────

    @Test
    fun `buffering while the viewer wants playback is waiting`() {
        val signal = VideoSignal(
            phase = PlayerPhase.BUFFERING,
            playWhenReady = true,
            renderedFirstFrame = true,
        )

        assertThat(signal.isWaitingForFrames()).isTrue()
    }

    @Test
    fun `a viewer-initiated pause is never waiting, even mid-buffer`() {
        val signal = VideoSignal(
            phase = PlayerPhase.BUFFERING,
            playWhenReady = false,
            renderedFirstFrame = true,
        )

        assertThat(signal.isWaitingForFrames()).isFalse()
    }

    @Test
    fun `a prepared but unplayed neighbour is not waiting`() {
        // What PlayerPool.preload leaves behind: ready, playWhenReady false.
        val signal = VideoSignal(
            phase = PlayerPhase.READY,
            playWhenReady = false,
            renderedFirstFrame = false,
        )

        assertThat(signal.isWaitingForFrames()).isFalse()
    }

    @Test
    fun `ready with no first frame yet is waiting`() {
        // The cold open: the player has enough media, the surface has not
        // drawn. This is the gap STATE_BUFFERING alone does not cover.
        val signal = VideoSignal(
            phase = PlayerPhase.READY,
            playWhenReady = true,
            renderedFirstFrame = false,
        )

        assertThat(signal.isWaitingForFrames()).isTrue()
    }

    @Test
    fun `ready and drawing is not waiting`() {
        val signal = VideoSignal(
            phase = PlayerPhase.READY,
            playWhenReady = true,
            renderedFirstFrame = true,
        )

        assertThat(signal.isWaitingForFrames()).isFalse()
    }

    @Test
    fun `an idle player is not waiting`() {
        // Stopped or failed: the surface shows its own error or "still
        // processing", and a spinner would contradict it.
        val signal = VideoSignal(
            phase = PlayerPhase.IDLE,
            playWhenReady = true,
            renderedFirstFrame = false,
        )

        assertThat(signal.isWaitingForFrames()).isFalse()
    }

    @Test
    fun `an ended video is not waiting`() {
        val signal = VideoSignal(
            phase = PlayerPhase.ENDED,
            playWhenReady = true,
            renderedFirstFrame = true,
        )

        assertThat(signal.isWaitingForFrames()).isFalse()
    }

    @Test
    fun `every media3 playback state maps to a phase`() {
        assertThat(playerPhaseOf(STATE_IDLE)).isEqualTo(PlayerPhase.IDLE)
        assertThat(playerPhaseOf(STATE_BUFFERING)).isEqualTo(PlayerPhase.BUFFERING)
        assertThat(playerPhaseOf(STATE_READY)).isEqualTo(PlayerPhase.READY)
        assertThat(playerPhaseOf(STATE_ENDED)).isEqualTo(PlayerPhase.ENDED)
    }

    // ── The debounce ─────────────────────────────────────────────────────

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `a brief stall is never shown`() = runTest {
        val states = flow {
            emit(true)
            delay(SHORT_STALL)
            emit(false)
        }.videoLoadStates(SHOW_AFTER, STALL_AFTER).toList()

        assertThat(states).containsExactly(VideoLoadState.NONE)
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `a sustained stall is shown`() = runTest {
        val states = flow {
            emit(true)
            delay(LONG_STALL)
            emit(false)
        }.videoLoadStates(SHOW_AFTER, STALL_AFTER).toList()

        assertThat(states).containsExactly(VideoLoadState.NONE, VideoLoadState.LOADING, VideoLoadState.NONE).inOrder()
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `resuming hides the indicator immediately`() = runTest {
        var hiddenAt = -1L
        flow {
            emit(true)
            delay(LONG_STALL)
            emit(false)
            delay(SHOW_AFTER * 2)
        }.videoLoadStates(SHOW_AFTER, STALL_AFTER).collect { state ->
            if (state == VideoLoadState.NONE) hiddenAt = currentTime
        }

        // The hide lands on the very tick playback resumed — no trailing
        // delay of its own.
        assertThat(hiddenAt).isEqualTo(LONG_STALL)
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `waiting that never ends becomes a stall`() = runTest {
        val states = flow {
            emit(true)
            delay(STALL_AFTER * 2)
        }.videoLoadStates(SHOW_AFTER, STALL_AFTER).toList()

        assertThat(states)
            .containsExactly(VideoLoadState.NONE, VideoLoadState.LOADING, VideoLoadState.STALLED)
            .inOrder()
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `the stall clock does not restart on repeated waiting reports`() = runTest {
        var stalledAt = -1L
        flow {
            // The listener publishes on every player event; the flag itself
            // does not change. distinctUntilChanged must swallow these, or a
            // stuttering connection would never reach STALLED.
            repeat(REPEATS) {
                emit(true)
                delay(STALL_AFTER / REPEATS)
            }
            delay(STALL_AFTER)
        }.videoLoadStates(SHOW_AFTER, STALL_AFTER).collect { state ->
            if (state == VideoLoadState.STALLED) stalledAt = currentTime
        }

        assertThat(stalledAt).isEqualTo(STALL_AFTER)
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `a viewer-paused surface is never shown as loading`() = runTest {
        val states = flowOf(false).videoLoadStates(SHOW_AFTER, STALL_AFTER).toList()

        assertThat(states).containsExactly(VideoLoadState.NONE)
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `pausing mid-wait takes the spinner away`() = runTest {
        val states = flow {
            emit(true)
            delay(LONG_STALL)
            // The viewer taps to pause while it is still buffering.
            emit(false)
            delay(STALL_AFTER * 2)
        }.videoLoadStates(SHOW_AFTER, STALL_AFTER).toList()

        // Never reaches STALLED: the pause ended the wait.
        assertThat(states).containsExactly(VideoLoadState.NONE, VideoLoadState.LOADING, VideoLoadState.NONE).inOrder()
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun `the shipped delay is short enough to catch a real stall and long enough to miss a micro one`() = runTest {
        // A guard on the constants themselves, not on the operator: the
        // whole design rests on the shipped delay sitting between these.
        val micro = flow {
            emit(true)
            delay(VIDEO_LOADING_DELAY_MILLIS / 2)
            emit(false)
        }.videoLoadStates(VIDEO_LOADING_DELAY_MILLIS, VIDEO_STALL_MILLIS).toList()
        val real = flow {
            emit(true)
            delay(VIDEO_LOADING_DELAY_MILLIS * 4)
            emit(false)
        }.videoLoadStates(VIDEO_LOADING_DELAY_MILLIS, VIDEO_STALL_MILLIS).toList()

        assertThat(micro).containsExactly(VideoLoadState.NONE)
        assertThat(real).contains(VideoLoadState.LOADING)
    }

    private companion object {
        const val SHOW_AFTER = 250L
        const val STALL_AFTER = 12_000L
        const val SHORT_STALL = 100L
        const val LONG_STALL = 1_000L
        const val REPEATS = 4

        // Media3's own constants, restated so the mapping is asserted
        // against the documented values rather than against itself.
        const val STATE_IDLE = 1
        const val STATE_BUFFERING = 2
        const val STATE_READY = 3
        const val STATE_ENDED = 4
    }
}
