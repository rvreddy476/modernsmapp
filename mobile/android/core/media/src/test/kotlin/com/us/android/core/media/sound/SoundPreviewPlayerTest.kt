package com.us.android.core.media.sound

import androidx.media3.common.Player
import com.google.common.truth.Truth.assertThat
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The preview button: a sound played alone, on the sound page and in the
 * reel form.
 *
 * What this protects: the button saying what a tap will do (a sound paused
 * while it loads reads paused, not loading); the preview being AUDIBLE — the
 * factory's players start silent, and the Reels mute switch is not this
 * button's; a preview playing once rather than looping under the form; and a
 * sound that cannot be loaded saying so instead of spinning.
 *
 * Under Robolectric for one reason: `ExoPlayer` is an interface whose static
 * initialiser reads `android.os.Build`, so even a scripted stand-in for it
 * cannot be made on the bare JVM. Nothing here touches a real player.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class SoundPreviewPlayerTest {

    private val factory = FakePlayerFactory()
    private val asked = mutableListOf<String>()
    private var now = 1_000_000L

    private val preview = SoundPreviewPlayer(
        playerFactory = factory,
        sourceFor = { url ->
            asked += url
            fakeMediaSource()
        },
        serveUrl = { id -> "https://api.test/v1/audio/$id/serve" },
        clock = { now },
    )

    private val sound: FakeExoPlayer get() = factory.made.single()

    @Test
    fun `what the button shows follows the load and the listener's wish`() {
        assertThat(soundPreview(SoundLoad.NONE, wantsPlay = false)).isEqualTo(SoundPreview.IDLE)
        assertThat(soundPreview(SoundLoad.NONE, wantsPlay = true)).isEqualTo(SoundPreview.IDLE)
        assertThat(soundPreview(SoundLoad.LOADING, wantsPlay = true)).isEqualTo(SoundPreview.LOADING)
        assertThat(soundPreview(SoundLoad.LOADING, wantsPlay = false)).isEqualTo(SoundPreview.PAUSED)
        assertThat(soundPreview(SoundLoad.READY, wantsPlay = true)).isEqualTo(SoundPreview.PLAYING)
        assertThat(soundPreview(SoundLoad.READY, wantsPlay = false)).isEqualTo(SoundPreview.PAUSED)
        assertThat(soundPreview(SoundLoad.FAILED, wantsPlay = true)).isEqualTo(SoundPreview.FAILED)
        assertThat(soundPreview(SoundLoad.FAILED, wantsPlay = false)).isEqualTo(SoundPreview.FAILED)
    }

    @Test
    fun `it is idle, with no player made, until a sound is asked for`() {
        assertThat(preview.state.value).isEqualTo(SoundPreview.IDLE)
        assertThat(factory.made).isEmpty()
    }

    @Test
    fun `the first tap loads the sound from the serve route and plays it, audibly and once`() {
        preview.toggle("s1")

        assertThat(asked).containsExactly("https://api.test/v1/audio/s1/serve")
        assertThat(preview.state.value).isEqualTo(SoundPreview.LOADING)
        assertThat(sound.playWhenReady).isTrue()
        assertThat(sound.volume).isEqualTo(1f)
        assertThat(sound.repeatMode).isEqualTo(Player.REPEAT_MODE_OFF)

        sound.becomeReady()
        assertThat(preview.state.value).isEqualTo(SoundPreview.PLAYING)
    }

    @Test
    fun `a second tap pauses and a third plays again, without loading again`() {
        preview.toggle("s1")
        sound.becomeReady()

        preview.toggle("s1")
        assertThat(preview.state.value).isEqualTo(SoundPreview.PAUSED)
        assertThat(sound.playWhenReady).isFalse()

        preview.toggle("s1")
        assertThat(preview.state.value).isEqualTo(SoundPreview.PLAYING)
        assertThat(sound.playWhenReady).isTrue()
        assertThat(asked).hasSize(1)
    }

    @Test
    fun `a sound paused while it loads reads paused, and stays paused once loaded`() {
        preview.toggle("s1")
        preview.toggle("s1")
        assertThat(preview.state.value).isEqualTo(SoundPreview.PAUSED)

        sound.becomeReady()

        assertThat(preview.state.value).isEqualTo(SoundPreview.PAUSED)
        assertThat(sound.playWhenReady).isFalse()
    }

    @Test
    fun `played through, it goes back to the start and waits`() {
        preview.toggle("s1")
        sound.becomeReady()

        sound.state(Player.STATE_ENDED)

        assertThat(preview.state.value).isEqualTo(SoundPreview.PAUSED)
        assertThat(sound.playWhenReady).isFalse()
        assertThat(sound.calls("seekTo")).containsExactly("seekTo=0")
    }

    @Test
    fun `the Sound slider sets how loud the preview is`() {
        preview.setVolume(0.35)
        preview.toggle("s1")
        assertThat(sound.volume).isEqualTo(0.35f)

        preview.setVolume(0.0)
        assertThat(sound.volume).isEqualTo(0f)

        preview.setVolume(1.7)
        assertThat(sound.volume).isEqualTo(1f)
    }

    @Test
    fun `a sound that cannot be loaded says so, and a tap tries again`() {
        preview.toggle("s1")

        sound.fail()
        assertThat(preview.state.value).isEqualTo(SoundPreview.FAILED)
        assertThat(sound.playWhenReady).isFalse()

        preview.toggle("s1")
        assertThat(preview.state.value).isEqualTo(SoundPreview.LOADING)
        assertThat(asked).hasSize(2)
    }

    @Test
    fun `a sound with no address to ask has failed, and no player is made for it`() {
        val none = SoundPreviewPlayer(factory, { fakeMediaSource() }, { null }, { now })

        none.toggle("s1")

        assertThat(none.state.value).isEqualTo(SoundPreview.FAILED)
        assertThat(factory.made).isEmpty()
    }

    @Test
    fun `a sound that fails long after it loaded is asked for once more`() {
        preview.toggle("s1")
        sound.becomeReady()

        now += SOUND_RELOAD_AFTER_MS
        sound.fail()

        assertThat(asked).hasSize(2)
        assertThat(preview.state.value).isEqualTo(SoundPreview.LOADING)
        assertThat(sound.playWhenReady).isTrue()
    }

    @Test
    fun `another sound replaces the first on the one player`() {
        preview.toggle("s1")
        sound.becomeReady()

        preview.toggle("s2")

        assertThat(factory.made).hasSize(1)
        assertThat(asked.last()).isEqualTo("https://api.test/v1/audio/s2/serve")
        assertThat(preview.state.value).isEqualTo(SoundPreview.LOADING)
    }

    @Test
    fun `pausing for the background stops it, and pausing a paused preview changes nothing`() {
        preview.toggle("s1")
        sound.becomeReady()

        preview.pause()
        assertThat(preview.state.value).isEqualTo(SoundPreview.PAUSED)
        assertThat(sound.playWhenReady).isFalse()

        val calls = sound.calls.size
        preview.pause()
        assertThat(sound.calls).hasSize(calls)
    }

    @Test
    fun `stopping forgets the sound, and release lets the player go`() {
        preview.toggle("s1")
        sound.becomeReady()

        preview.stop()
        assertThat(preview.state.value).isEqualTo(SoundPreview.IDLE)
        assertThat(sound.playWhenReady).isFalse()
        assertThat(sound.calls).containsAtLeast("stop", "clearMediaItems").inOrder()

        val first = sound
        preview.release()
        assertThat(first.released).isTrue()
        assertThat(first.listeners).isEmpty()

        preview.toggle("s1")
        assertThat(factory.made).hasSize(2)
    }
}
