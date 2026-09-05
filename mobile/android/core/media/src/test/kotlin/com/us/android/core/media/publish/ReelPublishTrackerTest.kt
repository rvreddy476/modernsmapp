package com.us.android.core.media.publish

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The queue the pending tiles trust: one entry per creation key in enqueue
 * order, progress that only moves forward, and a finished entry the user
 * can dismiss but an in-flight one they cannot.
 */
class ReelPublishTrackerTest {

    private val one = ReelPublishPreview("key-1", "/cache/key-1.jpg", "first")
    private val two = ReelPublishPreview("key-2", "/cache/key-2.jpg", "second")

    @Test
    fun `starts empty and walks one publish through the phases in order`() {
        val tracker = ReelPublishTracker()
        assertThat(tracker.items.value).isEmpty()
        assertThat(tracker.isActive).isFalse()

        tracker.setPreview(one)
        tracker.update("key-1", ReelPublishState.Preparing)
        assertThat(tracker.isActive).isTrue()
        tracker.update("key-1", ReelPublishState.Uploading(0.5f))
        tracker.update("key-1", ReelPublishState.Processing)
        tracker.update("key-1", ReelPublishState.Posting)
        assertThat(tracker.isActive).isTrue()
        tracker.update("key-1", ReelPublishState.Published("post-1"))

        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Published("post-1"))
        assertThat(tracker.isActive).isFalse()
        assertThat(tracker.stateOf("key-1").isTerminal).isTrue()
    }

    /** Two reels started back to back: both tracked, the first stays first, each with its own progress. */
    @Test
    fun `two pending publishes keep their order and their own progress`() {
        val tracker = ReelPublishTracker()
        tracker.setPreview(one)
        tracker.update("key-1", ReelPublishState.Uploading(0.42f))
        tracker.setPreview(two)
        tracker.update("key-2", ReelPublishState.Preparing)

        assertThat(tracker.items.value.map { it.creationKey }).containsExactly("key-1", "key-2").inOrder()
        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Uploading(0.42f))
        assertThat(tracker.stateOf("key-2")).isEqualTo(ReelPublishState.Preparing)
        assertThat(tracker.items.value.all { it.isDrawable }).isTrue()

        tracker.update("key-2", ReelPublishState.Uploading(0.1f))
        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Uploading(0.42f))
    }

    @Test
    fun `upload progress is clamped and never runs backwards`() {
        val tracker = ReelPublishTracker()

        tracker.update("key-1", ReelPublishState.Uploading(-0.2f))
        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Uploading(0f))
        tracker.update("key-1", ReelPublishState.Uploading(0.42f))
        tracker.update("key-1", ReelPublishState.Uploading(0.40f))
        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Uploading(0.42f))
        tracker.update("key-1", ReelPublishState.Uploading(1.7f))
        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Uploading(1f))
    }

    @Test
    fun `a retry restarts the bar from zero because it passes through a non-upload phase`() {
        val tracker = ReelPublishTracker()
        tracker.update("key-1", ReelPublishState.Uploading(0.8f))
        tracker.update("key-1", ReelPublishState.Failed("The upload did not finish.", retryable = true))

        tracker.update("key-1", ReelPublishState.Preparing)
        tracker.update("key-1", ReelPublishState.Uploading(0f))

        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Uploading(0f))
    }

    @Test
    fun `dismiss removes a finished entry and leaves an in-flight one alone`() {
        val tracker = ReelPublishTracker()
        tracker.setPreview(one)

        tracker.update("key-1", ReelPublishState.Processing)
        tracker.dismiss("key-1")
        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Processing)

        tracker.update("key-1", ReelPublishState.Published("post-1"))
        tracker.dismiss("key-1")
        assertThat(tracker.items.value).isEmpty()

        tracker.update("key-1", ReelPublishState.Failed("Could not post.", retryable = false))
        tracker.dismiss("key-1")
        assertThat(tracker.items.value).isEmpty()
    }

    @Test
    fun `dismissing one entry leaves the others where they were`() {
        val tracker = ReelPublishTracker()
        tracker.setPreview(one)
        tracker.setPreview(two)
        tracker.update("key-1", ReelPublishState.Published("post-1"))
        tracker.update("key-2", ReelPublishState.Uploading(0.3f))

        tracker.dismiss("key-1")

        assertThat(tracker.items.value.map { it.creationKey }).containsExactly("key-2")
        assertThat(tracker.previewOf("key-2")).isEqualTo(two)
    }

    @Test
    fun `restore only wins while idle so a running worker is never overwritten`() {
        val tracker = ReelPublishTracker()

        assertThat(tracker.restoreIfIdle("key-1", ReelPublishState.Failed("Could not post.", retryable = true)))
            .isTrue()
        assertThat(tracker.stateOf("key-1")).isInstanceOf(ReelPublishState.Failed::class.java)

        tracker.update("key-1", ReelPublishState.Uploading(0.3f))
        assertThat(tracker.restoreIfIdle("key-1", ReelPublishState.Processing)).isFalse()
        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Uploading(0.3f))
    }

    @Test
    fun `reset drops an entry whatever its state because the publish was discarded`() {
        val tracker = ReelPublishTracker()
        tracker.setPreview(one)
        tracker.update("key-1", ReelPublishState.Uploading(0.3f))

        tracker.reset("key-1")

        assertThat(tracker.items.value).isEmpty()
        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Idle)
        assertThat(tracker.previewOf("key-1")).isNull()
    }

    /**
     * A state without a preview is not drawable — a worker restarted by
     * WorkManager reports before the controller has read the record — and
     * the preview arriving later completes the same entry, not a second one.
     */
    @Test
    fun `a state before its preview is tracked but not drawn until the preview lands`() {
        val tracker = ReelPublishTracker()

        tracker.update("key-1", ReelPublishState.Uploading(0.2f))
        assertThat(tracker.items.value.single().isDrawable).isFalse()

        tracker.setPreview(one)
        assertThat(tracker.items.value).hasSize(1)
        assertThat(tracker.items.value.single().isDrawable).isTrue()
        assertThat(tracker.stateOf("key-1")).isEqualTo(ReelPublishState.Uploading(0.2f))
    }
}
