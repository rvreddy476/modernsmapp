package com.us.android.core.media.sound

import androidx.media3.common.Player
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The wiring of [ReelSoundPlayer], against two scripted players: that the
 * video's events reach the planner, and that the planner's answer reaches
 * both players.
 *
 * What this protects, in the order a viewer would notice it breaking: the
 * sound starting with the video and stopping with it; the mute switch moving
 * BOTH players; the creator's mix being on the video before it plays; a
 * sound that fails leaving the reel at full volume, silently; the video
 * getting its plain volume back when the pager moves on; and the player
 * never chasing its own seeks.
 *
 * Under Robolectric for one reason: `ExoPlayer` is an interface whose static
 * initialiser reads `android.os.Build`, so even a scripted stand-in for it
 * cannot be made on the bare JVM. Nothing here touches a real player.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ReelSoundPlayerTest {

    private val factory = FakePlayerFactory()
    private val video = FakeExoPlayer("video").apply {
        playbackState = Player.STATE_READY
        durationMs = 35_000L
        volume = 1f
    }
    private val asked = mutableListOf<String>()
    private var now = 1_000_000L

    private val sounds = ReelSoundPlayer(
        playerFactory = factory,
        sourceFor = { url ->
            asked += url
            fakeMediaSource()
        },
        serveUrl = { id -> "https://api.test/v1/audio/$id/serve" },
        clock = { now },
    )

    private val sound: FakeExoPlayer get() = factory.made.single()

    private val track = SoundTrack(id = "s1", startMs = 1_500L, durationMs = 28_400L)
    private val mix = SoundMix(originalVolume = 0.2, overlayVolume = 0.8)

    /** The settled page plays a sound, un-muted, and the sound has loaded. */
    private fun attachedAndReady(muted: Boolean = false) {
        sounds.setMuted(muted)
        sounds.attach(video.player, track, mix)
        sound.becomeReady(durationMs = 28_400L)
    }

    // ── Loading ─────────────────────────────────────────────────────────

    @Test
    fun `nothing is made until a reel with a sound is settled on`() {
        assertThat(factory.made).isEmpty()
        assertThat(sounds.load).isEqualTo(SoundLoad.NONE)
    }

    @Test
    fun `attaching asks the serve route for the sound and prepares it, paused`() {
        sounds.attach(video.player, track, mix)

        assertThat(asked).containsExactly("https://api.test/v1/audio/s1/serve")
        assertThat(sound.sources).hasSize(1)
        assertThat(sound.calls).containsAtLeast("setMediaSource", "prepare").inOrder()
        assertThat(sound.playWhenReady).isFalse()
        assertThat(sounds.load).isEqualTo(SoundLoad.LOADING)
    }

    @Test
    fun `the creator's mix is on the video before the sound has loaded`() {
        sounds.setMuted(false)
        sounds.attach(video.player, track, mix)

        assertThat(video.volume).isEqualTo(0.2f)
    }

    @Test
    fun `once loaded the sound lands on the start offset plus the video's clock`() {
        video.positionMs = 5_000L
        attachedAndReady()

        assertThat(sounds.load).isEqualTo(SoundLoad.READY)
        assertThat(sound.calls("seekTo")).containsExactly("seekTo=6500")
        assertThat(sound.volume).isEqualTo(0.8f)
    }

    // ── Play and pause ──────────────────────────────────────────────────

    @Test
    fun `the sound runs only while the video is really advancing`() {
        attachedAndReady()
        assertThat(sound.playWhenReady).isFalse()

        video.playing(true)
        assertThat(sound.playWhenReady).isTrue()

        video.playing(false)
        assertThat(sound.playWhenReady).isFalse()
    }

    @Test
    fun `a video waiting for data holds the sound, and its return starts it again`() {
        attachedAndReady()
        video.playing(true)

        video.playing = false
        video.state(Player.STATE_BUFFERING)
        assertThat(sound.playWhenReady).isFalse()

        video.playing = true
        video.state(Player.STATE_READY)
        assertThat(sound.playWhenReady).isTrue()
    }

    @Test
    fun `a video that fails stops the sound with it`() {
        attachedAndReady()
        video.playing(true)

        video.playing = false
        video.fail()

        assertThat(sound.playWhenReady).isFalse()
    }

    // ── Seek, loop and rate ─────────────────────────────────────────────

    @Test
    fun `a seek on the video moves the sound to the same place`() {
        attachedAndReady()
        sound.state(Player.STATE_READY)

        video.jumpTo(12_000L)

        assertThat(sound.calls("seekTo").last()).isEqualTo("seekTo=13500")
    }

    @Test
    fun `the video looping back to the start pulls the sound back with it`() {
        attachedAndReady()
        sound.state(Player.STATE_READY)
        sound.positionMs = 27_000L

        video.jumpTo(0L, Player.DISCONTINUITY_REASON_AUTO_TRANSITION)

        assertThat(sound.calls("seekTo").last()).isEqualTo("seekTo=1500")
    }

    @Test
    fun `a sound shorter than the reel wraps by its own length`() {
        sounds.setMuted(false)
        sounds.attach(video.player, SoundTrack(id = "s1", durationMs = 10_000L), mix)
        sound.becomeReady(durationMs = 10_000L)
        sound.state(Player.STATE_READY)

        video.jumpTo(34_000L)

        assertThat(sound.calls("seekTo").last()).isEqualTo("seekTo=4000")
    }

    @Test
    fun `the declared length stands in while the player does not know its own`() {
        sounds.setMuted(false)
        sounds.attach(video.player, SoundTrack(id = "s1", durationMs = 10_000L), mix)
        sound.becomeReady() // the player still reports no duration
        sound.state(Player.STATE_READY)

        video.jumpTo(34_000L)

        assertThat(sound.calls("seekTo").last()).isEqualTo("seekTo=4000")
    }

    @Test
    fun `the sound takes the video's rate`() {
        attachedAndReady()

        video.rate(1.5f)

        assertThat(sound.speed).isEqualTo(1.5f)
    }

    // ── Drift ───────────────────────────────────────────────────────────

    @Test
    fun `a tick leaves a small difference alone and corrects a large one`() {
        attachedAndReady()
        sound.state(Player.STATE_READY)
        video.playing(true)
        val before = sound.calls("seekTo").size

        video.positionMs = 5_000L
        sound.positionMs = 6_500L + 250L
        sounds.tick()
        assertThat(sound.calls("seekTo")).hasSize(before)

        sound.positionMs = 6_500L + 350L
        sounds.tick()
        assertThat(sound.calls("seekTo").last()).isEqualTo("seekTo=6500")
        assertThat(sound.calls("seekTo")).hasSize(before + 1)
    }

    @Test
    fun `a sound that is still landing a seek is not moved again`() {
        attachedAndReady()
        sound.state(Player.STATE_READY)
        video.playing(true)
        video.jumpTo(12_000L)
        val after = sound.calls("seekTo").size
        assertThat(sound.playbackState).isEqualTo(Player.STATE_BUFFERING)

        // The video runs on while the sound's seek has not landed: far out, and left alone.
        video.positionMs = 14_000L
        sounds.tick()
        assertThat(sound.calls("seekTo")).hasSize(after)

        // Landed. The next tick sees the real difference and corrects it.
        sound.state(Player.STATE_READY)
        sounds.tick()
        assertThat(sound.calls("seekTo")).hasSize(after + 1)
        assertThat(sound.calls("seekTo").last()).isEqualTo("seekTo=15500")
    }

    @Test
    fun `the sound landing a seek is not answered with another jump`() {
        attachedAndReady()
        video.playing(true)
        video.jumpTo(12_000L)
        val after = sound.calls("seekTo").size

        // The video has moved on by the time the seek lands; a jump here would seek again, for ever.
        video.positionMs = 12_200L
        sound.state(Player.STATE_READY)

        assertThat(sound.calls("seekTo")).hasSize(after)
    }

    // ── Mute ────────────────────────────────────────────────────────────

    @Test
    fun `it starts muted, both players silent, until the viewer's choice is known`() {
        sounds.attach(video.player, track, mix)
        sound.becomeReady()

        assertThat(video.volume).isEqualTo(0f)
        assertThat(sound.volume).isEqualTo(0f)
    }

    @Test
    fun `mute and un-mute move both players together`() {
        attachedAndReady(muted = false)
        assertThat(video.volume).isEqualTo(0.2f)
        assertThat(sound.volume).isEqualTo(0.8f)

        sounds.setMuted(true)
        assertThat(video.volume).isEqualTo(0f)
        assertThat(sound.volume).isEqualTo(0f)

        sounds.setMuted(false)
        assertThat(video.volume).isEqualTo(0.2f)
        assertThat(sound.volume).isEqualTo(0.8f)
    }

    @Test
    fun `a creator who muted the original gets a silent video under the sound`() {
        sounds.setMuted(false)
        sounds.attach(video.player, track, SoundMix(originalVolume = 0.0, overlayVolume = 1.0))
        sound.becomeReady()

        assertThat(video.volume).isEqualTo(0f)
        assertThat(sound.volume).isEqualTo(1f)
    }

    // ── Failure ─────────────────────────────────────────────────────────

    @Test
    fun `a sound that cannot be loaded leaves the reel at the viewer's full level`() {
        sounds.setMuted(false)
        sounds.attach(video.player, track, SoundMix(originalVolume = 0.0, overlayVolume = 1.0))
        assertThat(video.volume).isEqualTo(0f)

        sound.fail()

        assertThat(sounds.load).isEqualTo(SoundLoad.FAILED)
        assertThat(video.volume).isEqualTo(1f)
        assertThat(sound.playWhenReady).isFalse()
        assertThat(sound.calls("prepare")).hasSize(1)
    }

    @Test
    fun `a failed sound stays muted with the reel when the viewer has muted it`() {
        sounds.setMuted(true)
        sounds.attach(video.player, track, mix)

        sound.fail()

        assertThat(video.volume).isEqualTo(0f)
    }

    @Test
    fun `a sound with no address to ask is a failed sound, and no player is made for it`() {
        val player = ReelSoundPlayer(factory, { fakeMediaSource() }, { null }, { now })
        player.setMuted(false)

        player.attach(video.player, track, mix)

        assertThat(player.load).isEqualTo(SoundLoad.FAILED)
        assertThat(factory.made).isEmpty()
        assertThat(video.volume).isEqualTo(1f)
    }

    @Test
    fun `a sound still loading after ten seconds is given up on`() {
        sounds.setMuted(false)
        sounds.attach(video.player, track, mix)

        now += SOUND_LOAD_TIMEOUT_MS - 1
        sounds.tick()
        assertThat(sounds.load).isEqualTo(SoundLoad.LOADING)
        assertThat(video.volume).isEqualTo(0.2f)

        now += 1
        sounds.tick()
        assertThat(sounds.load).isEqualTo(SoundLoad.FAILED)
        assertThat(video.volume).isEqualTo(1f)
        assertThat(sound.calls).contains("stop")
    }

    @Test
    fun `a sound that fails long after it loaded is asked for once more`() {
        attachedAndReady()
        video.playing(true)

        now += SOUND_RELOAD_AFTER_MS
        sound.fail()

        assertThat(sounds.load).isEqualTo(SoundLoad.LOADING)
        assertThat(asked).hasSize(2)
        assertThat(sound.calls("prepare")).hasSize(2)
        // The mix is still on: a sound is on its way.
        assertThat(video.volume).isEqualTo(0.2f)

        sound.becomeReady()
        assertThat(sounds.load).isEqualTo(SoundLoad.READY)
        assertThat(sound.playWhenReady).isTrue()
    }

    @Test
    fun `a sound that fails straight after loading is not asked for again`() {
        attachedAndReady()

        now += SOUND_RELOAD_AFTER_MS - 1
        sound.fail()

        assertThat(sounds.load).isEqualTo(SoundLoad.FAILED)
        assertThat(asked).hasSize(1)
        assertThat(video.volume).isEqualTo(1f)
    }

    @Test
    fun `a reload that fails is not reloaded again`() {
        attachedAndReady()
        now += SOUND_RELOAD_AFTER_MS
        sound.fail()
        assertThat(sounds.load).isEqualTo(SoundLoad.LOADING)

        now += SOUND_RELOAD_AFTER_MS
        sound.fail()

        assertThat(sounds.load).isEqualTo(SoundLoad.FAILED)
        assertThat(asked).hasSize(2)
    }

    // ── Detach, re-attach, release ──────────────────────────────────────

    @Test
    fun `detaching stops the sound and gives the video back at the plain level`() {
        attachedAndReady()
        video.playing(true)

        sounds.detach()

        assertThat(sounds.load).isEqualTo(SoundLoad.NONE)
        assertThat(sound.playWhenReady).isFalse()
        assertThat(sound.calls).containsAtLeast("stop", "clearMediaItems").inOrder()
        assertThat(video.volume).isEqualTo(1f)
        assertThat(video.listeners).isEmpty()
    }

    @Test
    fun `detaching while muted gives the video back muted`() {
        attachedAndReady(muted = true)

        sounds.detach()

        assertThat(video.volume).isEqualTo(0f)
    }

    @Test
    fun `a detached video's events move nothing`() {
        attachedAndReady()
        sounds.detach()
        val calls = sound.calls.size

        video.playing(true)
        sounds.tick()
        sound.fail()

        assertThat(sound.calls).hasSize(calls)
        assertThat(sounds.load).isEqualTo(SoundLoad.NONE)
    }

    @Test
    fun `attaching the same sound to the same player again takes the new mix and loads nothing`() {
        attachedAndReady()

        sounds.attach(video.player, track, SoundMix(originalVolume = 0.5, overlayVolume = 0.4))

        assertThat(asked).hasSize(1)
        assertThat(video.listeners).hasSize(1)
        assertThat(video.volume).isEqualTo(0.5f)
        assertThat(sound.volume).isEqualTo(0.4f)
    }

    @Test
    fun `another reel's sound replaces the first on the one player`() {
        attachedAndReady()
        val next = FakeExoPlayer("next").apply { volume = 1f }

        sounds.attach(next.player, SoundTrack(id = "s2"), SoundMix(originalVolume = 0.6, overlayVolume = 1.0))

        assertThat(factory.made).hasSize(1)
        assertThat(asked).containsExactly(
            "https://api.test/v1/audio/s1/serve",
            "https://api.test/v1/audio/s2/serve",
        ).inOrder()
        assertThat(video.volume).isEqualTo(1f)
        assertThat(video.listeners).isEmpty()
        assertThat(next.volume).isEqualTo(0.6f)
        assertThat(next.listeners).hasSize(1)
    }

    @Test
    fun `pausing for the background stops the sound until the video speaks again`() {
        attachedAndReady()
        video.playing(true)

        sounds.pause()
        assertThat(sound.playWhenReady).isFalse()

        video.playing(true)
        assertThat(sound.playWhenReady).isTrue()
    }

    @Test
    fun `release lets the player go, and a later attach makes a new one`() {
        attachedAndReady()
        val first = sound

        sounds.release()

        assertThat(first.released).isTrue()
        assertThat(first.listeners).isEmpty()
        assertThat(video.listeners).isEmpty()
        assertThat(video.volume).isEqualTo(1f)

        sounds.attach(video.player, track, mix)
        assertThat(factory.made).hasSize(2)
        assertThat(factory.made.last().released).isFalse()
    }

    @Test
    fun `releasing with nothing attached is safe`() {
        sounds.release()
        sounds.detach()
        sounds.pause()
        sounds.tick()

        assertThat(factory.made).isEmpty()
    }
}
