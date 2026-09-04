package com.us.android.feature.feed.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/** The banner's one piece of behaviour of its own: good news goes away by itself. */
@OptIn(ExperimentalCoroutinesApi::class)
class ReelPublishBannerViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule(StandardTestDispatcher())

    private class FakeActions(private val tracker: ReelPublishTracker) : ReelPublishActions {
        var retries = 0
        var discards = 0
        var dismisses = 0
        override fun retry() {
            retries++
        }

        override fun discard() {
            discards++
        }

        override fun dismiss() {
            dismisses++
            tracker.dismiss()
        }
    }

    @Test
    fun `a live reel dismisses itself after six seconds`() = runTest {
        val tracker = ReelPublishTracker()
        val actions = FakeActions(tracker)
        val vm = ReelPublishBannerViewModel(tracker, actions)

        tracker.update(ReelPublishState.Published("post-1"))
        advanceTimeBy(ReelPublishBannerViewModel.AUTO_DISMISS_MILLIS - 1)
        assertThat(actions.dismisses).isEqualTo(0)
        assertThat(vm.state.value).isEqualTo(ReelPublishState.Published("post-1"))

        advanceTimeBy(2)
        assertThat(actions.dismisses).isEqualTo(1)
        assertThat(vm.state.value).isEqualTo(ReelPublishState.Idle)
    }

    @Test
    fun `a failure waits for a decision`() = runTest {
        val tracker = ReelPublishTracker()
        val actions = FakeActions(tracker)
        val vm = ReelPublishBannerViewModel(tracker, actions)

        tracker.update(ReelPublishState.Failed("Couldn't post.", retryable = true))
        advanceTimeBy(60_000)

        assertThat(actions.dismisses).isEqualTo(0)
        assertThat(vm.state.value).isInstanceOf(ReelPublishState.Failed::class.java)
    }

    @Test
    fun `a new publish within the window cancels the old dismiss`() = runTest {
        val tracker = ReelPublishTracker()
        val actions = FakeActions(tracker)
        ReelPublishBannerViewModel(tracker, actions)

        tracker.update(ReelPublishState.Published("post-1"))
        advanceTimeBy(3_000)
        tracker.update(ReelPublishState.Uploading(0.1f))
        advanceTimeBy(10_000)

        assertThat(actions.dismisses).isEqualTo(0)
        assertThat(tracker.state.value).isEqualTo(ReelPublishState.Uploading(0.1f))
    }

    @Test
    fun `retry discard and dismiss reach the worker's side`() = runTest {
        val tracker = ReelPublishTracker()
        val actions = FakeActions(tracker)
        val vm = ReelPublishBannerViewModel(tracker, actions)

        vm.retry()
        vm.discard()
        vm.dismiss()

        assertThat(actions.retries).isEqualTo(1)
        assertThat(actions.discards).isEqualTo(1)
        assertThat(actions.dismisses).isEqualTo(1)
    }
}
