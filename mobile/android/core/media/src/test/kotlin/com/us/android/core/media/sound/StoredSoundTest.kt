package com.us.android.core.media.sound

import androidx.media3.common.Player
import androidx.media3.exoplayer.source.MediaSource
import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.Playback
import com.us.android.core.media.PlaybackKind
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * A reel saved offline plays its added sound from the device (2026-10-02).
 *
 * What this protects: with a stored sound the `serve` route is NOT asked
 * (there may be no network at all), the stored source is what the sound
 * player is prepared with, and a sound with no stored copy still goes to
 * the network exactly as before.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class StoredSoundTest {

    private val factory = FakePlayerFactory()
    private val video = FakeExoPlayer("video").apply {
        playbackState = Player.STATE_READY
        durationMs = 35_000L
    }
    private val askedOfNetwork = mutableListOf<String>()
    private val askedOfDevice = mutableListOf<Playback>()
    private val storedSource: MediaSource = fakeMediaSource()

    private val sounds = ReelSoundPlayer(
        playerFactory = factory,
        sourceFor = { url ->
            askedOfNetwork += url
            fakeMediaSource()
        },
        serveUrl = { id -> "https://api.test/v1/audio/$id/serve" },
        clock = { 0L },
        storedSourceFor = { playback ->
            askedOfDevice += playback
            storedSource
        },
    )

    private val stored = Playback.offline(url = "https://api.test/v1/audio/s1/serve", cacheKey = "p1/sound")

    @Test
    fun `a stored sound is played from the device and the network is not asked`() {
        sounds.attach(video.player, SoundTrack(id = "s1", startMs = 1_500L, stored = stored), SoundMix())

        assertThat(askedOfNetwork).isEmpty()
        assertThat(askedOfDevice).containsExactly(stored)
        assertThat(askedOfDevice.single().kind).isEqualTo(PlaybackKind.Offline)
        assertThat(factory.made.single().sources).containsExactly(storedSource)
        assertThat(sounds.load).isEqualTo(SoundLoad.LOADING)
    }

    /** The copy carries the bytes; the id is only for the sound line, and may not have been known. */
    @Test
    fun `a stored sound needs no sound id`() {
        sounds.attach(video.player, SoundTrack(id = "", stored = stored), SoundMix())

        assertThat(askedOfDevice).containsExactly(stored)
        assertThat(sounds.load).isEqualTo(SoundLoad.LOADING)
    }

    @Test
    fun `a sound with no stored copy is asked of the serve route, as before`() {
        sounds.attach(video.player, SoundTrack(id = "s1"), SoundMix())

        assertThat(askedOfDevice).isEmpty()
        assertThat(askedOfNetwork).containsExactly("https://api.test/v1/audio/s1/serve")
    }
}
