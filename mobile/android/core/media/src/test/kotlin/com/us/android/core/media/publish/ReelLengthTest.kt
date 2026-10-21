package com.us.android.core.media.publish

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The ten-minute line (founder, 2026-09-06; raised from five to ten on
 * 2026-10-21): anything longer is a video and belongs in Tube, never in the
 * Reels feed — whatever the row calls itself.
 */
class ReelLengthTest {

    private val tenMinutes = 10L * 60L * 1_000L

    @Test
    fun `the cap is ten minutes`() {
        assertThat(REEL_MAX_DURATION_MS).isEqualTo(tenMinutes)
        assertThat(REEL_MAX_MINUTES).isEqualTo(10L)
    }

    /** The camera's per-clip limit is the reel cap, so every recording is postable as a reel. */
    @Test
    fun `the camera records at most ten minutes per clip`() {
        assertThat(CAMERA_MAX_RECORDING_MS).isEqualTo(tenMinutes)
    }

    @Test
    fun `a flick up to ten minutes plays in Reels`() {
        assertThat(playsInReels("flick", 0L)).isTrue()
        assertThat(playsInReels("flick", 5L * 60L * 1_000L + 1)).isTrue()
        assertThat(playsInReels("flick", 599_000L)).isTrue()
        assertThat(playsInReels("flick", tenMinutes - 1)).isTrue()
        assertThat(playsInReels("flick", tenMinutes)).isTrue()
    }

    /** The defensive half: a mistagged long capture cannot reach Reels. */
    @Test
    fun `a flick over ten minutes does not play in Reels`() {
        assertThat(playsInReels("flick", tenMinutes + 1)).isFalse()
        assertThat(playsInReels("flick", 601_000L)).isFalse()
        assertThat(playsInReels("flick", 60L * 60L * 1_000L)).isFalse()
    }

    @Test
    fun `a long video never plays in Reels, however short it claims to be`() {
        assertThat(playsInReels(LONG_VIDEO_CONTENT_TYPE, 1_000L)).isFalse()
        assertThat(playsInReels(LONG_VIDEO_CONTENT_TYPE, 0L)).isFalse()
    }

    /** Zero is "not known" — an image, or a row from a server without `duration_ms`. */
    @Test
    fun `an unknown length never excludes a post`() {
        assertThat(playsInReels("post", 0L)).isTrue()
    }
}
