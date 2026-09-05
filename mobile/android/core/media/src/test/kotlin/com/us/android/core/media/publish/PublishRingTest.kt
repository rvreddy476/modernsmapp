package com.us.android.core.media.publish

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The pending tile's ring: upload bytes become a sweep, everything else in flight spins. */
class PublishRingTest {

    @Test
    fun `an upload fraction is a determinate sweep`() {
        assertThat(ReelPublishState.Uploading(0.42f).ring()).isEqualTo(PublishRing.Determinate(0.42f))
        assertThat(ReelPublishState.Uploading(0f).ring()).isEqualTo(PublishRing.Determinate(0f))
    }

    @Test
    fun `a fraction outside 0 to 1 is clamped`() {
        assertThat(ReelPublishState.Uploading(1.4f).ring()).isEqualTo(PublishRing.Determinate(1f))
        assertThat(ReelPublishState.Uploading(-0.2f).ring()).isEqualTo(PublishRing.Determinate(0f))
    }

    @Test
    fun `preparing, processing and posting spin`() {
        assertThat(ReelPublishState.Preparing.ring()).isEqualTo(PublishRing.Indeterminate)
        assertThat(ReelPublishState.Processing.ring()).isEqualTo(PublishRing.Indeterminate)
        assertThat(ReelPublishState.Posting.ring()).isEqualTo(PublishRing.Indeterminate)
    }

    @Test
    fun `published is the full ring and a failure has none`() {
        assertThat(ReelPublishState.Published("p1").ring()).isEqualTo(PublishRing.Determinate(1f))
        assertThat(ReelPublishState.Failed("no", retryable = true).ring()).isEqualTo(PublishRing.None)
        assertThat(ReelPublishState.Idle.ring()).isEqualTo(PublishRing.None)
    }

    @Test
    fun `the label says the percent while uploading`() {
        assertThat(ReelPublishState.Uploading(0.657f).ringLabel()).isEqualTo("Uploading 65%")
        assertThat(ReelPublishState.Processing.ringLabel()).isEqualTo("Processing")
        assertThat(ReelPublishState.Failed("x", retryable = false).ringLabel()).isEqualTo("Couldn't post")
    }

    /** The number inside the ring (founder, 2026-09-05): "42 %" while bytes go up, nothing otherwise. */
    @Test
    fun `the percent inside the ring exists only while uploading`() {
        assertThat(ReelPublishState.Uploading(0.42f).ringPercentLabel()).isEqualTo("42 %")
        assertThat(ReelPublishState.Uploading(0.999f).ringPercentLabel()).isEqualTo("99 %")
        assertThat(ReelPublishState.Uploading(1.5f).ringPercentLabel()).isEqualTo("100 %")
        assertThat(ReelPublishState.Uploading(-1f).ringPercentLabel()).isEqualTo("0 %")
        assertThat(ReelPublishState.Preparing.ringPercentLabel()).isNull()
        assertThat(ReelPublishState.Processing.ringPercentLabel()).isNull()
        assertThat(ReelPublishState.Posting.ringPercentLabel()).isNull()
        assertThat(ReelPublishState.Published("p1").ringPercentLabel()).isNull()
        assertThat(ReelPublishState.Failed("x", retryable = true).ringPercentLabel()).isNull()
    }
}
