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
}
