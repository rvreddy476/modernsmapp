package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.dating.selfie.SelfieClipOutcome
import com.us.android.feature.dating.selfie.SelfieClipStore
import com.us.android.feature.dating.selfie.SelfieOutcomes
import com.us.android.feature.dating.selfie.SelfiePhase
import com.us.android.feature.dating.selfie.SelfieRecordingState
import org.junit.After
import org.junit.Test
import java.io.File

/**
 * The parts of the blink-twice recorder that do not need a camera: the clip
 * directory, the one-clip-at-a-time state machine, and the duration cap.
 *
 * The camera itself (CameraX `VideoCapture` + `Recorder`) cannot be exercised on
 * the JVM and is not faked here — a fake would only assert its own shape.
 */
class SelfieRecordingTest {

    private val directory = File(System.getProperty("java.io.tmpdir"), "dating-selfie-test-${System.nanoTime()}")
    private val clips = SelfieClipStore(directory)

    @After
    fun tearDown() {
        directory.deleteRecursively()
    }

    // ── the clip directory ─────────────────────────────────────────────

    @Test
    fun `a new clip creates the directory and is not written yet`() {
        assertThat(directory.exists()).isFalse()

        val clip = clips.next(stamp = 1_000L)

        assertThat(directory.isDirectory).isTrue()
        assertThat(clip.parentFile).isEqualTo(directory)
        assertThat(clip.name).endsWith(".mp4")
        assertThat(clip.exists()).isFalse()
    }

    @Test
    fun `starting a clip sweeps away whatever the last attempt left behind`() {
        val stale = clips.next(stamp = 1_000L).apply { writeBytes(byteArrayOf(1, 2, 3)) }
        assertThat(stale.exists()).isTrue()

        val fresh = clips.next(stamp = 2_000L)

        assertThat(stale.exists()).isFalse()
        assertThat(fresh).isNotEqualTo(stale)
        assertThat(directory.listFiles()).isEmpty()
    }

    @Test
    fun `clearing deletes every clip, and a directory that never existed is fine`() {
        clips.next(stamp = 1_000L).writeBytes(byteArrayOf(9))
        clips.clear()
        assertThat(directory.listFiles()).isEmpty()

        directory.deleteRecursively()
        clips.clear()
    }

    // ── one clip at a time ─────────────────────────────────────────────

    @Test
    fun `a clip runs start to finish and is delivered`() {
        val state = SelfieRecordingState()
        assertThat(state.isBusy).isFalse()

        assertThat(state.beginRequested()).isTrue()
        assertThat(state.phase).isEqualTo(SelfiePhase.Starting)
        assertThat(state.isBusy).isTrue()

        state.started()
        assertThat(state.phase).isEqualTo(SelfiePhase.Recording)

        assertThat(state.stopRequested()).isTrue()
        assertThat(state.phase).isEqualTo(SelfiePhase.Stopping)

        assertThat(state.finished()).isTrue()
        assertThat(state.phase).isEqualTo(SelfiePhase.Idle)
        assertThat(state.isBusy).isFalse()
    }

    @Test
    fun `a second recording is refused while one is running`() {
        val state = SelfieRecordingState()
        assertThat(state.beginRequested()).isTrue()

        assertThat(state.beginRequested()).isFalse()
        state.started()
        assertThat(state.beginRequested()).isFalse()

        state.finished()
        assertThat(state.beginRequested()).isTrue()
    }

    @Test
    fun `the watchdog and the camera's own limit cannot stop the same clip twice`() {
        val state = SelfieRecordingState()
        state.beginRequested()
        state.started()

        assertThat(state.stopRequested()).isTrue()
        assertThat(state.stopRequested()).isFalse()
    }

    @Test
    fun `a cancelled clip is abandoned and never reaches the caller`() {
        val state = SelfieRecordingState()
        state.beginRequested()
        state.started()

        assertThat(state.cancelled()).isTrue()
        assertThat(state.abandoned).isTrue()

        assertThat(state.finished()).isFalse()
        assertThat(state.phase).isEqualTo(SelfiePhase.Idle)
    }

    @Test
    fun `cancelling after a clip was delivered touches nothing`() {
        val state = SelfieRecordingState()
        state.beginRequested()
        state.started()
        assertThat(state.finished()).isTrue()

        // The file now belongs to the uploader: leaving the screen must not
        // stop or delete anything.
        assertThat(state.cancelled()).isFalse()
        assertThat(state.abandoned).isFalse()
    }

    @Test
    fun `the next clip is delivered again after a cancelled one`() {
        val state = SelfieRecordingState()
        state.beginRequested()
        state.cancelled()
        state.finished()

        assertThat(state.beginRequested()).isTrue()
        assertThat(state.abandoned).isFalse()
        state.started()
        assertThat(state.finished()).isTrue()
    }

    // ── what came out of the recorder ──────────────────────────────────

    @Test
    fun `only a clip with bytes and no fatal error is uploaded`() {
        val empty = clips.next(stamp = 1_000L).apply { createNewFile() }
        assertThat(SelfieClipOutcome.usable(fatal = false, file = empty)).isFalse()

        val missing = File(directory, "gone.mp4")
        assertThat(SelfieClipOutcome.usable(fatal = false, file = missing)).isFalse()

        val written = File(directory, "written.mp4").apply { writeBytes(ByteArray(size = 64)) }
        assertThat(SelfieClipOutcome.usable(fatal = true, file = written)).isFalse()
        assertThat(SelfieClipOutcome.usable(fatal = false, file = written)).isTrue()
    }

    // ── the duration cap ───────────────────────────────────────────────

    @Test
    fun `the clip plus the watchdog still fits the server's limit`() {
        listOf(0, 1_000, 2_500, 3_000, 4_000, 10_000).forEach { serverCap ->
            val record = SelfieOutcomes.recordMillis(serverCap)
            assertThat(record + SelfieOutcomes.RECORD_WATCHDOG_MS).isAtMost(SelfieOutcomes.MAX_CLIP_MS.toLong())
        }
    }
}
