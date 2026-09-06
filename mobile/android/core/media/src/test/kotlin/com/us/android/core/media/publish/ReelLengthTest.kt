package com.us.android.core.media.publish

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The five-minute line (founder, 2026-09-06): anything longer is a video and
 * belongs in Tube, never in the Reels feed — whatever the row calls itself.
 */
class ReelLengthTest {

    private val fiveMinutes = 5L * 60L * 1_000L

    @Test
    fun `the cap is five minutes`() {
        assertThat(REEL_MAX_DURATION_MS).isEqualTo(fiveMinutes)
    }

    @Test
    fun `a flick up to five minutes plays in Reels`() {
        assertThat(playsInReels("flick", 0L)).isTrue()
        assertThat(playsInReels("flick", fiveMinutes - 1)).isTrue()
        assertThat(playsInReels("flick", fiveMinutes)).isTrue()
    }

    /** The defensive half: a mistagged long capture cannot reach Reels. */
    @Test
    fun `a flick over five minutes does not play in Reels`() {
        assertThat(playsInReels("flick", fiveMinutes + 1)).isFalse()
        assertThat(playsInReels("flick", 10L * 60L * 1_000L)).isFalse()
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
