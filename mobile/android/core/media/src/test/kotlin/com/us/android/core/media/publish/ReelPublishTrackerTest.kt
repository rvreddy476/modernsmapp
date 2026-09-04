package com.us.android.core.media.publish

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The state the feed banner trusts: one publish at a time, progress that
 * only moves forward, and a finished state the user can dismiss but an
 * in-flight one they cannot.
 */
class ReelPublishTrackerTest {

    @Test
    fun `starts idle and walks the phases in order`() {
        val tracker = ReelPublishTracker()
        assertThat(tracker.state.value).isEqualTo(ReelPublishState.Idle)
        assertThat(tracker.isActive).isFalse()

        tracker.update(ReelPublishState.Preparing)
        assertThat(tracker.isActive).isTrue()
        tracker.update(ReelPublishState.Uploading(0.5f))
        tracker.update(ReelPublishState.Processing)
        tracker.update(ReelPublishState.Posting)
        assertThat(tracker.isActive).isTrue()
        tracker.update(ReelPublishState.Published("post-1"))

        assertThat(tracker.state.value).isEqualTo(ReelPublishState.Published("post-1"))
        assertThat(tracker.isActive).isFalse()
        assertThat(tracker.state.value.isTerminal).isTrue()
    }

    @Test
    fun `upload progress is clamped and never runs backwards`() {
        val tracker = ReelPublishTracker()

        tracker.update(ReelPublishState.Uploading(-0.2f))
        assertThat(tracker.state.value).isEqualTo(ReelPublishState.Uploading(0f))
        tracker.update(ReelPublishState.Uploading(0.42f))
        tracker.update(ReelPublishState.Uploading(0.40f))
        assertThat(tracker.state.value).isEqualTo(ReelPublishState.Uploading(0.42f))
        tracker.update(ReelPublishState.Uploading(1.7f))
        assertThat(tracker.state.value).isEqualTo(ReelPublishState.Uploading(1f))
    }

    @Test
    fun `a retry restarts the bar from zero because it passes through a non-upload phase`() {
        val tracker = ReelPublishTracker()
        tracker.update(ReelPublishState.Uploading(0.8f))
        tracker.update(ReelPublishState.Failed("The upload did not finish.", retryable = true))

        tracker.update(ReelPublishState.Preparing)
        tracker.update(ReelPublishState.Uploading(0f))

        assertThat(tracker.state.value).isEqualTo(ReelPublishState.Uploading(0f))
    }

    @Test
    fun `dismiss clears a finished state and leaves an in-flight one alone`() {
        val tracker = ReelPublishTracker()

        tracker.update(ReelPublishState.Processing)
        tracker.dismiss()
        assertThat(tracker.state.value).isEqualTo(ReelPublishState.Processing)

        tracker.update(ReelPublishState.Published("post-1"))
        tracker.dismiss()
        assertThat(tracker.state.value).isEqualTo(ReelPublishState.Idle)

        tracker.update(ReelPublishState.Failed("Could not post.", retryable = false))
        tracker.dismiss()
        assertThat(tracker.state.value).isEqualTo(ReelPublishState.Idle)
    }

    @Test
    fun `restore only wins while idle so a running worker is never overwritten`() {
        val tracker = ReelPublishTracker()

        assertThat(tracker.restoreIfIdle(ReelPublishState.Failed("Could not post.", retryable = true))).isTrue()
        assertThat(tracker.state.value).isInstanceOf(ReelPublishState.Failed::class.java)

        tracker.update(ReelPublishState.Uploading(0.3f))
        assertThat(tracker.restoreIfIdle(ReelPublishState.Processing)).isFalse()
        assertThat(tracker.state.value).isEqualTo(ReelPublishState.Uploading(0.3f))
    }

    @Test
    fun `reset clears any state because the publish was discarded`() {
        val tracker = ReelPublishTracker()
        tracker.update(ReelPublishState.Uploading(0.3f))

        tracker.reset()

        assertThat(tracker.state.value).isEqualTo(ReelPublishState.Idle)
    }
}
