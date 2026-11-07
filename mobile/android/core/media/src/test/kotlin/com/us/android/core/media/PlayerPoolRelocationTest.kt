package com.us.android.core.media

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * A pooled player follows its video when the list's indices shift under it
 * — a bounded feed dropping its oldest page behind the reader (audit
 * 2026-10-30, A2), or a reel pinned above page 0 — instead of the video on
 * screen being prepared again from the top.
 */
class PlayerPoolRelocationTest {

    private val video = Playback(url = "https://cdn/p1/master.m3u8", kind = PlaybackKind.Hls)
    private val other = Playback(url = "https://cdn/p2/master.m3u8", kind = PlaybackKind.Hls)

    @Test
    fun `the same video at another index is found there`() {
        val held = heldElsewhere(
            pageIndex = 125,
            playback = video,
            contentId = "p1",
            contents = mapOf(140 to "p1", 141 to "p2"),
            playbacks = mapOf(140 to video, 141 to other),
        )

        assertThat(held).isEqualTo(140)
    }

    @Test
    fun `an unnamed video is never moved`() {
        assertThat(heldElsewhere(125, video, contentId = null, mapOf(140 to "p1"), mapOf(140 to video))).isNull()
    }

    /** The same post with a new source (an audio track) re-prepares where it is. */
    @Test
    fun `the same post with another source is not a move`() {
        assertThat(heldElsewhere(125, video, "p1", mapOf(140 to "p1"), mapOf(140 to other))).isNull()
    }

    @Test
    fun `a video already at its own index is not a move`() {
        assertThat(heldElsewhere(140, video, "p1", mapOf(140 to "p1"), mapOf(140 to video))).isNull()
    }

    @Test
    fun `a swap exchanges two entries, or moves one into an empty slot`() {
        val both = mutableMapOf(1 to "a", 2 to "b")
        both.swap(1, 2)
        assertThat(both).containsExactly(1, "b", 2, "a")

        val one = mutableMapOf(140 to "p1", 141 to "p2")
        one.swap(140, 125)
        assertThat(one).containsExactly(125, "p1", 141, "p2")
    }
}
