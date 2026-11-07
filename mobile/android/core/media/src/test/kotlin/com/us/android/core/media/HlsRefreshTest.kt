package com.us.android.core.media

import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSpec
import androidx.media3.datasource.HttpDataSource
import androidx.media3.exoplayer.ExoPlaybackException
import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.sound.FakeExoPlayer
import com.us.android.core.media.sound.fakeMediaSource
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.IOException

/**
 * HLS kept playable past its segment signatures (2026-11-07): a 403 on a
 * segment, or the playlist's `X-HLS-Expires-At` passed at a play or a loop,
 * re-prepares the SAME playback where it was, a bounded number of times;
 * progressive playback, which a re-prepare cannot heal, is left alone.
 *
 * Robolectric for `Uri`, which the load events and the HTTP error carry.
 */
@OptIn(UnstableApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class HlsRefreshTest {

    private val hls = Playback(url = "https://api/v1/media/m1/hls/master.m3u8", kind = PlaybackKind.Hls)
    private val mp4 = Playback(url = "https://cdn/m1/original.mp4", kind = PlaybackKind.Progressive)

    private var clock = 1_000_000L
    private val rebuilt = mutableListOf<Playback>()
    private val fake = FakeExoPlayer("reel")
    private val refresh = HlsRefresh(
        fake.player,
        { playback ->
            rebuilt += playback
            fakeMediaSource()
        },
        now = { clock },
    )

    private fun forbidden(): PlaybackException {
        val http = HttpDataSource.InvalidResponseCodeException(
            403,
            "Forbidden",
            IOException("expired"),
            emptyMap(),
            DataSpec(Uri.parse("https://cdn/seg-7.ts")),
            ByteArray(0),
        )
        return ExoPlaybackException.createForSource(http, PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS)
    }

    private fun expiresAt(iso: String) = mapOf("x-hls-expires-at" to listOf(iso))

    @Test
    fun `a 403 on a segment re-prepares the same playback where it was, still playing`() {
        refresh.prepared(hls)
        fake.positionMs = 42_000L
        fake.playWhenReady = true

        fake.fail(forbidden())

        assertThat(rebuilt).containsExactly(hls)
        assertThat(fake.calls.takeLast(3))
            .containsExactly("setMediaSource@42000", "prepare", "playWhenReady=true")
            .inOrder()
    }

    @Test
    fun `another error before the signatures expire is left alone`() {
        refresh.prepared(hls)

        fake.fail()

        assertThat(rebuilt).isEmpty()
    }

    @Test
    fun `a play after the playlist's expiry fetches fresh playlists first`() {
        refresh.prepared(hls)
        fake.loaded(expiresAt("1970-01-01T00:20:00Z")) // 1,200,000 ms

        fake.requestPlay(true)
        assertThat(rebuilt).isEmpty()

        clock = 1_200_000L
        fake.requestPlay(false)
        fake.requestPlay(true)
        assertThat(rebuilt).containsExactly(hls)
    }

    @Test
    fun `a reel's loop after the expiry refreshes, a seek does not`() {
        refresh.prepared(hls)
        fake.loaded(expiresAt("1970-01-01T00:16:40Z")) // 1,000,000 ms: already due
        fake.jumpTo(0L, Player.DISCONTINUITY_REASON_SEEK)
        assertThat(rebuilt).isEmpty()

        fake.jumpTo(0L, Player.DISCONTINUITY_REASON_AUTO_TRANSITION)

        assertThat(rebuilt).containsExactly(hls)
    }

    @Test
    fun `the earliest expiry of the playlists is the one that counts, and segments say nothing`() {
        refresh.prepared(hls)
        fake.loaded(expiresAt("1970-01-01T01:00:00Z"))
        fake.loaded(expiresAt("1970-01-01T00:16:40Z"))
        fake.loaded(expiresAt("1970-01-01T00:00:01Z"), dataType = C.DATA_TYPE_MEDIA)

        fake.requestPlay(true)

        assertThat(rebuilt).hasSize(1)
    }

    @Test
    fun `a server that keeps refusing gets two tries, then the error shows, and READY resets the count`() {
        refresh.prepared(hls)
        repeat(4) { fake.fail(forbidden()) }
        assertThat(rebuilt).hasSize(HlsRefresh.MAX_REFRESHES)

        fake.becomeReady()
        fake.fail(forbidden())
        assertThat(rebuilt).hasSize(HlsRefresh.MAX_REFRESHES + 1)
    }

    @Test
    fun `a new video starts a fresh budget and a fresh clock`() {
        refresh.prepared(hls)
        repeat(3) { fake.fail(forbidden()) }
        fake.loaded(expiresAt("1970-01-01T00:00:01Z"))

        refresh.prepared(hls.copy(url = "https://api/v1/media/m2/hls/master.m3u8"))
        fake.requestPlay(true)
        assertThat(rebuilt).hasSize(HlsRefresh.MAX_REFRESHES)

        fake.fail(forbidden())
        assertThat(rebuilt).hasSize(HlsRefresh.MAX_REFRESHES + 1)
    }

    @Test
    fun `progressive playback is never re-prepared`() {
        refresh.prepared(mp4)

        fake.fail(forbidden())

        assertThat(rebuilt).isEmpty()
    }

    @Test
    fun `removed, it hears nothing more`() {
        refresh.prepared(hls)
        refresh.remove()

        assertThat(fake.listeners).isEmpty()
        assertThat(fake.analytics).isEmpty()
    }

    @Test
    fun `the expiry header is read in any case and an unreadable one is none`() {
        assertThat(hlsExpiresAtMillis(mapOf("X-HLS-Expires-At" to listOf("2026-11-07T10:00:00Z"))))
            .isEqualTo(1_794_045_600_000L)
        assertThat(hlsExpiresAtMillis(expiresAt("2026-11-07T15:30:00+05:30"))).isEqualTo(1_794_045_600_000L)
        assertThat(hlsExpiresAtMillis(expiresAt("tomorrow"))).isNull()
        assertThat(hlsExpiresAtMillis(emptyMap())).isNull()
    }

    @Test
    fun `only a 403 is an expired signature`() {
        assertThat(forbidden().isExpiredSignature()).isTrue()
        assertThat(PlaybackException("x", null, PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS).isExpiredSignature())
            .isFalse()
    }
}
