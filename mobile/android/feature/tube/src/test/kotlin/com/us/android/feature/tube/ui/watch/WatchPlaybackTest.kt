package com.us.android.feature.tube.ui.watch

import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSpec
import androidx.media3.datasource.HttpDataSource
import androidx.media3.exoplayer.ExoPlaybackException
import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.Playback
import com.us.android.core.media.PlaybackKind
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.IOException

/**
 * The long-video player kept playable (audit H1, M2, 2026-11-07): an
 * expired signed MP4 is read again once and prepared where it was, an error
 * nothing healed is [WatchPlayback.failed] with Retry, and a screen behind
 * another lets go of its decoder and takes it back where it was.
 *
 * Robolectric for `Uri`, which the HTTP error carries.
 */
@OptIn(UnstableApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class WatchPlaybackTest {

    private val mp4 = Playback(url = "https://cdn/m1/original.mp4?sig=old", kind = PlaybackKind.Progressive)
    private val freshMp4 = mp4.copy(url = "https://cdn/m1/original.mp4?sig=new")
    private val hls = Playback(url = "https://api/v1/media/m1/hls/master.m3u8", kind = PlaybackKind.Hls)

    private val fake = FakeWatchPlayer()
    private val built = mutableListOf<Playback>()
    private val reads = mutableListOf<Playback>()
    private var answer: CompletableDeferred<Playback?> = CompletableDeferred(freshMp4)

    private val playback = WatchPlayback(
        player = fake.player,
        sourceFor = { target ->
            built += target
            fakeSource()
        },
        scope = TestScope(UnconfinedTestDispatcher()),
        freshOriginal = { stale ->
            reads += stale
            answer.await()
        },
    )

    private fun forbidden(): ExoPlaybackException = ExoPlaybackException.createForSource(
        HttpDataSource.InvalidResponseCodeException(
            403,
            "Forbidden",
            IOException("expired"),
            emptyMap(),
            DataSpec(Uri.parse("https://cdn/m1/original.mp4")),
            ByteArray(0),
        ),
        PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS,
    )

    private fun otherError(): ExoPlaybackException = ExoPlaybackException.createForUnexpected(
        IllegalStateException("decoder"),
        PlaybackException.ERROR_CODE_DECODING_FAILED,
    )

    /** Prepared, playing, and [atMs] in. */
    private fun playing(target: Playback, atMs: Long = 42_000L) {
        playback.prepare(target)
        fake.state(Player.STATE_READY)
        fake.positionMs = atMs
        fake.calls.clear()
        built.clear()
    }

    @Test
    fun `an expired MP4 is read again and prepared where it was, still playing`() {
        playing(mp4)

        fake.fail(forbidden())

        assertThat(reads).containsExactly(mp4)
        assertThat(built).containsExactly(freshMp4)
        assertThat(fake.calls).containsExactly("setMediaSource@42000", "prepare", "playWhenReady=true").inOrder()
        assertThat(playback.playback).isEqualTo(freshMp4)
        assertThat(playback.failed.value).isFalse()
    }

    @Test
    fun `an MP4 that is refused again before it plays is left to show Retry`() {
        playing(mp4)

        fake.fail(forbidden())
        fake.fail(forbidden())

        assertThat(reads).hasSize(WatchPlayback.MAX_ORIGINAL_REFRESHES)
        assertThat(playback.failed.value).isTrue()
    }

    @Test
    fun `an MP4 that played again earns another fresh address`() {
        playing(mp4)
        fake.fail(forbidden())
        fake.state(Player.STATE_READY)

        fake.fail(forbidden())

        assertThat(reads).hasSize(2)
        assertThat(playback.failed.value).isFalse()
    }

    @Test
    fun `an error a fresh address cannot heal is not read again, and shows Retry`() {
        playing(mp4)

        fake.fail(otherError())

        assertThat(reads).isEmpty()
        assertThat(fake.calls).isEmpty()
        assertThat(playback.failed.value).isTrue()
    }

    @Test
    fun `Retry prepares again where it was, with a fresh address and no failure`() {
        playing(mp4)
        fake.fail(otherError())
        fake.calls.clear()

        playback.retry()

        assertThat(reads).containsExactly(mp4)
        assertThat(fake.calls).containsExactly("setMediaSource@42000", "prepare", "playWhenReady=true").inOrder()
        assertThat(playback.failed.value).isFalse()
    }

    @Test
    fun `Retry on HLS prepares the same playlist again, with no read`() {
        playing(hls)
        fake.fail(otherError())

        playback.retry()

        assertThat(reads).isEmpty()
        assertThat(built).containsExactly(hls)
        assertThat(playback.failed.value).isFalse()
    }

    @Test
    fun `a fresh address answered after another video opened is dropped`() {
        answer = CompletableDeferred()
        playing(mp4)
        fake.fail(forbidden())

        playback.prepare(hls)
        fake.calls.clear()
        answer.complete(freshMp4)

        assertThat(fake.calls).isEmpty()
        assertThat(playback.playback).isEqualTo(hls)
    }

    @Test
    fun `HLS heals by itself and is never read again here`() {
        playing(hls)

        fake.fail(forbidden())

        assertThat(reads).isEmpty()
        // The reel pool's refresher: the same playlist, where it was.
        assertThat(built).containsExactly(hls)
        assertThat(fake.calls.first()).isEqualTo("setMediaSource@42000")
    }

    @Test
    fun `behind another screen the player lets go, and back in front it is where it was, paused`() {
        playing(hls, atMs = 30_000L)
        fake.playWhenReady = true

        playback.hold()

        assertThat(fake.calls).containsExactly("playWhenReady=false", "stop").inOrder()
        assertThat(playback.held).isTrue()
        fake.calls.clear()

        playback.resume()

        // The playlist and position a stop keeps: no new source, no seek, no play.
        assertThat(fake.calls).containsExactly("prepare")
        assertThat(fake.positionMs).isEqualTo(30_000L)
        assertThat(fake.playWhenReady).isFalse()
        assertThat(playback.held).isFalse()
    }

    @Test
    fun `a video held on its last frame stays held until Replay`() {
        playing(hls)
        fake.state(Player.STATE_ENDED)

        playback.hold()
        fake.calls.clear()
        playback.resume()

        assertThat(fake.calls).isEmpty()
        assertThat(playback.heldAtEnd).isTrue()

        playback.replay()

        assertThat(fake.calls).containsExactly("seekTo=0", "prepare", "playWhenReady=true").inOrder()
        assertThat(playback.held).isFalse()
        assertThat(playback.heldAtEnd).isFalse()
    }

    @Test
    fun `nothing to play lets the player go and is never failed`() {
        playing(mp4)
        fake.fail(otherError())

        playback.clear()

        assertThat(playback.playback).isNull()
        assertThat(playback.failed.value).isFalse()
        assertThat(fake.calls.takeLast(2)).containsExactly("stop", "clearMediaItems").inOrder()
    }

    @Test
    fun `release stops listening before the player goes`() {
        playback.release()

        assertThat(fake.listeners).isEmpty()
        assertThat(fake.released).isTrue()
    }

    @Test
    fun `failed is idle with an error held, never buffering or ready`() {
        assertThat(isFailed(Player.STATE_IDLE, hasError = true)).isTrue()
        assertThat(isFailed(Player.STATE_IDLE, hasError = false)).isFalse()
        assertThat(isFailed(Player.STATE_BUFFERING, hasError = true)).isFalse()
        assertThat(isFailed(Player.STATE_READY, hasError = true)).isFalse()
        assertThat(isFailed(Player.STATE_ENDED, hasError = false)).isFalse()
    }

    @Test
    fun `the centre button retries a failed player, replays an ended one and otherwise plays or pauses`() {
        assertThat(transportTap(failed = true, ended = true)).isEqualTo(TransportTap.RETRY)
        assertThat(transportTap(failed = true, ended = false)).isEqualTo(TransportTap.RETRY)
        assertThat(transportTap(failed = false, ended = true)).isEqualTo(TransportTap.REPLAY)
        assertThat(transportTap(failed = false, ended = false)).isEqualTo(TransportTap.TOGGLE)
    }
}
