package com.us.android.core.media

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Playlists must never be served from the cache.
 *
 * An .m3u8 body embeds signed segment URLs valid for five minutes. Caching one
 * leaves the player replaying dead links AND stops it requesting a fresh
 * playlist — the very request that would have healed it. Observed on a device
 * on 2026-08-18 as a black frame, 403 "Request has expired" from the object
 * store, and no gateway traffic at all.
 */
class PlaylistRoutingTest {

    @Test
    fun `master and child playlists are recognised`() {
        assertThat(isPlaylistPath("/v1/media/abc/hls/master.m3u8")).isTrue()
        assertThat(isPlaylistPath("/v1/media/abc/hls/360p.m3u8")).isTrue()
    }

    @Test
    fun `segments are not playlists`() {
        assertThat(isPlaylistPath("/media/u/a/hls/360p_000.ts")).isFalse()
    }

    @Test
    fun `case is ignored`() {
        assertThat(isPlaylistPath("/a/MASTER.M3U8")).isTrue()
    }

    @Test
    fun `a null or empty path is not a playlist`() {
        assertThat(isPlaylistPath(null)).isFalse()
        assertThat(isPlaylistPath("")).isFalse()
    }

    /**
     * This takes a PATH, which is why the caller passes `Uri.path` rather than
     * the whole URL. A signed segment carries a long query string, and
     * `Uri.path` is what strips it — matching against a full URL would let a
     * query mentioning `.m3u8` misclassify a segment and silently stop it
     * being cached. Documented here because the guarantee lives at the call
     * site, not in this function.
     */
    @Test
    fun `a real segment path is a segment`() {
        assertThat(isPlaylistPath("/media/user/a/b/hls/360p_000.ts")).isFalse()
    }
}
