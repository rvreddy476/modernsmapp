package com.us.android.feature.post.createhub

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.publish.PublishKind
import com.us.android.core.media.publish.REEL_MAX_DURATION_MS
import org.junit.Test

/**
 * The duration gate (Tube, 2026-09-05; limits of 2026-10-21): a reel is at
 * most ten minutes and is offered as a video past that; a video has no
 * length cap and no size cap; and a fact the client could not establish
 * never blocks a post.
 */
class VideoGateTest {

    private val tenMinutes = 10L * 60L * 1_000L
    private val threeGb = 3L * 1024L * 1024L * 1024L

    @Test
    fun `a reel at 599 and 600 seconds passes and one at 601 is offered as a video`() {
        assertThat(videoGate(PublishKind.REEL, VideoProbe(599_000L, 1L))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.REEL, VideoProbe(600_000L, 1L))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.REEL, VideoProbe(601_000L, 1L)))
            .isEqualTo(VideoGate.TooLongForReel(601_000L))
    }

    @Test
    fun `a reel at the cap passes and one millisecond over is offered as a video`() {
        assertThat(videoGate(PublishKind.REEL, VideoProbe(tenMinutes, 1L))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.REEL, VideoProbe(tenMinutes + 1, 1L)))
            .isEqualTo(VideoGate.TooLongForReel(tenMinutes + 1))
    }

    /** The old five-minute line is gone: six minutes is a reel now. */
    @Test
    fun `a six minute reel is a reel`() {
        assertThat(videoGate(PublishKind.REEL, VideoProbe(6L * 60L * 1_000L, 1L))).isEqualTo(VideoGate.Ok)
    }

    @Test
    fun `a long video has no duration cap`() {
        assertThat(videoGate(PublishKind.LONG, VideoProbe(3L * 60L * 60L * 1_000L, 1L))).isEqualTo(VideoGate.Ok)
    }

    /** Founder, 2026-10-21: "remove the 500 MB limit". */
    @Test
    fun `there is no size cap for either kind`() {
        assertThat(videoGate(PublishKind.LONG, VideoProbe(2L * 60L * 60L * 1_000L, threeGb))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.LONG, VideoProbe(1L, 501L * 1024L * 1024L))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.REEL, VideoProbe(1L, threeGb))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.LONG, VideoProbe(null, Long.MAX_VALUE))).isEqualTo(VideoGate.Ok)
    }

    @Test
    fun `a long and huge reel is offered as a video, which can take it`() {
        assertThat(videoGate(PublishKind.REEL, VideoProbe(tenMinutes * 2, threeGb)))
            .isEqualTo(VideoGate.TooLongForReel(tenMinutes * 2))
    }

    @Test
    fun `unknown facts pass`() {
        assertThat(videoGate(PublishKind.REEL, null)).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.REEL, VideoProbe(null, null))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.REEL, VideoProbe(null, threeGb))).isEqualTo(VideoGate.Ok)
        assertThat(videoGate(PublishKind.REEL, VideoProbe(tenMinutes, null))).isEqualTo(VideoGate.Ok)
    }

    @Test
    fun `only Ok allows a post`() {
        assertThat(VideoGate.Ok.allowsPost).isTrue()
        assertThat(VideoGate.TooLongForReel(1L).allowsPost).isFalse()
    }

    @Test
    fun `the gate says ten minutes and points at the video path`() {
        assertThat(gateMessage(VideoGate.TooLongForReel(tenMinutes + 1))).contains("up to 10 minutes")
        assertThat(gateMessage(VideoGate.TooLongForReel(tenMinutes + 1))).contains("Post it as a Video")
        assertThat(gateMessage(VideoGate.Ok)).isEmpty()
    }

    /**
     * The ten minutes lives in `:core:media` (the Reels FEED applies the same
     * number and cannot see this module); this asserts the gate is judging by
     * it and not by a second copy.
     */
    @Test
    fun `the constant is the agreed limit`() {
        assertThat(REEL_MAX_DURATION_MS).isEqualTo(600_000L)
    }
}
