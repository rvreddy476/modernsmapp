package com.us.android.core.media.sound

import com.google.common.truth.Truth.assertThat
import org.junit.Test
import java.io.File

/**
 * The sync rules, case for case with the web's `soundSync.test.ts`
 * (postbook-ui `features/reels/__tests__`), so a reel with an added sound
 * sounds the same on both.
 *
 * What these protect: the sound landing where the video is after a seek or a
 * loop; small differences being LEFT ALONE (a correction is audible); a
 * sound shorter than the reel looping under it, with the distance measured
 * around the loop so the wrap is not read as a whole sound of drift; and the
 * one rule a viewer would hear at once if it broke — a sound that failed
 * must never leave a reel playing quietly, or silently, under nothing.
 */
class SoundSyncTest {

    /** A reel playing normally with a 28.4 s sound in step. */
    private fun input(
        videoTime: Double = 5.0,
        videoPlaying: Boolean = true,
        rate: Double = 1.0,
        viewerVolume: Double = 1.0,
        muted: Boolean = false,
        originalVolume: Double = 1.0,
        overlayVolume: Double = 1.0,
        startOffsetS: Double = 0.0,
        soundDurationS: Double = 28.4,
        load: SoundLoad = SoundLoad.READY,
        soundTime: Double = 5.0,
        soundSeeking: Boolean = false,
        reason: SyncReason = SyncReason.TICK,
    ) = SoundSyncInput(
        videoTime = videoTime,
        videoPlaying = videoPlaying,
        rate = rate,
        viewerVolume = viewerVolume,
        muted = muted,
        originalVolume = originalVolume,
        overlayVolume = overlayVolume,
        startOffsetS = startOffsetS,
        soundDurationS = soundDurationS,
        load = load,
        soundTime = soundTime,
        soundSeeking = soundSeeking,
        reason = reason,
    )

    private fun sound(input: SoundSyncInput): SoundCommand = checkNotNull(planSoundSync(input).sound)

    private fun assertClose(actual: Double?, expected: Double) {
        assertThat(actual).isNotNull()
        assertThat(actual!!).isWithin(TOLERANCE).of(expected)
    }

    // ── soundPosition: where the sound should be ────────────────────────

    @Test
    fun `the position is the start offset plus the video's clock`() {
        assertThat(soundPosition(5.0, 0.0, 28.4)).isEqualTo(5.0)
        assertThat(soundPosition(5.0, 1.5, 28.4)).isEqualTo(6.5)
        assertThat(soundPosition(0.0, 1.5, 28.4)).isEqualTo(1.5)
    }

    @Test
    fun `a sound shorter than the video loops, the position wraps by the sound's length`() {
        assertThat(soundPosition(30.0, 0.0, 10.0)).isEqualTo(0.0)
        assertThat(soundPosition(35.0, 0.0, 10.0)).isEqualTo(5.0)
        assertClose(soundPosition(30.0, 1.5, 28.4), 3.1)
        assertThat(soundPosition(95.0, 0.0, 10.0)).isEqualTo(5.0)
    }

    @Test
    fun `a start offset past the end wraps too`() {
        assertThat(soundPosition(0.0, 12.0, 10.0)).isEqualTo(2.0)
    }

    @Test
    fun `an unknown length cannot wrap, the position runs on`() {
        assertThat(soundPosition(40.0, 1.0, 0.0)).isEqualTo(41.0)
        assertThat(soundPosition(40.0, 1.0, Double.NaN)).isEqualTo(41.0)
        assertThat(soundPosition(40.0, 1.0, Double.POSITIVE_INFINITY)).isEqualTo(41.0)
    }

    @Test
    fun `the position is never negative and never NaN`() {
        assertThat(soundPosition(-3.0, 0.0, 10.0)).isEqualTo(0.0)
        assertThat(soundPosition(Double.NaN, 2.0, 10.0)).isEqualTo(2.0)
        assertThat(soundPosition(4.0, -2.0, 10.0)).isEqualTo(4.0)
        assertThat(soundPosition(4.0, Double.NaN, 10.0)).isEqualTo(4.0)
    }

    // ── soundDrift: measured around the loop ────────────────────────────

    @Test
    fun `the drift is the plain distance when both are mid-sound`() {
        assertClose(soundDrift(5.0, 5.2, 28.4), 0.2)
        assertClose(soundDrift(5.2, 5.0, 28.4), 0.2)
    }

    @Test
    fun `either side of the wrap the two are close, not a whole sound apart`() {
        assertClose(soundDrift(27.9, 0.1, 28.0), 0.2)
        assertClose(soundDrift(0.1, 27.9, 28.0), 0.2)
    }

    @Test
    fun `half a loop apart is the furthest two positions can be`() {
        assertThat(soundDrift(0.0, 14.0, 28.0)).isEqualTo(14.0)
        assertThat(soundDrift(0.0, 20.0, 28.0)).isEqualTo(8.0)
    }

    @Test
    fun `with no known length the distance is linear`() {
        assertClose(soundDrift(27.9, 0.1, 0.0), 27.8)
    }

    // ── correctionDue: the threshold ────────────────────────────────────

    @Test
    fun `about 0_3 s while running, smaller differences are left alone`() {
        assertThat(DRIFT_THRESHOLD_S).isEqualTo(0.3)
        assertThat(correctionDue(0.1)).isFalse()
        assertThat(correctionDue(0.29)).isFalse()
        assertThat(correctionDue(0.3)).isFalse()
        assertThat(correctionDue(0.31)).isTrue()
        assertThat(correctionDue(2.0)).isTrue()
    }

    @Test
    fun `tighter after a jump, and still not zero`() {
        assertThat(SNAP_THRESHOLD_S).isEqualTo(0.05)
        assertThat(correctionDue(0.04, SyncReason.JUMP)).isFalse()
        assertThat(correctionDue(0.06, SyncReason.JUMP)).isTrue()
        assertThat(correctionDue(0.2, SyncReason.JUMP)).isTrue()
        assertThat(correctionDue(0.2, SyncReason.TICK)).isFalse()
    }

    // ── planSoundSync: drift ────────────────────────────────────────────

    @Test
    fun `in step, nothing is moved`() {
        val plan = planSoundSync(input())

        assertThat(plan.sound).isNotNull()
        assertThat(plan.sound!!.seekTo).isNull()
        assertThat(plan.sound!!.targetTime).isEqualTo(5.0)
        assertThat(plan.sound!!.play).isTrue()
    }

    @Test
    fun `0_25 s out while running is left alone and 0_35 s out is corrected to the target`() {
        assertThat(sound(input(soundTime = 5.25)).seekTo).isNull()
        assertThat(sound(input(soundTime = 4.75)).seekTo).isNull()
        assertThat(sound(input(soundTime = 5.35)).seekTo).isEqualTo(5.0)
        assertThat(sound(input(soundTime = 4.65)).seekTo).isEqualTo(5.0)
    }

    @Test
    fun `a sound that is mid-seek is not moved again, however far out`() {
        assertThat(sound(input(soundTime = 20.0, soundSeeking = true)).seekTo).isNull()
        assertThat(sound(input(soundTime = 20.0, soundSeeking = false)).seekTo).isEqualTo(5.0)
    }

    @Test
    fun `after a seek the sound lands on the new position`() {
        val plan = planSoundSync(input(videoTime = 12.0, soundTime = 5.0, reason = SyncReason.JUMP))

        assertThat(plan.sound!!.seekTo).isEqualTo(12.0)
    }

    @Test
    fun `a jump inside the running threshold is still corrected`() {
        assertThat(sound(input(soundTime = 5.2, reason = SyncReason.JUMP)).seekTo).isEqualTo(5.0)
        assertThat(sound(input(soundTime = 5.2, reason = SyncReason.TICK)).seekTo).isNull()
    }

    // ── planSoundSync: looping ──────────────────────────────────────────

    @Test
    fun `a 10 s sound under a 35 s reel, the target wraps`() {
        val command = sound(input(videoTime = 35.0, soundDurationS = 10.0, soundTime = 5.0))

        assertThat(command.targetTime).isEqualTo(5.0)
        assertThat(command.seekTo).isNull()
    }

    @Test
    fun `the player wrapping a moment before the clock does is not a drift`() {
        // 28.4 s sound: the video is at 28.35 (target 28.35), the looping player has already wrapped to 0.05.
        val command = sound(input(videoTime = 28.35, soundTime = 0.05))

        assertClose(command.targetTime, 28.35)
        assertThat(command.seekTo).isNull()
    }

    @Test
    fun `the video looping back to the start pulls the sound back with it`() {
        assertClose(sound(input(videoTime = 0.1, soundTime = 15.0)).seekTo, 0.1)
    }

    @Test
    fun `the start offset is kept on every lap of the video`() {
        assertThat(sound(input(videoTime = 0.0, startOffsetS = 1.5, soundTime = 1.5)).targetTime).isEqualTo(1.5)
        assertThat(sound(input(videoTime = 0.0, startOffsetS = 1.5, soundTime = 9.0)).seekTo).isEqualTo(1.5)
    }

    // ── planSoundSync: play, pause, rate, mute ──────────────────────────

    @Test
    fun `the sound runs only while the video does`() {
        assertThat(sound(input(videoPlaying = true)).play).isTrue()
        assertThat(sound(input(videoPlaying = false)).play).isFalse()
    }

    @Test
    fun `a paused video still places the sound, so it resumes in step`() {
        val command = sound(input(videoPlaying = false, videoTime = 9.0, soundTime = 2.0, reason = SyncReason.JUMP))

        assertThat(command.play).isFalse()
        assertThat(command.seekTo).isEqualTo(9.0)
    }

    @Test
    fun `the sound takes the video's rate, and a rate that is no rate is 1`() {
        assertThat(sound(input(rate = 1.5)).rate).isEqualTo(1.5)
        assertThat(sound(input(rate = 0.25)).rate).isEqualTo(0.25)
        assertThat(sound(input(rate = 0.0)).rate).isEqualTo(1.0)
        assertThat(sound(input(rate = Double.NaN)).rate).isEqualTo(1.0)
        assertThat(sound(input(rate = -2.0)).rate).isEqualTo(1.0)
    }

    @Test
    fun `mute is one switch for both, the sound is muted exactly when the video is`() {
        assertThat(sound(input(muted = true)).muted).isTrue()
        assertThat(sound(input(muted = false)).muted).isFalse()
    }

    // ── volumes ─────────────────────────────────────────────────────────

    @Test
    fun `video is viewer times original and sound is viewer times overlay`() {
        val plan = planSoundSync(input(viewerVolume = 0.5, originalVolume = 0.4, overlayVolume = 0.8))

        assertClose(plan.videoVolume, 0.2)
        assertClose(plan.sound!!.volume, 0.4)
    }

    @Test
    fun `a creator who muted the original gets a silent video under the sound`() {
        val plan = planSoundSync(input(viewerVolume = 0.9, originalVolume = 0.0, overlayVolume = 1.0))

        assertThat(plan.videoVolume).isEqualTo(0.0)
        assertClose(plan.sound!!.volume, 0.9)
    }

    @Test
    fun `a creator who muted the sound gets a silent sound`() {
        assertThat(sound(input(overlayVolume = 0.0)).volume).isEqualTo(0.0)
    }

    @Test
    fun `everything stays inside 0 to 1`() {
        assertThat(clamp01(1.4)).isEqualTo(1.0)
        assertThat(clamp01(-1.0)).isEqualTo(0.0)
        assertThat(clamp01(Double.NaN)).isEqualTo(1.0)
        assertThat(videoVolume(2.0, 3.0, SoundLoad.READY)).isEqualTo(1.0)
        assertThat(soundVolume(2.0, -1.0)).isEqualTo(0.0)
    }

    // ── failure is silent: the reel plays alone at the viewer's full volume ──

    @Test
    fun `a sound that failed to load has no command, and the creator's original level is dropped`() {
        val plan = planSoundSync(input(load = SoundLoad.FAILED, viewerVolume = 0.7, originalVolume = 0.2))

        assertThat(plan.sound).isNull()
        assertClose(plan.videoVolume, 0.7)
    }

    @Test
    fun `even when the creator muted the original, a failed sound must not leave the reel silent`() {
        val plan = planSoundSync(input(load = SoundLoad.FAILED, viewerVolume = 1.0, originalVolume = 0.0))

        assertThat(plan.videoVolume).isEqualTo(1.0)
        assertClose(videoVolume(0.6, 0.0, SoundLoad.FAILED), 0.6)
    }

    @Test
    fun `a reel with no added sound plays at the viewer's level, whatever the creator levels say`() {
        val plan = planSoundSync(input(load = SoundLoad.NONE, viewerVolume = 0.8, originalVolume = 0.3))

        assertThat(plan.sound).isNull()
        assertClose(plan.videoVolume, 0.8)
    }

    @Test
    fun `while the sound is still loading nothing plays beside the video, and the mix is already applied`() {
        val plan = planSoundSync(input(load = SoundLoad.LOADING, viewerVolume = 1.0, originalVolume = 0.3))

        assertThat(plan.sound).isNull()
        assertClose(plan.videoVolume, 0.3)
    }

    // ── onSoundPlayRefused: the platform's autoplay rule ────────────────

    @Test
    fun `an unmuted start refused mutes both sides`() {
        assertThat(onSoundPlayRefused("NotAllowedError", soundMuted = false)).isEqualTo(PlayRefusedAction.MUTE_BOTH)
    }

    @Test
    fun `already muted, or interrupted by a pause, there is nothing to do`() {
        assertThat(onSoundPlayRefused("NotAllowedError", soundMuted = true)).isEqualTo(PlayRefusedAction.IGNORE)
        assertThat(onSoundPlayRefused("AbortError", soundMuted = false)).isEqualTo(PlayRefusedAction.IGNORE)
        assertThat(onSoundPlayRefused(null, soundMuted = false)).isEqualTo(PlayRefusedAction.IGNORE)
    }

    // ── soundDurationS ──────────────────────────────────────────────────

    @Test
    fun `the player's own length wins, and the declared one stands in until it is known`() {
        assertThat(soundDurationS(28.4, 30_000L)).isEqualTo(28.4)
        assertThat(soundDurationS(Double.NaN, 28_400L)).isEqualTo(28.4)
        assertThat(soundDurationS(null, 28_400L)).isEqualTo(28.4)
        assertThat(soundDurationS(Double.POSITIVE_INFINITY, 28_400L)).isEqualTo(28.4)
        assertThat(soundDurationS(0.0, 0L)).isEqualTo(0.0)
        assertThat(soundDurationS(null, null)).isEqualTo(0.0)
        assertThat(soundDurationS(null, -5L)).isEqualTo(0.0)
    }

    // ── a sound that fails after it had loaded ──────────────────────────

    @Test
    fun `a sound that fails is asked for again once its link has had time to expire`() {
        assertThat(SOUND_RELOAD_AFTER_MS).isEqualTo(30_000L)
        assertThat(onSoundError(SoundLoad.READY, SOUND_RELOAD_AFTER_MS)).isEqualTo(SoundErrorAction.RELOAD)
        assertThat(onSoundError(SoundLoad.READY, 6L * 60_000L)).isEqualTo(SoundErrorAction.RELOAD)
    }

    @Test
    fun `it fails for good when it breaks straight after loading, so a broken file cannot reload in a loop`() {
        assertThat(onSoundError(SoundLoad.READY, 0L)).isEqualTo(SoundErrorAction.FAIL)
        assertThat(onSoundError(SoundLoad.READY, SOUND_RELOAD_AFTER_MS - 1)).isEqualTo(SoundErrorAction.FAIL)
        assertThat(onSoundError(SoundLoad.READY, null)).isEqualTo(SoundErrorAction.FAIL)
    }

    @Test
    fun `it is not asked for again when it never loaded`() {
        for (load in listOf(SoundLoad.LOADING, SoundLoad.FAILED, SoundLoad.NONE)) {
            assertThat(onSoundError(load, 10L * 60_000L)).isEqualTo(SoundErrorAction.FAIL)
        }
    }

    // ── Android's additions ─────────────────────────────────────────────

    @Test
    fun `muted is silence on a player, whatever the level, and a level is kept inside 0 to 1`() {
        assertThat(appliedVolume(0.8, muted = true)).isEqualTo(0f)
        assertThat(appliedVolume(1.0, muted = true)).isEqualTo(0f)
        assertThat(appliedVolume(0.8, muted = false)).isEqualTo(0.8f)
        assertThat(appliedVolume(0.0, muted = false)).isEqualTo(0f)
        assertThat(appliedVolume(1.7, muted = false)).isEqualTo(1f)
        assertThat(appliedVolume(-0.3, muted = false)).isEqualTo(0f)
    }

    @Test
    fun `the sound is seeking only while a seek that was asked for is still buffering`() {
        assertThat(soundSeeking(seekIssued = true, buffering = true)).isTrue()
        assertThat(soundSeeking(seekIssued = true, buffering = false)).isFalse()
        // Buffering for the network is not a seek: a sound that stalled may still be corrected.
        assertThat(soundSeeking(seekIssued = false, buffering = true)).isFalse()
        assertThat(soundSeeking(seekIssued = false, buffering = false)).isFalse()
    }

    @Test
    fun `a sound still loading after ten seconds has failed, and only one that is loading`() {
        assertThat(SOUND_LOAD_TIMEOUT_MS).isEqualTo(10_000L)
        assertThat(loadTimedOut(SoundLoad.LOADING, 9_999L)).isFalse()
        assertThat(loadTimedOut(SoundLoad.LOADING, 10_000L)).isTrue()
        assertThat(loadTimedOut(SoundLoad.LOADING, 60_000L)).isTrue()
        for (load in listOf(SoundLoad.READY, SoundLoad.FAILED, SoundLoad.NONE)) {
            assertThat(loadTimedOut(load, 60_000L)).isFalse()
        }
    }

    // ── the module and its wiring ───────────────────────────────────────

    private fun source(name: String): String =
        File("src/main/kotlin/com/us/android/core/media/sound/$name").readText()

    /** Comments say what the code must not: they are taken out before the code is read. */
    private fun code(name: String): String = source(name)
        .replace(Regex("/\\*[\\s\\S]*?\\*/"), "")
        .replace(Regex("//.*$", RegexOption.MULTILINE), "")

    @Test
    fun `the planner touches no player, no Android, no clock and no timer`() {
        // The package line names the app ("com.us.android…"); it is not a dependency.
        val planner = code("SoundSync.kt").lines().filterNot { it.startsWith("package ") }.joinToString("\n")

        for (banned in listOf("import ", "android", "media3", "Player", "System.", "Handler", "delay(", "Timer")) {
            assertThat(planner).doesNotContain(banned)
        }
    }

    @Test
    fun `the player decides nothing itself, every level and position comes from the planner`() {
        val player = code("ReelSoundPlayer.kt")

        assertThat(player).contains("planSoundSync(")
        assertThat(player).contains("appliedVolume(plan.videoVolume, muted)")
        assertThat(player).contains("appliedVolume(command.volume, command.muted)")
        // A volume is written in exactly three places, each from the plan: the video's in obey
        // and in detach, the sound's in obey.
        assertThat(Regex("\\.volume = ").findAll(player).count()).isEqualTo(3)
        assertThat(player).doesNotContain("volume = 1f")
        assertThat(player).doesNotContain("volume = 0f")
        // No arithmetic on positions outside the planner: the target is only converted to milliseconds.
        assertThat(player).doesNotContain("DRIFT_THRESHOLD_S")
        assertThat(player).doesNotContain("SNAP_THRESHOLD_S")
    }

    @Test
    fun `the sound is built by the injected factory and the one media source chain`() {
        for (name in listOf("ReelSoundPlayer.kt", "SoundPreviewPlayer.kt")) {
            val player = code(name)

            assertThat(player).doesNotContain("ExoPlayer.Builder")
            assertThat(player).contains("playerFactory.create()")
            assertThat(player).contains("sources.create(Playback.original(url))")
            assertThat(player).contains("soundServeUrl(config.baseUrl, id)")
        }
    }

    @Test
    fun `a sound failure raises nothing, no log and no message in the player`() {
        val player = code("ReelSoundPlayer.kt")

        for (banned in listOf("Log.", "println", "Toast", "UsMessage", "Timber")) {
            assertThat(player).doesNotContain(banned)
        }
    }

    private companion object {
        const val TOLERANCE = 1e-6
    }
}
