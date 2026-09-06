package com.us.android.feature.post.createhub

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.publish.PublishKind
import com.us.android.core.media.publish.REEL_MAX_DURATION_MS
import org.junit.Test

/**
 * The duration and size gate (Tube, 2026-09-05): a reel is at most five
 * minutes and is offered as a video past that; a video has no length cap;
 * neither may exceed 500 MB; and a fact the client could not establish
 * never blocks a post.
 */
class VideoGateTest {

    private val fiveMinutes = 5L * 60L * 1_000L
    private val fiveHundredMb = 500L * 1024L * 1024L

    @Test
    fun `a reel at the cap passes and one millisecond over is offered as a video`() {
        assertThat(videoGate(PublishKind.REEL, VideoProbe(fiveMinutes, 1L))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.REEL, VideoProbe(fiveMinutes + 1, 1L)))
            .isEqualTo(VideoGate.TooLongForReel(fiveMinutes + 1))
    }

    @Test
    fun `a long video has no duration cap`() {
        assertThat(videoGate(PublishKind.LONG, VideoProbe(3L * 60L * 60L * 1_000L, 1L))).isEqualTo(VideoGate.Ok)
    }

    @Test
    fun `the upload ceiling applies to both kinds and outranks the reel cap`() {
        assertThat(videoGate(PublishKind.REEL, VideoProbe(1L, fiveHundredMb))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.LONG, VideoProbe(1L, fiveHundredMb))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.REEL, VideoProbe(1L, fiveHundredMb + 1)))
            .isEqualTo(VideoGate.TooLarge(fiveHundredMb + 1))
        assertThat(videoGate(PublishKind.LONG, VideoProbe(1L, fiveHundredMb + 1)))
            .isEqualTo(VideoGate.TooLarge(fiveHundredMb + 1))
        // Too long AND too large: the size is the one nothing can fix.
        assertThat(videoGate(PublishKind.REEL, VideoProbe(fiveMinutes * 2, fiveHundredMb + 1)))
            .isEqualTo(VideoGate.TooLarge(fiveHundredMb + 1))
    }

    @Test
    fun `unknown facts pass`() {
        assertThat(videoGate(PublishKind.REEL, null)).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.REEL, VideoProbe(null, null))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.REEL, VideoProbe(null, fiveHundredMb))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.REEL, VideoProbe(fiveMinutes, null))).isEqualTo(VideoGate.Ok)
    }

    @Test
    fun `only Ok allows a post`() {
        assertThat(VideoGate.Ok.allowsPost).isTrue()
        assertThat(VideoGate.TooLongForReel(1L).allowsPost).isFalse()
        assertThat(VideoGate.TooLarge(1L).allowsPost).isFalse()
    }

    /**
     * The five minutes now lives in `:core:media` (the Reels FEED applies the
     * same number and cannot see this module); this asserts the gate is still
     * judging by it and not by a second copy.
     */
    @Test
    fun `the constants are the agreed limits`() {
        assertThat(REEL_MAX_DURATION_MS).isEqualTo(300_000L)
        assertThat(MAX_UPLOAD_BYTES).isEqualTo(524_288_000L)
    }
}
